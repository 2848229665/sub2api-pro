package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"golang.org/x/net/http2"
	"golang.org/x/sync/singleflight"
)

// chatgptCodexModelsURL is the ChatGPT Codex models manifest endpoint.
// Package-level variable so tests can point it at a stub server.
var chatgptCodexModelsURL = "https://chatgpt.com/backend-api/codex/models"

const (
	codexModelsManifestCacheBodyLimit = 1 << 20
	// codexModelsManifestCacheMaxEntries 上限按「账号数 × 客户端版本数 × 代理形态」
	// 估算：缓存同时覆盖 OAuth 与 API Key 账号，且缓存键含 Authorization 与
	// Version 头，不同客户端版本各自占一条；64 条在大规模部署下会被淘汰导致
	// 额外上游请求。单条清单通常几十 KB，512 条最坏内存占用在几十 MB 量级。
	codexModelsManifestCacheMaxEntries = 512
	// 三段时效：≤TTL 为新鲜（直接返回缓存，零上游请求）；TTL 到 StaleTTL 之间
	// 乐观返回旧值并后台单飞刷新（携带上游 ETag，304 续期）；超过 StaleTTL 丢弃
	// 缓存同步等待刷新。TTL 取 1 分钟：manifest 变化低频，1 分钟内同账号重复
	// 请求完全吸收；StaleTTL 取 5 分钟，控制旧内容最长可见时间在分钟级。
	codexModelsManifestCacheTTL       = 60 * time.Second
	codexModelsManifestCacheStaleTTL  = 5 * time.Minute
	codexModelsManifestRequestTimeout = 15 * time.Second
	codexAutoModelPrefix              = "codex-auto-"
)

// FilterCodexModelIDsForGroup removes dedicated media-generation models,
// wildcard mapping keys, and Codex automatic modes from a client catalog.
// Automatic modes are retained only when the group's enabled custom model list
// explicitly selects the exact slug; account model mappings describe routing
// and are not feature opt-ins. Wildcard keys such as "foo-*" are routing
// patterns, not concrete Codex models.
func FilterCodexModelIDsForGroup(modelIDs []string, group *Group) []string {
	explicitlyEnabled := make(map[string]struct{})
	if group != nil && group.CustomModelsListEnabled() {
		for _, modelID := range group.ModelsListConfig.Models {
			modelID = strings.TrimSpace(modelID)
			if strings.HasPrefix(modelID, codexAutoModelPrefix) {
				explicitlyEnabled[modelID] = struct{}{}
			}
		}
	}

	filtered := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if isCodexDedicatedMediaModel(modelID) {
			continue
		}
		if strings.Contains(modelID, "*") {
			continue
		}
		if strings.HasPrefix(modelID, codexAutoModelPrefix) {
			if _, ok := explicitlyEnabled[modelID]; !ok {
				continue
			}
		}
		filtered = append(filtered, modelID)
	}
	return filtered
}

func isCodexDedicatedMediaModel(modelID string) bool {
	canonical := codexProviderQualifiedModelID(modelID)
	return IsGPTImageGenerationModel(canonical) ||
		isImageGenerationModel(canonical) ||
		xai.IsGrokImagineModel(modelID)
}

func codexProviderQualifiedModelID(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if slash := strings.LastIndexByte(modelID, '/'); slash >= 0 {
		modelID = strings.TrimSpace(modelID[slash+1:])
	}
	return strings.TrimPrefix(modelID, "models/")
}

// CodexModelsManifest carries the client representation plus caching metadata.
type CodexModelsManifest struct {
	Body                         []byte
	ETag                         string
	upstreamETag                 string
	upstreamSourceBody           []byte
	convertedFromOpenAIModelList bool
	NotModified                  bool
	UpstreamEndpoint             string
	ResponseHeaders              http.Header
}

type configuredCodexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type configuredCodexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

type configuredCodexServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type configuredCodexModelMessages struct {
	InstructionsTemplate  string `json:"instructions_template"`
	InstructionsVariables any    `json:"instructions_variables"`
	Approvals             any    `json:"approvals"`
	CollaborationModes    any    `json:"collaboration_modes"`
	AutoReview            any    `json:"auto_review"`
	Permissions           any    `json:"permissions"`
	MultiAgent            any    `json:"multi_agent"`
	TokenBudget           any    `json:"token_budget"`
	GuardianV2            any    `json:"guardian_v2"`
}

// configuredCodexModelDescriptor is the minimum complete ModelInfo contract
// understood by current Codex clients. Several nullable fields are intentionally
// emitted: unlike ordinary OpenAI /v1/models entries, the Codex manifest parser
// requires them to be present.
type configuredCodexModelDescriptor struct {
	Slug                              string                          `json:"slug"`
	DisplayName                       string                          `json:"display_name"`
	Description                       string                          `json:"description"`
	DefaultReasoningLevel             *string                         `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels          []configuredCodexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                         string                          `json:"shell_type"`
	Visibility                        string                          `json:"visibility"`
	SupportedInAPI                    bool                            `json:"supported_in_api"`
	Priority                          int                             `json:"priority"`
	AdditionalSpeedTiers              []string                        `json:"additional_speed_tiers"`
	ServiceTiers                      []configuredCodexServiceTier    `json:"service_tiers"`
	DefaultServiceTier                any                             `json:"default_service_tier"`
	AvailabilityNUX                   any                             `json:"availability_nux"`
	Upgrade                           any                             `json:"upgrade"`
	ModelMessages                     configuredCodexModelMessages    `json:"model_messages"`
	IncludeSkillsUsageInstructions    bool                            `json:"include_skills_usage_instructions"`
	IncludePluginUsageInstructions    bool                            `json:"include_plugin_usage_instructions"`
	IncludeAppsUsageInstructions      bool                            `json:"include_apps_usage_instructions"`
	SupportsReasoningSummaryParameter bool                            `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary           string                          `json:"default_reasoning_summary"`
	SupportVerbosity                  bool                            `json:"support_verbosity"`
	DefaultVerbosity                  *string                         `json:"default_verbosity"`
	ApplyPatchToolType                *string                         `json:"apply_patch_tool_type"`
	WebSearchToolType                 string                          `json:"web_search_tool_type"`
	TruncationPolicy                  configuredCodexTruncationPolicy `json:"truncation_policy"`
	SupportsImageDetailOriginal       bool                            `json:"supports_image_detail_original"`
	SupportsParallelToolCalls         bool                            `json:"supports_parallel_tool_calls"`
	ContextWindow                     int64                           `json:"context_window"`
	MaxContextWindow                  int64                           `json:"max_context_window"`
	AutoCompactTokenLimit             any                             `json:"auto_compact_token_limit"`
	CompHash                          any                             `json:"comp_hash"`
	EffectiveContextWindowPercent     int64                           `json:"effective_context_window_percent"`
	ExperimentalSupportedTools        []string                        `json:"experimental_supported_tools"`
	InputModalities                   []string                        `json:"input_modalities"`
	SupportsSearchTool                bool                            `json:"supports_search_tool"`
	UseResponsesLite                  bool                            `json:"use_responses_lite"`
	NodeREPLAutoReviewRequired        bool                            `json:"node_repl_auto_review_required"`
	NodeREPLDisabled                  bool                            `json:"node_repl_disabled"`
	AutoReviewModelOverride           any                             `json:"auto_review_model_override"`
	ModelSpecialty                    any                             `json:"model_specialty"`
	ToolMode                          any                             `json:"tool_mode"`
	MultiAgentVersion                 any                             `json:"multi_agent_version"`
}

type codexModelMetadataOverride struct {
	UpstreamModelMetadata
	reasoningConflict       bool
	inputModalitiesConflict bool
}

// BuildDeepSeekCodexModelsManifest preserves the historical entry point for
// callers that still use the provider-specific function name.
func BuildDeepSeekCodexModelsManifest(modelIDs []string) ([]byte, error) {
	return BuildCodexModelsManifest(modelIDs)
}

type codexModelsManifestUpstreamError struct {
	err        error
	retryable  bool
	statusCode int
	headers    http.Header
	body       []byte
}

func (e *codexModelsManifestUpstreamError) Error() string { return e.err.Error() }

func (e *codexModelsManifestUpstreamError) Unwrap() error { return e.err }

// IsRetryableCodexModelsManifestError reports whether another selected account
// may succeed without changing the request. API key upstream 404/405 responses
// mean that the selected account does not expose a model-discovery endpoint, so
// another account may still serve the manifest. Other upstream 4xx responses,
// except 429 and ChatGPT-backend 401, are intentionally not retried. A manifest
// 401 from the ChatGPT Codex backend reflects the selected OAuth account's
// upstream token rather than the client request (the client's own API key was
// already validated locally). Custom API key upstreams keep the no-failover 401
// behavior because their /models auth semantics are not authoritative for the
// account.
func IsRetryableCodexModelsManifestError(err error) bool {
	var upstreamErr *codexModelsManifestUpstreamError
	return errors.As(err, &upstreamErr) && upstreamErr.retryable
}

func isRetryableCodexModelsManifestStatus(statusCode int, useAPIKeyUpstream bool) bool {
	return (useAPIKeyUpstream &&
		(statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed)) ||
		(statusCode == http.StatusUnauthorized && !useAPIKeyUpstream) ||
		statusCode == http.StatusTooManyRequests ||
		(statusCode >= http.StatusInternalServerError && statusCode < 600)
}

func isRetryableCodexModelsManifestTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var goAwayErr http2.GoAwayError
	if errors.As(err, &goAwayErr) {
		return true
	}
	var streamErr http2.StreamError
	if errors.As(err, &streamErr) {
		return true
	}
	var connectionErr http2.ConnectionError
	if errors.As(err, &connectionErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// net/http uses unexported HTTP/2 error types, so typed matching is not
	// possible for errors produced by the standard library transport.
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "http2:") &&
		(strings.Contains(message, "goaway") ||
			strings.Contains(message, "refused_stream") ||
			strings.Contains(message, "frame too large")) {
		return true
	}
	if strings.Contains(message, "stream error: stream id ") {
		return true
	}
	for _, code := range []http2.ErrCode{
		http2.ErrCodeNo,
		http2.ErrCodeProtocol,
		http2.ErrCodeInternal,
		http2.ErrCodeFlowControl,
		http2.ErrCodeSettingsTimeout,
		http2.ErrCodeStreamClosed,
		http2.ErrCodeFrameSize,
		http2.ErrCodeRefusedStream,
		http2.ErrCodeCancel,
		http2.ErrCodeCompression,
		http2.ErrCodeConnect,
		http2.ErrCodeEnhanceYourCalm,
		http2.ErrCodeInadequateSecurity,
		http2.ErrCodeHTTP11Required,
	} {
		if strings.Contains(message, "connection error: "+strings.ToLower(code.String())) {
			return true
		}
	}
	return false
}

type codexModelsManifestRequest struct {
	url                 string
	headers             http.Header
	proxyURL            string
	accountID           int64
	credentialAccountID int64
	credentialAccount   *Account
	accountConcurrency  int
	useAPIKeyUpstream   bool
}

type codexModelsManifestCacheEntry struct {
	manifest   *CodexModelsManifest
	order      uint64
	expiresAt  time.Time
	staleUntil time.Time
}

type codexModelsManifestCacheState uint8

const (
	codexModelsManifestCacheMiss codexModelsManifestCacheState = iota
	codexModelsManifestCacheFresh
	codexModelsManifestCacheStale
)

type codexModelsManifestCache struct {
	mu        sync.Mutex
	entries   map[string]codexModelsManifestCacheEntry
	nextOrder uint64
	refresh   singleflight.Group
}

func (c *codexModelsManifestCache) get(key string, now time.Time) (*CodexModelsManifest, codexModelsManifestCacheState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, codexModelsManifestCacheMiss
	}
	if !now.Before(entry.staleUntil) {
		delete(c.entries, key)
		return nil, codexModelsManifestCacheMiss
	}
	if now.Before(entry.expiresAt) {
		return entry.manifest, codexModelsManifestCacheFresh
	}
	return entry.manifest, codexModelsManifestCacheStale
}

func (c *codexModelsManifestCache) set(key string, manifest *CodexModelsManifest, now time.Time) {
	if manifest == nil || len(manifest.Body) > codexModelsManifestCacheBodyLimit {
		return
	}
	remainingBodyBudget := codexModelsManifestCacheBodyLimit - len(manifest.Body)
	if len(manifest.upstreamSourceBody) > remainingBodyBudget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]codexModelsManifestCacheEntry)
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= codexModelsManifestCacheMaxEntries {
		oldestKey := ""
		var oldestOrder uint64
		for candidateKey, entry := range c.entries {
			if !now.Before(entry.staleUntil) {
				delete(c.entries, candidateKey)
				continue
			}
			if oldestKey == "" || entry.order < oldestOrder {
				oldestKey = candidateKey
				oldestOrder = entry.order
			}
		}
		if len(c.entries) >= codexModelsManifestCacheMaxEntries && oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	c.nextOrder++
	c.entries[key] = codexModelsManifestCacheEntry{
		manifest:   manifest,
		order:      c.nextOrder,
		expiresAt:  now.Add(codexModelsManifestCacheTTL),
		staleUntil: now.Add(codexModelsManifestCacheStaleTTL),
	}
}

// FetchCodexModelsManifest fetches the live Codex models manifest from either
// the ChatGPT backend for OAuth accounts or a custom upstream for API key accounts.
//
// After validating the stable top-level envelope, OAuth response bodies are
// passed through verbatim. Custom API key manifests receive only the narrowly
// scoped compatibility adjustments required by custom-provider Codex clients.
func (s *OpenAIGatewayService) FetchCodexModelsManifest(ctx context.Context, account *Account, clientVersion, ifNoneMatch string) (*CodexModelsManifest, error) {
	if account == nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_ACCOUNT_REQUIRED", "account is required")
	}
	credAccount, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_CREDENTIALS_FAILED", "resolve credential account: %v", err)
	}

	clientVersion = strings.TrimSpace(clientVersion)
	if clientVersion == "" {
		clientVersion = CodexCanonicalClientVersion()
	}

	requestEndpoint := chatgptCodexModelsURL
	authToken := ""
	useAPIKeyUpstream := false
	appendModelsPath := false
	switch {
	case credAccount.IsOpenAIOAuth():
		authToken = strings.TrimSpace(credAccount.GetOpenAIAccessToken())
		if authToken == "" && !credAccount.IsOpenAIAgentIdentity() {
			return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_TOKEN_MISSING", "account has no Codex backend access token")
		}
	case credAccount.IsOpenAIApiKey():
		baseURL := strings.TrimSpace(credAccount.GetOpenAIBaseURL())
		authToken = strings.TrimSpace(credAccount.GetOpenAIApiKey())
		if authToken == "" {
			return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_API_KEY_MISSING", "account has no API key for the Codex models upstream")
		}
		normalizedBaseURL, validateErr := s.validateUpstreamBaseURL(baseURL)
		if validateErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_API_KEY_UPSTREAM_INVALID", "invalid Codex models upstream base URL: %v", validateErr)
		}
		requestEndpoint = normalizedBaseURL
		useAPIKeyUpstream = true
		appendModelsPath = true
	default:
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_ACCOUNT_TYPE_UNSUPPORTED", "account type %q cannot fetch the Codex models manifest", credAccount.Type)
	}

	requestURL, err := buildCodexModelsManifestURL(requestEndpoint, appendModelsPath, clientVersion)
	if err != nil {
		if useAPIKeyUpstream {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_API_KEY_UPSTREAM_INVALID", "invalid Codex models upstream base URL: %v", err)
		}
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_REQUEST_FAILED", "parse codex models request URL: %v", err)
	}

	headers := make(http.Header)
	if useAPIKeyUpstream {
		headers.Set("Authorization", "Bearer "+authToken)
		credAccount.ApplyHeaderOverrides(headers)
	} else {
		authHeaders, authErr := s.buildOpenAIAuthenticationHeaders(ctx, credAccount, authToken)
		if authErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_AUTH_FAILED", "build Codex models authentication: %v", authErr)
		}
		for key, values := range authHeaders {
			for _, value := range values {
				headers.Add(key, value)
			}
		}
		setOpenAIChatGPTAccountHeaders(headers, credAccount)
	}
	headers.Set("Accept", "application/json")
	overrideUA := ""
	if !useAPIKeyUpstream {
		overrideUA = credAccount.GetOpenAIUserAgent()
	}
	identity := resolveCodexOutboundIdentity(overrideUA)
	headers.Set("Originator", identity.originator)
	headers.Set("User-Agent", identity.userAgent)
	// Version 头优先与 client_version 查询参数同源：客户端自报版本合法且不低于上游
	// 门槛时原样使用；否则回退规范版本，避免陈旧 version 触发上游 404（issue #3901）。
	// client_version 查询参数本身始终按客户端原值透传（内容协商语义，契约见
	// TestFetchCodexModelsManifestPassthrough）。
	headerVersion := NormalizeCodexClientVersion(clientVersion)
	if headerVersion == "" || CompareVersions(headerVersion, codexUpstreamMinVersion) < 0 {
		headerVersion = identity.version
	}
	headers.Set("Version", headerVersion)
	account.ApplyHeaderOverrides(headers)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	request := codexModelsManifestRequest{
		url:                 requestURL.String(),
		headers:             headers,
		proxyURL:            proxyURL,
		accountID:           account.ID,
		credentialAccountID: credAccount.ID,
		credentialAccount:   credAccount,
		accountConcurrency:  account.Concurrency,
		useAPIKeyUpstream:   useAPIKeyUpstream,
	}
	if useAPIKeyUpstream {
		return s.fetchCachedCodexModelsManifest(ctx, request, s.fetchCodexModelsManifestUpstreamForRequest(request), ifNoneMatch)
	}
	// OAuth 账号同样经过账号级缓存；闭包保留 agent identity 任务恢复逻辑，
	// 错误时仍交给 handleCodexModelsManifestAccountAuthError 处理账号状态。
	oauthFetch := func(fetchCtx context.Context, ifNoneMatch string) (*CodexModelsManifest, error) {
		manifest, fetchErr := s.fetchCodexModelsManifestUpstream(fetchCtx, request, ifNoneMatch)
		if !credAccount.IsOpenAIAgentIdentity() || !isAgentIdentityTaskInvalidCodexModelsError(fetchErr) {
			s.handleCodexModelsManifestAccountAuthError(fetchCtx, account, credAccount, fetchErr)
			return manifest, fetchErr
		}
		expectedTaskID := strings.TrimSpace(credAccount.GetCredential("task_id"))
		if recoverErr := s.recoverAgentIdentityTask(fetchCtx, credAccount, expectedTaskID); recoverErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_AUTH_FAILED", "agent identity task recovery failed: %v", recoverErr)
		}
		authHeaders, authErr := s.buildOpenAIAuthenticationHeaders(fetchCtx, credAccount, "")
		if authErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_AUTH_FAILED", "build Codex models authentication after task recovery: %v", authErr)
		}
		request.headers.Del("Authorization")
		request.headers.Del("ChatGPT-Account-ID")
		for key, values := range authHeaders {
			for _, value := range values {
				request.headers.Add(key, value)
			}
		}
		setOpenAIChatGPTAccountHeaders(request.headers, credAccount)
		return s.fetchCodexModelsManifestUpstream(fetchCtx, request, ifNoneMatch)
	}
	return s.fetchCachedCodexModelsManifest(ctx, request, oauthFetch, ifNoneMatch)
}

func isAgentIdentityTaskInvalidCodexModelsError(err error) bool {
	var upstreamErr *codexModelsManifestUpstreamError
	return errors.As(err, &upstreamErr) &&
		isAgentIdentityTaskInvalidHTTPResponse(upstreamErr.statusCode, upstreamErr.body)
}

// handleCodexModelsManifestAccountAuthError feeds manifest 401s from the
// ChatGPT Codex backend into the shared upstream-error state machinery
// (token cache invalidation, temp-unschedulable cooldown, or permanent
// disable for token_revoked/token_invalidated). Without this, an account
// whose OAuth token was revoked upstream stays active and schedulable and
// keeps being selected for every subsequent /models request (#4544).
//
// Scope is deliberately limited to plain OAuth accounts: the manifest
// endpoint authenticates with the same token as /responses forwarding, so a
// 401 is authoritative for the account. Agent Identity accounts are excluded
// because their 401s can be task-scoped and have a dedicated recovery flow,
// and API key manifests come from custom upstreams whose /models auth may
// diverge from their chat endpoints.
func (s *OpenAIGatewayService) handleCodexModelsManifestAccountAuthError(ctx context.Context, account, credAccount *Account, err error) {
	if s == nil || account == nil || err == nil {
		return
	}
	if credAccount == nil || !credAccount.IsOpenAIOAuth() || credAccount.IsOpenAIAgentIdentity() {
		return
	}
	var upstreamErr *codexModelsManifestUpstreamError
	if !errors.As(err, &upstreamErr) || upstreamErr.statusCode != http.StatusUnauthorized {
		return
	}
	headers := upstreamErr.headers
	if headers == nil {
		headers = http.Header{}
	}
	s.handleOpenAIAccountUpstreamError(ctx, account, upstreamErr.statusCode, headers, upstreamErr.body)
}

func (s *OpenAIGatewayService) fetchCachedCodexModelsManifest(ctx context.Context, request codexModelsManifestRequest, fetch func(ctx context.Context, ifNoneMatch string) (*CodexModelsManifest, error), ifNoneMatch string) (*CodexModelsManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cacheKey := buildCodexModelsManifestCacheKey(request)
	manifest, state := s.codexModelsManifestCache.get(cacheKey, time.Now())
	if state == codexModelsManifestCacheFresh {
		return codexModelsManifestForClient(manifest, ifNoneMatch), nil
	}
	resultCh := s.refreshCachedCodexModelsManifest(cacheKey, request, fetch)
	if state == codexModelsManifestCacheStale {
		return codexModelsManifestForClient(manifest, ifNoneMatch), nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		manifest, ok := result.Val.(*CodexModelsManifest)
		if !ok || manifest == nil {
			return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_REQUEST_FAILED", "invalid shared Codex models manifest result")
		}
		return codexModelsManifestForClient(manifest, ifNoneMatch), nil
	}
}

func (s *OpenAIGatewayService) refreshCachedCodexModelsManifest(cacheKey string, request codexModelsManifestRequest, fetch func(ctx context.Context, ifNoneMatch string) (*CodexModelsManifest, error)) <-chan singleflight.Result {
	return s.codexModelsManifestCache.refresh.DoChan(cacheKey, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), codexModelsManifestRequestTimeout)
		defer cancel()
		cached, _ := s.codexModelsManifestCache.get(cacheKey, time.Now())
		ifNoneMatch := ""
		if cached != nil {
			ifNoneMatch = cached.upstreamETag
		}
		manifest, err := fetch(ctx, ifNoneMatch)
		if err != nil {
			return nil, err
		}
		if manifest.NotModified && cached != nil {
			s.codexModelsManifestCache.set(cacheKey, cached, time.Now())
			return cached, nil
		}
		if !manifest.NotModified {
			s.codexModelsManifestCache.set(cacheKey, manifest, time.Now())
		}
		return manifest, nil
	})
}

func (s *OpenAIGatewayService) fetchCodexModelsManifestUpstreamForRequest(request codexModelsManifestRequest) func(ctx context.Context, ifNoneMatch string) (*CodexModelsManifest, error) {
	return func(ctx context.Context, ifNoneMatch string) (*CodexModelsManifest, error) {
		return s.fetchCodexModelsManifestUpstream(ctx, request, ifNoneMatch)
	}
}

func (s *OpenAIGatewayService) fetchCodexModelsManifestUpstream(ctx context.Context, request codexModelsManifestRequest, ifNoneMatch string) (*CodexModelsManifest, error) {
	reqCtx, cancel := context.WithTimeout(ctx, codexModelsManifestRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, request.url, nil)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_REQUEST_FAILED", "create codex models request: %v", err)
	}
	req.Header = request.headers.Clone()
	if ifNoneMatch = strings.TrimSpace(ifNoneMatch); ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	var resp *http.Response
	if request.useAPIKeyUpstream {
		if s.httpUpstream == nil {
			return nil, infraerrors.New(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_UPSTREAM_NOT_CONFIGURED", "Codex models upstream HTTP client is not configured")
		}
		req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
		resp, err = s.httpUpstream.Do(req, request.proxyURL, request.accountID, request.accountConcurrency)
	} else {
		handled := false
		if s.pluginManager != nil {
			resp, handled, err = s.pluginManager.RoundTripOpenAIOAuth(reqCtx, req, request.proxyURL, request.credentialAccount)
		}
		if !handled {
			client, clientErr := httpclient.GetClient(httpclient.Options{
				ProxyURL:              request.proxyURL,
				Timeout:               codexModelsManifestRequestTimeout,
				ResponseHeaderTimeout: 10 * time.Second,
			})
			if clientErr != nil {
				return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_CODEX_MODELS_PROXY_INVALID", "invalid proxy configuration: %v", clientErr)
			}
			resp, err = client.Do(req)
		}
	}
	if err != nil {
		return nil, &codexModelsManifestUpstreamError{
			err:       infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "codex models manifest request failed: %v", err),
			retryable: isRetryableCodexModelsManifestTransportError(err),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return &CodexModelsManifest{
			ETag:             resp.Header.Get("ETag"),
			NotModified:      true,
			UpstreamEndpoint: req.URL.Path,
			ResponseHeaders:  resp.Header.Clone(),
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		body = s.redactAgentIdentitySensitiveBody(reqCtx, request.credentialAccount, body)
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, &codexModelsManifestUpstreamError{
			err:        infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "codex models manifest upstream error %d: %s", resp.StatusCode, message),
			statusCode: resp.StatusCode,
			headers:    resp.Header.Clone(),
			body:       body,
			retryable:  isRetryableCodexModelsManifestStatus(resp.StatusCode, request.useAPIKeyUpstream),
		}
	}

	bodyLimit := resolveModelsListReadLimit(s.cfg)
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return nil, &codexModelsManifestUpstreamError{
			err:       infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "read codex models manifest response: %v", err),
			retryable: isRetryableCodexModelsManifestTransportError(err),
		}
	}
	if int64(len(body)) > bodyLimit {
		return nil, &codexModelsManifestUpstreamError{
			err:       infraerrors.Newf(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "codex models manifest response exceeds %d bytes", bodyLimit),
			retryable: true,
		}
	}
	upstreamBody := body
	if request.useAPIKeyUpstream {
		convertedBody := convertOpenAIModelListToCodexManifestForAccount(body, request.credentialAccount)
		body = convertedBody
	}
	if err := validateCodexModelsManifestEnvelope(body); err != nil {
		return nil, &codexModelsManifestUpstreamError{
			err: infraerrors.Newf(
				http.StatusBadGateway,
				"OPENAI_CODEX_MODELS_UPSTREAM_INVALID_MANIFEST",
				"codex models manifest upstream returned an invalid envelope: %v",
				err,
			),
			retryable: true,
		}
	}
	if request.useAPIKeyUpstream {
		body, err = completeAPIKeyCodexModelsManifestMetadata(
			body,
			false,
			request.credentialAccount,
		)
		if err != nil {
			return nil, &codexModelsManifestUpstreamError{
				err: infraerrors.Newf(
					http.StatusBadGateway,
					"OPENAI_CODEX_MODELS_UPSTREAM_INVALID_MANIFEST",
					"codex models manifest upstream metadata could not be completed: %v",
					err,
				),
				retryable: true,
			}
		}
		body, err = adjustAPIKeyCodexModelsManifest(body, request.credentialAccount)
		if err != nil {
			return nil, &codexModelsManifestUpstreamError{
				err: infraerrors.Newf(
					http.StatusBadGateway,
					"OPENAI_CODEX_MODELS_UPSTREAM_INVALID_MANIFEST",
					"codex models manifest upstream could not be adjusted: %v",
					err,
				),
				retryable: true,
			}
		}
	}
	etag := resp.Header.Get("ETag")
	manifest := &CodexModelsManifest{
		Body:                         body,
		ETag:                         etag,
		UpstreamEndpoint:             req.URL.Path,
		ResponseHeaders:              resp.Header.Clone(),
		upstreamETag:                 etag,
		upstreamSourceBody:           append([]byte(nil), upstreamBody...),
		convertedFromOpenAIModelList: request.useAPIKeyUpstream && !bytes.Equal(body, upstreamBody),
	}
	if request.useAPIKeyUpstream && !bytes.Equal(body, upstreamBody) {
		manifest.ETag = codexModelsManifestBodyETag(body)
	}
	return manifest, nil
}

func codexModelsManifestBodyETag(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf(`"%x"`, sum)
}

// CodexModelsManifestETag returns the strong ETag for a generated client
// catalog. It is based on the final JSON body after local filtering.
func CodexModelsManifestETag(body []byte) string {
	return codexModelsManifestBodyETag(body)
}

var apiKeyCodexModelsWithoutResponsesLite = map[string]struct{}{
	"gpt-6-astra":   {},
	"gpt-5.6-sol":   {},
	"gpt-5.6-terra": {},
	"gpt-5.6-luna":  {},
}

// adjustAPIKeyCodexModelsManifest prevents Codex from selecting Responses
// Lite for custom API key providers. Those clients do not install web.run in
// Lite mode, so the affected model manifests must advertise the full Responses
// path. Return the original body when no targeted true value is present.
func adjustAPIKeyCodexModelsManifest(body []byte, account *Account) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, fmt.Errorf("decode top-level models array: %w", err)
	}

	changed := false
	for i, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil || model == nil {
			continue
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			continue
		}
		target := slug
		if account != nil {
			target = account.GetMappedModel(slug)
		}
		if isOpenAIGPT6AstraModel(target) {
			target = "gpt-6-astra"
		}
		if _, targeted := apiKeyCodexModelsWithoutResponsesLite[target]; !targeted {
			continue
		}
		var useResponsesLite bool
		if err := json.Unmarshal(model["use_responses_lite"], &useResponsesLite); err != nil || !useResponsesLite {
			continue
		}
		model["use_responses_lite"] = json.RawMessage("false")
		adjusted, err := json.Marshal(model)
		if err != nil {
			return nil, fmt.Errorf("encode model %q: %w", slug, err)
		}
		models[i] = adjusted
		changed = true
	}
	if !changed {
		return body, nil
	}

	adjustedModels, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("encode top-level models array: %w", err)
	}
	envelope["models"] = adjustedModels
	adjusted, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode JSON object: %w", err)
	}
	return adjusted, nil
}

// convertOpenAIModelListToCodexManifest rewrites a standard OpenAI
// GET /v1/models response ({"object":"list","data":[{"id":...},...]}) into the
// same complete Codex manifest used by locally generated custom-provider
// catalogs. Bodies that already carry a top-level models field, are not the
// standard list shape, or yield no usable model IDs are returned unchanged so
// envelope validation reports the original payload.
func convertOpenAIModelListToCodexManifest(body []byte) []byte {
	return convertOpenAIModelListToCodexManifestForAccount(body, nil)
}

func convertOpenAIModelListToCodexManifestForAccount(body []byte, account *Account) []byte {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return body
	}
	if _, ok := envelope["models"]; ok {
		return body
	}
	data, ok := envelope["data"]
	if !ok {
		return body
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return body
	}
	modelIDs := make([]string, 0, len(entries))
	modelMetadata := make(map[string]codexModelMetadataOverride, len(entries))
	metadataModels := make(map[string]string, len(entries))
	for _, entry := range entries {
		var id string
		if err := json.Unmarshal(entry["id"], &id); err != nil {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		modelIDs = append(modelIDs, id)
		capabilityModel := id
		if account != nil {
			capabilityModel = account.GetMappedModel(id)
		}
		metadataModels[id] = capabilityModel
		capabilities := accountCodexToolCapabilities(account, capabilityModel)
		applyCodexToolCapabilities(capabilities, entry, true)
		modelMetadata[id] = codexModelMetadataOverride{UpstreamModelMetadata: UpstreamModelMetadata{
			CodexToolCapabilities: capabilities,
		}}
	}
	if len(modelIDs) == 0 {
		return body
	}
	imageInputModels := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		if accountCodexModelSupportsImageInput(account, modelID) {
			imageInputModels[modelID] = true
		}
	}
	converted, err := buildCodexModelsManifest(modelIDs, imageInputModels, metadataModels, modelMetadata)
	if err != nil {
		return body
	}
	return converted
}

// completeAPIKeyCodexModelsManifestMetadata fills fields omitted by standard
// OpenAI-compatible /models endpoints. Existing provider metadata always wins;
// only absent or null values are synthesized.
// CompleteAPIKeyCodexModelsManifestForClient fills the complete ModelInfo
// contract immediately before a group-specific API key manifest is returned.
// The shared upstream cache remains independent from local group policy.
func (s *OpenAIGatewayService) CompleteAPIKeyCodexModelsManifestForClient(manifest *CodexModelsManifest, account *Account) error {
	if manifest == nil || account == nil || !account.IsOpenAIApiKey() || manifest.NotModified || len(manifest.Body) == 0 {
		return nil
	}
	body := manifest.Body
	if len(manifest.upstreamSourceBody) > 0 {
		body = append([]byte(nil), manifest.upstreamSourceBody...)
		if manifest.convertedFromOpenAIModelList {
			body = convertOpenAIModelListToCodexManifestForAccount(body, account)
		}
	}
	var err error
	body, err = applySyncedAPIKeyCodexModelMetadata(body, account, manifest.convertedFromOpenAIModelList)
	if err != nil {
		return err
	}
	body, err = completeAPIKeyCodexModelsManifestMetadata(
		body,
		true,
		account,
	)
	if err != nil {
		return err
	}
	body, err = adjustAPIKeyCodexModelsManifest(body, account)
	if err != nil {
		return err
	}
	manifest.Body = body
	manifest.ETag = codexModelsManifestBodyETag(manifest.Body)
	return nil
}

func applySyncedAPIKeyCodexModelMetadata(body []byte, account *Account, overwriteLocalDefaults bool) ([]byte, error) {
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil || len(snapshot.Models) == 0 {
		return body, nil
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, fmt.Errorf("decode top-level models array: %w", err)
	}

	changed := false
	for i, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil || model == nil {
			continue
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			continue
		}
		slug = strings.TrimSpace(slug)
		lookupModel := account.GetMappedModel(slug)
		metadata, ok := snapshot.Models[lookupModel]
		if !ok {
			continue
		}
		if lookupModel != slug {
			metadata.DisplayName = ""
			metadata.Description = ""
		}

		descriptor := newConfiguredCodexModelDescriptor(slug)
		applyUpstreamModelMetadataToCodexDescriptor(
			&descriptor,
			codexModelMetadataOverride{UpstreamModelMetadata: metadata},
		)
		descriptorBody, err := json.Marshal(descriptor)
		if err != nil {
			return nil, fmt.Errorf("encode synced model %q: %w", slug, err)
		}
		var syncedFields map[string]json.RawMessage
		if err := json.Unmarshal(descriptorBody, &syncedFields); err != nil {
			return nil, fmt.Errorf("decode synced model %q: %w", slug, err)
		}

		fields := make([]string, 0, 7)
		if strings.TrimSpace(metadata.DisplayName) != "" {
			fields = append(fields, "display_name")
		}
		if strings.TrimSpace(metadata.Description) != "" {
			fields = append(fields, "description")
		}
		if metadata.Reasoning != nil {
			fields = append(fields, "default_reasoning_level", "supported_reasoning_levels")
		}
		if len(normalizeCodexInputModalities(metadata.InputModalities)) > 0 {
			fields = append(fields, "input_modalities")
		}
		if metadata.ContextWindow > 0 {
			fields = append(fields, "context_window", "max_context_window")
		}

		// List conversion has already applied live fields over account capabilities.
		modelChanged := applyCodexToolCapabilities(model, metadata.CodexToolCapabilities, false)
		for _, field := range fields {
			value, exists := syncedFields[field]
			if !exists {
				continue
			}
			current, currentExists := model[field]
			current = bytes.TrimSpace(current)
			if !overwriteLocalDefaults && currentExists && len(current) > 0 && !bytes.Equal(current, []byte("null")) {
				continue
			}
			if bytes.Equal(current, bytes.TrimSpace(value)) {
				continue
			}
			model[field] = value
			modelChanged = true
		}
		if !modelChanged {
			continue
		}
		encoded, err := json.Marshal(model)
		if err != nil {
			return nil, fmt.Errorf("encode model %q with synced metadata: %w", slug, err)
		}
		models[i] = encoded
		changed = true
	}
	if !changed {
		return body, nil
	}

	encodedModels, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("encode models with synced metadata: %w", err)
	}
	envelope["models"] = encodedModels
	updated, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode manifest with synced metadata: %w", err)
	}
	return updated, nil
}

func completeAPIKeyCodexModelsManifestMetadata(body []byte, completeAll bool, account *Account) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, fmt.Errorf("decode top-level models array: %w", err)
	}

	officialOpenAI := account != nil && isOfficialOpenAIModelsBaseURL(account.GetOpenAIBaseURL())
	changed := false
	if officialOpenAI {
		filtered := make([]json.RawMessage, 0, len(models))
		for _, rawModel := range models {
			var model struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(rawModel, &model); err != nil || strings.TrimSpace(model.Slug) == "" {
				filtered = append(filtered, rawModel)
				continue
			}
			if !isOfficialOpenAICodexCatalogModel(model.Slug) {
				changed = true
				continue
			}
			filtered = append(filtered, rawModel)
		}
		models = filtered
	}
	for i, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil || model == nil {
			continue
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil {
			continue
		}
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}

		completeDescriptor := completeAll || isDeepSeekCodexModel(slug)
		forceOfficialImage := officialOpenAI && isOpenAICodexImageInputModel(slug)
		if !completeDescriptor && !forceOfficialImage {
			continue
		}

		descriptor := newConfiguredCodexModelDescriptor(slug)
		descriptor.SupportsSearchTool = shouldForwardOpenAIResponsesViaRawChatCompletions(account)
		if accountCodexModelSupportsImageInput(account, slug) {
			descriptor.InputModalities = []string{"text", "image"}
		}
		if forceOfficialImage {
			descriptor.InputModalities = []string{"text", "image"}
			descriptor.SupportsImageDetailOriginal = true
		}
		defaultBody, err := json.Marshal(descriptor)
		if err != nil {
			return nil, fmt.Errorf("encode default model %q: %w", slug, err)
		}
		var defaults map[string]json.RawMessage
		if err := json.Unmarshal(defaultBody, &defaults); err != nil {
			return nil, fmt.Errorf("decode default model %q: %w", slug, err)
		}

		capabilityModel := slug
		if account != nil {
			capabilityModel = account.GetMappedModel(slug)
		}
		capabilities := accountCodexToolCapabilities(account, capabilityModel)
		modelChanged := applyCodexToolCapabilities(model, capabilities, false)
		if completeDescriptor {
			merged, err := mergeMissingCodexModelFields(model, defaults)
			if err != nil {
				return nil, fmt.Errorf("complete model %q: %w", slug, err)
			}
			modelChanged = merged || modelChanged
		}
		if forceOfficialImage {
			modalities, err := json.Marshal([]string{"text", "image"})
			if err != nil {
				return nil, fmt.Errorf("encode input modalities for model %q: %w", slug, err)
			}
			if !bytes.Equal(bytes.TrimSpace(model["input_modalities"]), modalities) {
				model["input_modalities"] = modalities
				modelChanged = true
			}
			imageDetailOriginal := json.RawMessage("true")
			if !bytes.Equal(bytes.TrimSpace(model["supports_image_detail_original"]), imageDetailOriginal) {
				model["supports_image_detail_original"] = imageDetailOriginal
				modelChanged = true
			}
		}
		if !modelChanged {
			continue
		}
		encoded, err := json.Marshal(model)
		if err != nil {
			return nil, fmt.Errorf("encode completed model %q: %w", slug, err)
		}
		models[i] = encoded
		changed = true
	}
	if !changed {
		return body, nil
	}

	encodedModels, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("encode top-level models array: %w", err)
	}
	envelope["models"] = encodedModels
	completed, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode JSON object: %w", err)
	}
	return completed, nil
}

func mergeMissingCodexModelFields(current, defaults map[string]json.RawMessage) (bool, error) {
	changed := false
	for key, defaultValue := range defaults {
		currentValue, exists := current[key]
		if exists && stringSliceContains(codexToolCapabilityFields, key) {
			continue
		}
		if !exists || (bytes.Equal(bytes.TrimSpace(currentValue), []byte("null")) &&
			!bytes.Equal(bytes.TrimSpace(defaultValue), []byte("null"))) {
			current[key] = defaultValue
			changed = true
			continue
		}

		var currentObject map[string]json.RawMessage
		var defaultObject map[string]json.RawMessage
		if err := json.Unmarshal(currentValue, &currentObject); err != nil || currentObject == nil {
			continue
		}
		if err := json.Unmarshal(defaultValue, &defaultObject); err != nil || defaultObject == nil {
			continue
		}
		nestedChanged, err := mergeMissingCodexModelFields(currentObject, defaultObject)
		if err != nil {
			return false, err
		}
		if !nestedChanged {
			continue
		}
		mergedValue, err := json.Marshal(currentObject)
		if err != nil {
			return false, fmt.Errorf("encode field %q: %w", key, err)
		}
		current[key] = mergedValue
		changed = true
	}
	return changed, nil
}

func validateCodexModelsManifestEnvelope(body []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode JSON object: %w", err)
	}
	if envelope == nil {
		return errors.New("expected a JSON object")
	}
	models, ok := envelope["models"]
	if !ok {
		return errors.New("missing top-level models array")
	}
	models = bytes.TrimSpace(models)
	var entries []json.RawMessage
	if len(models) == 0 || models[0] != '[' {
		return errors.New("top-level models field is not an array")
	}
	if err := json.Unmarshal(models, &entries); err != nil {
		return fmt.Errorf("decode top-level models array: %w", err)
	}
	return nil
}

func buildCodexModelsManifestCacheKey(request codexModelsManifestRequest) string {
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "%d\n%d\n%s\n%s\n", request.accountID, request.credentialAccountID, request.proxyURL, request.url)
	headerNames := make([]string, 0, len(request.headers))
	for name := range request.headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		_, _ = fmt.Fprintf(hasher, "%s\n", strings.ToLower(name))
		for _, value := range request.headers[name] {
			_, _ = fmt.Fprintf(hasher, "%s\n", value)
		}
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func cloneCodexModelsManifest(manifest *CodexModelsManifest) *CodexModelsManifest {
	if manifest == nil {
		return nil
	}
	cloned := *manifest
	if manifest.Body != nil {
		cloned.Body = append([]byte(nil), manifest.Body...)
	}
	if manifest.upstreamSourceBody != nil {
		cloned.upstreamSourceBody = append([]byte(nil), manifest.upstreamSourceBody...)
	}
	return &cloned
}

func codexModelsManifestForClient(manifest *CodexModelsManifest, ifNoneMatch string) *CodexModelsManifest {
	if manifest == nil {
		return nil
	}
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		return &CodexModelsManifest{
			ETag:             manifest.ETag,
			NotModified:      true,
			UpstreamEndpoint: manifest.UpstreamEndpoint,
			ResponseHeaders:  manifest.ResponseHeaders.Clone(),
		}
	}
	return cloneCodexModelsManifest(manifest)
}

func codexModelsManifestETagMatches(ifNoneMatch, etag string) bool {
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return false
	}
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		if len(value) >= 2 && strings.EqualFold(value[:2], "W/") {
			value = strings.TrimSpace(value[2:])
		}
		return value
	}
	want := normalize(etag)
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || normalize(candidate) == want {
			return true
		}
	}
	return false
}

// CodexModelsManifestETagMatches applies If-None-Match semantics to a Codex
// catalog ETag, including weak and comma-separated validators.
func CodexModelsManifestETagMatches(ifNoneMatch, etag string) bool {
	return codexModelsManifestETagMatches(ifNoneMatch, etag)
}

func isOfficialOpenAIModelsBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	hostname := strings.TrimSuffix(parsed.Hostname(), ".")
	return strings.EqualFold(hostname, "api.openai.com")
}

func buildCodexModelsManifestURL(endpoint string, appendModelsPath bool, clientVersion string) (*url.URL, error) {
	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if requestURL.Fragment != "" {
		return nil, fmt.Errorf("URL fragments are not supported")
	}

	query := requestURL.Query()
	requestURL.RawQuery = ""
	requestURL.ForceQuery = false
	if appendModelsPath {
		requestURL, err = url.Parse(buildOpenAIModelsURL(requestURL.String()))
		if err != nil {
			return nil, err
		}
	}
	query.Set("client_version", clientVersion)
	requestURL.RawQuery = query.Encode()
	return requestURL, nil
}
