package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// CodexMemories proxies Codex's unary memory summarization endpoint.
func (h *OpenAIGatewayHandler) CodexMemories(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)
	setOpenAIClientTransportHTTP(c)
	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.codex_memories",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		logRequestBodyParseFailure(reqLog, body, nil)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	modelResult := gjson.GetBytes(body, "model")
	if modelResult.Type != gjson.String || strings.TrimSpace(modelResult.String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	requestedModel := strings.TrimSpace(modelResult.String())
	if !groupModelsListAllowsModel(c, apiKey, requestedModel) {
		h.errorResponse(c, http.StatusNotFound, "model_not_found", groupModelsListModelNotFoundMessage(c, requestedModel))
		return
	}
	if !fixedEndpointTargetPlatformAllowed(c, apiKey, requestedModel, service.PlatformOpenAI) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex memories are only available for OpenAI groups")
		return
	}

	clientRequestModel := clientRequestedModel(c, requestedModel)
	routingModel := requestedModel
	if resolvedModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok {
		routingModel = resolvedModel
	}
	reqLog = reqLog.With(
		zap.String("model", clientRequestModel),
		zap.String("routing_model", routingModel),
	)
	setOpsRequestContext(c, clientRequestModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	if decision := h.checkSecurityAudit(
		c,
		reqLog,
		apiKey,
		subject,
		service.ContentModerationProtocolOpenAICodexMemory,
		requestedModel,
		body,
	); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}

	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, routingModel)
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}
	if err := h.billingCacheService.CheckBillingEligibility(
		c.Request.Context(),
		apiKey.User,
		apiKey,
		apiKey.Group,
		subscription,
		service.QuotaPlatform(c.Request.Context(), apiKey),
	); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	sessionHash := h.gatewayService.GenerateExplicitSessionHash(c, body)
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	switchCount := 0
	profitVetoCount := 0
	routingStart := time.Now()

	for {
		selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapabilityOptions(
			c.Request.Context(),
			apiKey.GroupID,
			"",
			sessionHash,
			routingModel,
			failedAccountIDs,
			service.OpenAIUpstreamTransportHTTPSSE,
			service.OpenAIEndpointCapabilityCodexDirect,
			false,
			service.OpenAIAccountSchedulingOptions{
				CanTemporarilyOverflow: true,
				Platform:               service.PlatformOpenAI,
			},
		)
		if err != nil || selection == nil || selection.Account == nil {
			if failoverClientGone(c) {
				reqLog.Info("openai.codex_memories.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			if len(failedAccountIDs) == 0 {
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, clientRequestModel, routingModel, service.PlatformOpenAI)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				message := cls.Message
				if !cls.ModelNotFound {
					message = "No available compatible OAuth accounts"
				}
				h.errorResponse(c, cls.Status, cls.ErrType, message)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			return
		}

		account := selection.Account
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		setOpsSelectedAccount(c, account.ID, account.Platform)
		accountRelease, slotResult, resolvedSessionHash := h.acquireResponsesAccountSlot(
			c,
			apiKey.GroupID,
			sessionHash,
			selection,
			false,
			false,
			&streamStarted,
			reqLog,
		)
		sessionHash = resolvedSessionHash
		if slotResult == openAISlotAcquireProfitVetoed {
			if !recordOpenAIProfitVeto(failedAccountIDs, account.ID, &profitVetoCount) {
				h.handleOpenAIProfitVetoExhausted(c, streamStarted, reqLog, profitVetoCount)
				return
			}
			continue
		}
		if slotResult != openAISlotAcquireOK {
			return
		}
		account = selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())

		writerSizeBeforeForward := c.Writer.Size()
		forwardStart := time.Now()
		result, err := func() (*service.OpenAIForwardResult, error) {
			if accountRelease != nil {
				defer accountRelease()
			}
			return h.gatewayService.ForwardCodexMemories(
				c.Request.Context(),
				c,
				account,
				body,
				requestedModel,
				channelMapping.MappedModel,
			)
		}()
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())

		if err == nil {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, account.GetMappedModel(routingModel), true, nil)
			if result != nil {
				if !account.IsShadow() {
					h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
				}
				h.recordCodexDirectUsage(c, apiKey, account, subscription, channelMapping, requestedModel, body, result, subject.UserID)
			}
			return
		}

		var failoverErr *service.UpstreamFailoverError
		if !errors.As(err, &failoverErr) {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, account.GetMappedModel(routingModel), false, nil)
			if c.Writer.Size() == writerSizeBeforeForward {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			reqLog.Warn("openai.codex_memories.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			return
		}

		h.gatewayService.ReportOpenAIAccountScheduleResult(account.ID, account.GetMappedModel(routingModel), false, nil)
		if c.Writer.Size() != writerSizeBeforeForward {
			h.handleFailoverExhausted(c, failoverErr, true)
			return
		}
		if failoverClientGone(c) {
			reqLog.Info("openai.codex_memories.failover_aborted_client_disconnected",
				zap.Int64("account_id", account.ID),
				zap.Int("upstream_status", failoverErr.StatusCode),
			)
			return
		}
		if failoverErr.RetryableOnSameAccount {
			retryLimit := account.GetPoolModeRetryCount()
			if sameAccountRetryCount[account.ID] < retryLimit {
				sameAccountRetryCount[account.ID]++
				select {
				case <-c.Request.Context().Done():
					return
				case <-time.After(sameAccountRetryDelay):
				}
				continue
			}
		}
		h.gatewayService.RecordOpenAIAccountSwitch()
		failedAccountIDs[account.ID] = struct{}{}
		lastFailoverErr = failoverErr
		if switchCount >= h.maxAccountSwitches {
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		switchCount++
		if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		reqLog.Warn("openai.codex_memories.upstream_failover_switching",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("switch_count", switchCount),
		)
	}
}

func (h *OpenAIGatewayHandler) recordCodexDirectUsage(
	c *gin.Context,
	apiKey *service.APIKey,
	account *service.Account,
	subscription *service.UserSubscription,
	channelMapping service.ChannelMappingResult,
	requestedModel string,
	body []byte,
	result *service.OpenAIForwardResult,
	userID int64,
) {
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	sessionID := service.ExtractClientSessionID(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := resolveOpenAIUpstreamEndpoint(c, account, result)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)

	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: requestPayloadHash,
			APIKeyService:      h.apiKeyService,
			QuotaPlatform:      quotaPlatform,
			SessionID:          sessionID,
			ChannelUsageFields: channelMapping.ToUsageFields(requestedModel, result.UpstreamModel),
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.codex_memories"),
				zap.Int64("user_id", userID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.String("model", requestedModel),
				zap.Int64("account_id", account.ID),
			).Error("openai.codex_memories.record_usage_failed", zap.Error(err))
		}
	})
}
