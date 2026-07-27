package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	openAICodexDirectImagesGenerationsPath = "/backend-api/codex/images/generations"
	openAICodexDirectImagesEditsPath       = "/backend-api/codex/images/edits"
	openAICodexDirectMemoriesPath          = "/backend-api/codex/memories/trace_summarize"

	openAICodexDirectImagesGenerationsURL = "https://chatgpt.com" + openAICodexDirectImagesGenerationsPath
	openAICodexDirectImagesEditsURL       = "https://chatgpt.com" + openAICodexDirectImagesEditsPath
	openAICodexDirectMemoriesURL          = "https://chatgpt.com" + openAICodexDirectMemoriesPath
)

func (s *OpenAIGatewayService) forwardOpenAICodexDirectImages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	targetURL := openAICodexDirectImagesGenerationsURL
	upstreamPath := openAICodexDirectImagesGenerationsPath
	if parsed.IsEdits() {
		targetURL = openAICodexDirectImagesEditsURL
		upstreamPath = openAICodexDirectImagesEditsPath
	}

	result, responseBody, err := s.forwardOpenAICodexDirectJSON(
		ctx,
		c,
		account,
		body,
		parsed.Model,
		channelMappedModel,
		targetURL,
		upstreamPath,
	)
	if err != nil || result == nil {
		return result, err
	}
	result.ImageCount = extractOpenAIImageCountFromJSONBytes(responseBody)
	if result.ImageCount == 0 {
		result.ImageCount = parsed.N
	}
	result.ImageSize = parsed.SizeTier
	result.ImageInputSize = parsed.Size
	result.ImageOutputSizes = collectOpenAIResponseImageOutputSizesFromJSONBytes(responseBody)
	return result, nil
}

// ForwardCodexMemories forwards the unary memory summarization protocol used
// by current Codex clients without translating its evolving JSON schema.
func (s *OpenAIGatewayService) ForwardCodexMemories(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	requestedModel string,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	result, _, err := s.forwardOpenAICodexDirectJSON(
		ctx,
		c,
		account,
		body,
		requestedModel,
		channelMappedModel,
		openAICodexDirectMemoriesURL,
		openAICodexDirectMemoriesPath,
	)
	return result, err
}

func (s *OpenAIGatewayService) forwardOpenAICodexDirectJSON(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	requestedModel string,
	channelMappedModel string,
	targetURL string,
	upstreamPath string,
) (*OpenAIForwardResult, []byte, error) {
	if s == nil || c == nil || account == nil {
		return nil, nil, fmt.Errorf("service, context, and account are required")
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return nil, nil, fmt.Errorf("codex direct endpoints require an OpenAI OAuth account")
	}

	upstreamModel := strings.TrimSpace(channelMappedModel)
	if upstreamModel == "" {
		upstreamModel = strings.TrimSpace(requestedModel)
	}
	if upstreamModel != "" {
		upstreamModel = normalizeOpenAIModelForUpstream(account, account.GetMappedModel(upstreamModel))
	}
	if upstreamModel != "" && upstreamModel != strings.TrimSpace(requestedModel) {
		body = ReplaceModelInBody(body, upstreamModel)
	}

	token, _, err := s.GetRequestCredential(ctx, c, account)
	if err != nil {
		return nil, nil, err
	}
	req, err := s.buildOpenAICodexDirectRequest(ctx, c, account, body, token, targetURL)
	if err != nil {
		return nil, nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, upstreamPath)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	startedAt := time.Now()
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, nil, fmt.Errorf("read Codex direct response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		responseBody = s.redactAgentIdentitySensitiveBody(ctx, account, responseBody)
		upstreamMessage := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(responseBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMessage, responseBody) ||
			isOpenAICodexDirectEndpointUnsupported(resp.StatusCode) {
			resp.Body = io.NopCloser(bytes.NewReader(responseBody))
			shouldDisable := false
			if shouldApplyOpenAICodexDirectAccountErrorSideEffects(resp.StatusCode) {
				shouldDisable = s.handleFailoverSideEffects(ctx, resp, account, responseBody, upstreamModel)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(req.URL.String()),
				Kind:               "failover",
				Message:            upstreamMessage,
			})
			return nil, nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           responseBody,
				ResponseHeaders:        resp.Header.Clone(),
				RetryableOnSameAccount: !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		setOpsUpstreamError(c, resp.StatusCode, upstreamMessage, "")
	}
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, responseBody)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, responseBody, nil
	}

	usage, _ := extractOpenAIUsageFromJSONBytes(responseBody)
	return &OpenAIForwardResult{
		RequestID:        strings.TrimSpace(resp.Header.Get("x-request-id")),
		Usage:            usage,
		Model:            strings.TrimSpace(requestedModel),
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: upstreamPath,
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(startedAt),
	}, responseBody, nil
}

func (s *OpenAIGatewayService) buildOpenAICodexDirectRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
	targetURL string,
) (*http.Request, error) {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse Codex direct URL: %w", err)
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		query := parsedURL.Query()
		for key, values := range c.Request.URL.Query() {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		parsedURL.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	if c != nil && c.Request != nil {
		allowTimeoutHeaders := s.isOpenAIPassthroughTimeoutHeadersAllowed()
		for key, values := range c.Request.Header {
			lower := strings.ToLower(strings.TrimSpace(key))
			if !isOpenAIPassthroughAllowedRequestHeader(lower, allowTimeoutHeaders) {
				continue
			}
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	authHeaders, err := s.buildOpenAIAuthenticationHeaders(ctx, account, token)
	if err != nil {
		return nil, fmt.Errorf("build openai authentication headers: %w", err)
	}
	for key, values := range authHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	req.Host = "chatgpt.com"
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
		return nil, fmt.Errorf("resolve chatgpt account headers: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	s.overrideBrowserUserAgent(ctx, account, req)
	enforceCodexIdentityHeaders(req.Header)
	applyOpenAICodexDirectIdentityHeaders(c, account, req.Header)
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

func applyOpenAICodexDirectIdentityHeaders(c *gin.Context, account *Account, headers http.Header) {
	if c == nil || headers == nil {
		return
	}
	apiKeyID := getAPIKeyIDFromContext(c)
	identity := resolveOpenAICodexUpstreamIdentity(c, account, nil, apiKeyID, false)
	applyOpenAICodexUpstreamIdentityHeaders(headers, identity)

	if rawConversationID := strings.TrimSpace(headers.Get("conversation_id")); rawConversationID != "" {
		conversationID := isolateOpenAISessionID(apiKeyID, rawConversationID)
		if identity.SessionID != "" {
			conversationID = identity.SessionID
		}
		headers.Set("conversation_id", conversationID)
	}
}

func isOpenAICodexDirectEndpointUnsupported(statusCode int) bool {
	return statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed
}

func shouldApplyOpenAICodexDirectAccountErrorSideEffects(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
		return false
	default:
		return true
	}
}
