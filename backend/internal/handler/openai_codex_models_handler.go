package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexModels serves the Codex models manifest for Codex clients.
//
// Codex CLI and the Codex desktop app refresh their model picker from
// GET {base_url}/models?client_version=... (custom provider mode) or
// GET /backend-api/codex/models (chatgpt_base_url mode). Both routes land
// here. ChatGPT manifests are proxied verbatim; custom API key manifests receive
// provider-compatibility normalization and use a short-lived, asynchronously
// revalidated cache to tolerate canceled client requests.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	if c.Request.Context().Err() != nil {
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if !fixedEndpointTargetPlatformAllowed(c, apiKey, "", service.PlatformOpenAI) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for OpenAI groups")
		return
	}

	reqLog := requestLogger(c, "handler.openai_gateway.codex_models")
	streamStarted := false

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	profitVetoCount := 0
	var lastUpstreamErr error

	for {
		selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapabilityOptions(
			c.Request.Context(),
			apiKey.GroupID,
			"",
			"",
			"",
			failedAccountIDs,
			service.OpenAIUpstreamTransportHTTPSSE,
			"",
			false,
			service.OpenAIAccountSchedulingOptions{
				CanTemporarilyOverflow: false,
				Platform:               service.PlatformOpenAI,
			},
		)
		if err != nil || selection == nil || selection.Account == nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			return
		}
		account := selection.Account
		accountRelease, slotResult, _ := h.acquireResponsesAccountSlot(
			c,
			apiKey.GroupID,
			"",
			selection,
			false,
			false,
			&streamStarted,
			reqLog,
		)
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
		// 让 ops 错误日志携带实际选中的上游账号，便于定位失效账号（#4544）。
		setOpsSelectedAccount(c, account.ID, account.Platform)

		manifest, err := func() (*service.CodexModelsManifest, error) {
			if accountRelease != nil {
				defer accountRelease()
			}
			return h.gatewayService.FetchCodexModelsManifest(c.Request.Context(), account, c.Query("client_version"), c.GetHeader("If-None-Match"))
		}()
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if service.IsRetryableCodexModelsManifestError(err) && switchCount < maxAccountSwitches {
				failedAccountIDs[account.ID] = struct{}{}
				switchCount++
				lastUpstreamErr = err
				continue
			}
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
		service.SetActualOpenAIUpstreamEndpoint(c, manifest.UpstreamEndpoint)
		if !account.IsShadow() {
			h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, manifest.ResponseHeaders)
		}

		if manifest.ETag != "" {
			c.Header("ETag", manifest.ETag)
		}
		if manifest.NotModified {
			c.Status(http.StatusNotModified)
			return
		}
		c.Data(http.StatusOK, "application/json", manifest.Body)
		return
	}
}
