package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestTryTransformNativeCodexOAuthRequestPreservesStablePromptFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "session-raw")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "thread-raw")
	c.Request.Header.Set(openAIOfficialClientRequestIDHeader, "thread-raw")
	c.Request.Header.Set(openAICodexParentThreadIDHeader, "parent-thread-raw")
	c.Request.Header.Set("x-codex-window-id", "thread-raw:3")
	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"prompt_cache_key":"session-raw","session_id":"session-raw","thread_id":"thread-raw","parent_thread_id":"parent-thread-raw","window_id":"thread-raw:3","turn_id":"turn-1"}`)
	c.Set("api_key", &APIKey{ID: 41, Key: "sk-test-key"})

	original := []byte(`{
		"model":"gpt-5.5",
		"instructions":"stable <system> instructions",
		"tools":[{"type":"function","name":"shell","description":"stable tool","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"tool_choice":"auto",
		"parallel_tool_calls":true,
		"reasoning":{"effort":"high","summary":"auto"},
		"text":{"verbosity":"low","format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"ok":{"type":"boolean"}}}}},
		"prompt_cache_key":"session-raw",
		"client_metadata":{
			"session_id":"session-raw",
			"thread_id":"thread-raw",
			"x-codex-parent-thread-id":"parent-thread-raw",
			"x-codex-window-id":"thread-raw:3",
			"x-codex-turn-metadata":"{\"prompt_cache_key\":\"session-raw\",\"session_id\":\"session-raw\",\"thread_id\":\"thread-raw\",\"parent_thread_id\":\"parent-thread-raw\",\"window_id\":\"thread-raw:3\",\"turn_id\":\"turn-1\"}"
		},
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"store":true,
		"stream":false,
		"stream_options":{"include_usage":true}
	}`)

	account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	transformed, result, handled, err := tryTransformNativeCodexOAuthRequest(c, account, original)
	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, result.Modified)
	require.Equal(t, "session-raw", result.PromptCacheKey)
	require.Equal(t, "gpt-5.5", result.NormalizedModel)

	for _, field := range []string{"instructions", "tools", "input", "text"} {
		require.Equal(t, gjson.GetBytes(original, field).Raw, gjson.GetBytes(transformed, field).Raw, field)
	}
	require.False(t, gjson.GetBytes(transformed, "store").Bool())
	require.True(t, gjson.GetBytes(transformed, "stream").Bool())
	require.False(t, gjson.GetBytes(transformed, "stream_options").Exists())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(transformed, "include.0").String())

	relayIdentity, relayOK := newOpenAICodexRelayIdentity(c, account)
	require.True(t, relayOK)
	wantSession := relayIdentity.pseudonymize("session_id", "session-raw")
	wantThread := relayIdentity.pseudonymize("thread_id", "thread-raw")
	wantParentThread := relayIdentity.pseudonymize("thread_id", "parent-thread-raw")
	wantWindow := relayIdentity.pseudonymize("window_id", "thread-raw:3")
	wantTurn := relayIdentity.pseudonymize("turn_id", "turn-1")
	require.Equal(t, wantSession, gjson.GetBytes(transformed, "prompt_cache_key").String())
	require.Equal(t, wantSession, gjson.GetBytes(transformed, "client_metadata.session_id").String())
	require.Equal(t, wantThread, gjson.GetBytes(transformed, "client_metadata.thread_id").String())
	require.Equal(t, wantParentThread, gjson.GetBytes(transformed, "client_metadata.x-codex-parent-thread-id").String())
	require.Equal(t, wantWindow, gjson.GetBytes(transformed, "client_metadata.x-codex-window-id").String())

	bodyTurnMetadata := gjson.GetBytes(transformed, "client_metadata.x-codex-turn-metadata").String()
	require.Equal(t, "session-raw", gjson.Get(bodyTurnMetadata, "prompt_cache_key").String())
	require.Equal(t, wantSession, gjson.Get(bodyTurnMetadata, "session_id").String())
	require.Equal(t, wantThread, gjson.Get(bodyTurnMetadata, "thread_id").String())
	require.Equal(t, wantParentThread, gjson.Get(bodyTurnMetadata, "parent_thread_id").String())
	require.Equal(t, wantWindow, gjson.Get(bodyTurnMetadata, "window_id").String())
	require.Equal(t, wantTurn, gjson.Get(bodyTurnMetadata, "turn_id").String())

	identity, ok := openAICodexUpstreamIdentityFromContext(c)
	require.True(t, ok)
	headers := http.Header{}
	applyOpenAICodexUpstreamIdentityHeaders(headers, identity)
	require.Equal(t, wantSession, headers.Get(openAIOfficialSessionIDHeader))
	require.Equal(t, wantSession, headers.Get("session_id"))
	require.Equal(t, wantThread, headers.Get(openAIOfficialThreadIDHeader))
	require.Equal(t, wantThread, headers.Get(openAIOfficialClientRequestIDHeader))
	require.Equal(t, wantParentThread, headers.Get(openAICodexParentThreadIDHeader))
	require.Equal(t, wantWindow, headers.Get("x-codex-window-id"))
	require.Equal(t, "session-raw", gjson.Get(headers.Get(openAIWSTurnMetadataHeader), "prompt_cache_key").String())
	require.Equal(t, wantSession, gjson.Get(headers.Get(openAIWSTurnMetadataHeader), "session_id").String())
	require.Equal(t, wantParentThread, gjson.Get(headers.Get(openAIWSTurnMetadataHeader), "parent_thread_id").String())
}

func TestTryTransformNativeCodexOAuthRequestKeepsPriorInputPrefixAcrossTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newContext := func() *gin.Context {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set(openAIOfficialSessionIDHeader, "session-prefix")
		c.Request.Header.Set(openAIOfficialThreadIDHeader, "thread-prefix")
		c.Set("api_key", &APIKey{ID: 42, Key: "sk-test-key"})
		return c
	}

	staticPrefix := `"model":"gpt-5.5","instructions":"stable instructions","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"tool_choice":"auto","reasoning":{"effort":"high"},"text":{"verbosity":"low"},"prompt_cache_key":"session-prefix","client_metadata":{"session_id":"session-prefix","thread_id":"thread-prefix","x-codex-window-id":"thread-prefix:0"},`
	first := []byte(`{` + staticPrefix + `"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},{"type":"function_call","id":"fc_1","call_id":"fc_1","name":"shell","arguments":"{\"command\":\"pwd\"}"}]}`)
	second := []byte(`{` + staticPrefix + `"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},{"type":"function_call","id":"fc_1","call_id":"fc_1","name":"shell","arguments":"{\"command\":\"pwd\"}"},{"type":"function_call_output","call_id":"fc_1","output":"ok"},{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)

	account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	firstTransformed, firstResult, firstHandled, err := tryTransformNativeCodexOAuthRequest(newContext(), account, first)
	require.NoError(t, err)
	require.True(t, firstHandled)
	secondTransformed, secondResult, secondHandled, err := tryTransformNativeCodexOAuthRequest(newContext(), account, second)
	require.NoError(t, err)
	require.True(t, secondHandled)

	require.Equal(t, firstResult.PromptCacheKey, secondResult.PromptCacheKey)
	require.Equal(t, gjson.GetBytes(firstTransformed, "prompt_cache_key").String(), gjson.GetBytes(secondTransformed, "prompt_cache_key").String())
	for _, field := range []string{"instructions", "tools", "text"} {
		require.Equal(t, gjson.GetBytes(firstTransformed, field).Raw, gjson.GetBytes(secondTransformed, field).Raw, field)
	}
	require.Equal(t, gjson.GetBytes(firstTransformed, "input.0").Raw, gjson.GetBytes(secondTransformed, "input.0").Raw)
	require.Equal(t, gjson.GetBytes(firstTransformed, "input.1").Raw, gjson.GetBytes(secondTransformed, "input.1").Raw)
}

func TestTryTransformNativeCodexOAuthRequestSharesSessionKeyAcrossDistinctThreads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	transformForThread := func(threadID string) []byte {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set(openAIOfficialSessionIDHeader, "shared-session")
		c.Request.Header.Set(openAIOfficialThreadIDHeader, threadID)
		c.Set("api_key", &APIKey{ID: 45, Key: "sk-test-key"})
		body := []byte(`{"model":"gpt-5.5","instructions":"stable","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"tool_choice":"auto","prompt_cache_key":"shared-session","client_metadata":{"session_id":"shared-session","thread_id":"` + threadID + `","x-codex-window-id":"` + threadID + `:0"},"input":[]}`)
		account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		transformed, _, handled, err := tryTransformNativeCodexOAuthRequest(c, account, body)
		require.NoError(t, err)
		require.True(t, handled)
		return transformed
	}

	root := transformForThread("root-thread")
	child := transformForThread("child-thread")
	require.Equal(t, gjson.GetBytes(root, "prompt_cache_key").String(), gjson.GetBytes(child, "prompt_cache_key").String())
	require.NotEqual(t, gjson.GetBytes(root, "client_metadata.thread_id").String(), gjson.GetBytes(child, "client_metadata.thread_id").String())
}

func TestTryTransformNativeCodexOAuthRequestKeepsEncryptedReasoningButDropsReplayID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "session-reasoning")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "thread-reasoning")
	c.Set("api_key", &APIKey{ID: 43, Key: "sk-test-key"})

	original := []byte(`{"model":"gpt-5.5","instructions":"stable","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"tool_choice":"auto","reasoning":{"effort":"high"},"prompt_cache_key":"session-reasoning","input":[{"type":"message","role":"user","content":"first"},{"type":"reasoning","id":"rs_123","encrypted_content":"encrypted-value","content":null},{"type":"message","role":"user","content":"next"}]}`)
	account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	transformed, _, handled, err := tryTransformNativeCodexOAuthRequest(c, account, original)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, gjson.GetBytes(original, "input.0").Raw, gjson.GetBytes(transformed, "input.0").Raw)
	require.Equal(t, gjson.GetBytes(original, "input.2").Raw, gjson.GetBytes(transformed, "input.2").Raw)
	require.False(t, gjson.GetBytes(transformed, "input.1.id").Exists())
	require.Equal(t, "encrypted-value", gjson.GetBytes(transformed, "input.1.encrypted_content").String())
	require.True(t, gjson.GetBytes(transformed, "input.1.summary").IsArray())
}

func TestPatchNativeCodexReasoningIncludeAppendsWithoutReplacingExistingValues(t *testing.T) {
	body := []byte(`{"reasoning":{"effort":"high"},"include":["file_search_call.results"]}`)
	transformed, err := patchNativeCodexReasoningInclude(body)
	require.NoError(t, err)
	require.Equal(t, "file_search_call.results", gjson.GetBytes(transformed, "include.0").String())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(transformed, "include.1").String())
}

func TestTryTransformNativeCodexOAuthRequestFallsBackForCompatibilityTools(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","instructions":"stable","tools":[{"type":"function","function":{"name":"shell","parameters":{"type":"object"}}}],"input":[{"role":"user","content":"hello"}]}`)
	transformed, result, handled, err := tryTransformNativeCodexOAuthRequest(nil, &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, body)
	require.NoError(t, err)
	require.False(t, handled)
	require.Equal(t, codexTransformResult{}, result)
	require.Equal(t, body, transformed)
}

func TestOpenAIGatewayServiceNativeCodexOAuthIdentityDoesNotDependOnOptimization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	original := []byte(`{"model":"gpt-5.5","instructions":"stable instructions","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"tool_choice":"auto","prompt_cache_key":"default-session","client_metadata":{"session_id":"default-session","thread_id":"default-thread"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"store":true,"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(original))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", codexCLIUserAgent)
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "default-session")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "default-thread")
	c.Request.Header.Set(openAIOfficialClientRequestIDHeader, "default-thread")
	c.Set("api_key", &APIKey{ID: 44, Key: "sk-test-key"})

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"captured"}}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          800,
		Name:        "native-codex-default",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-account",
		},
		Status:      StatusActive,
		Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, original)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)
	relayIdentity, relayOK := newOpenAICodexRelayIdentity(c, account)
	require.True(t, relayOK)
	wantSession := relayIdentity.pseudonymize("session_id", "default-session")
	wantThread := relayIdentity.pseudonymize("thread_id", "default-thread")
	require.Equal(t, wantSession, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, wantSession, gjson.GetBytes(upstream.lastBody, "client_metadata.session_id").String())
	require.Equal(t, wantThread, gjson.GetBytes(upstream.lastBody, "client_metadata.thread_id").String())
	require.Equal(t, wantSession, upstream.lastReq.Header.Get(openAIOfficialSessionIDHeader))
	require.Equal(t, wantThread, upstream.lastReq.Header.Get(openAIOfficialThreadIDHeader))
	require.Equal(t, wantThread, upstream.lastReq.Header.Get(openAIOfficialClientRequestIDHeader))
	_, hasProtocolIdentity := openAICodexUpstreamIdentityFromContext(c)
	require.True(t, hasProtocolIdentity)
}

func TestPrepareOpenAICodexUpstreamIdentityUsesHeaderSessionSeedAndKeepsBodyIndependent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "header-session")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "root-thread")
	c.Request.Header.Set(openAIOfficialClientRequestIDHeader, "mismatched-client-request")
	c.Request.Header.Set(openAICodexParentThreadIDHeader, "parent-thread")
	c.Set("api_key", &APIKey{ID: 91, Key: "sk-test-key"})

	body := []byte(`{"model":"gpt-5.5","prompt_cache_key":"body-session","client_metadata":{"session_id":"stale-session","thread_id":"stale-thread","root_turn_id":"body-root-turn","x-client-request-id":"body-client-request","x-codex-parent-thread-id":"stale-parent","x-codex-turn-metadata":"{\"session_id\":\"stale-session\",\"thread_id\":\"stale-thread\",\"root_turn_id\":\"body-root-turn\",\"parent_thread_id\":\"stale-parent\"}"}}`)
	account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	transformed, identity, err := prepareOpenAICodexUpstreamIdentity(c, account, body, false)
	require.NoError(t, err)

	relayIdentity, relayOK := newOpenAICodexRelayIdentity(c, account)
	require.True(t, relayOK)
	wantSession := relayIdentity.pseudonymize("session_id", "header-session")
	wantPromptCacheKey := relayIdentity.pseudonymize("session_id", "body-session")
	wantThread := relayIdentity.pseudonymize("thread_id", "root-thread")
	wantClientRequest := relayIdentity.pseudonymize("thread_id", "mismatched-client-request")
	wantParentThread := relayIdentity.pseudonymize("thread_id", "parent-thread")
	wantBodySession := relayIdentity.pseudonymize("session_id", "stale-session")
	wantBodyThread := relayIdentity.pseudonymize("thread_id", "stale-thread")
	wantBodyRootTurn := relayIdentity.pseudonymize("turn_id", "body-root-turn")
	wantBodyClientRequest := relayIdentity.pseudonymize("thread_id", "body-client-request")
	wantBodyParentThread := relayIdentity.pseudonymize("thread_id", "stale-parent")
	require.Equal(t, wantSession, identity.SessionID)
	require.Equal(t, wantThread, identity.ThreadID)
	require.Equal(t, wantParentThread, identity.ParentThreadID)
	require.Equal(t, wantPromptCacheKey, gjson.GetBytes(transformed, "prompt_cache_key").String())
	require.Equal(t, wantBodySession, gjson.GetBytes(transformed, "client_metadata.session_id").String())
	require.Equal(t, wantBodyThread, gjson.GetBytes(transformed, "client_metadata.thread_id").String())
	require.Equal(t, wantBodyRootTurn, gjson.GetBytes(transformed, "client_metadata.root_turn_id").String())
	require.Equal(t, wantBodyClientRequest, gjson.GetBytes(transformed, "client_metadata.x-client-request-id").String())
	require.Equal(t, wantBodyParentThread, gjson.GetBytes(transformed, "client_metadata.x-codex-parent-thread-id").String())
	require.Equal(t, wantBodyRootTurn, gjson.Get(gjson.GetBytes(transformed, "client_metadata.x-codex-turn-metadata").String(), "root_turn_id").String())
	require.Equal(t, wantBodyParentThread, gjson.Get(gjson.GetBytes(transformed, "client_metadata.x-codex-turn-metadata").String(), "parent_thread_id").String())

	headers := http.Header{}
	applyOpenAICodexUpstreamIdentityHeaders(headers, identity)
	require.Equal(t, wantSession, headers.Get(openAIOfficialSessionIDHeader))
	require.Equal(t, wantThread, headers.Get(openAIOfficialThreadIDHeader))
	require.Equal(t, wantClientRequest, headers.Get(openAIOfficialClientRequestIDHeader))
	require.Equal(t, wantParentThread, headers.Get(openAICodexParentThreadIDHeader))
}

func TestPrepareOpenAICodexUpstreamIdentityNormalizesInstallationAndForkMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "fork-session")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "child-thread")
	c.Request.Header.Set(
		openAIWSTurnMetadataHeader,
		`{"installation_id":"client-installation","session_id":"fork-session","thread_id":"child-thread","forked_from_thread_id":"root-thread"}`,
	)
	c.Set("api_key", &APIKey{ID: 509, Key: "sk-test-key"})

	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"openai_device_id": "account-installation"},
	}
	body := []byte(`{
		"model":"gpt-5.5",
		"prompt_cache_key":"fork-session",
		"client_metadata":{
			"session_id":"fork-session",
			"thread_id":"child-thread",
			"x-codex-installation-id":"client-installation",
			"x-codex-turn-metadata":"{\"installation_id\":\"client-installation\",\"session_id\":\"fork-session\",\"thread_id\":\"child-thread\",\"forked_from_thread_id\":\"root-thread\"}"
		}
	}`)

	transformed, identity, err := prepareOpenAICodexUpstreamIdentity(c, account, body, false)
	require.NoError(t, err)

	wantSession := identity.RelayIdentity.pseudonymize("session_id", "fork-session")
	wantThread := identity.RelayIdentity.pseudonymize("thread_id", "child-thread")
	wantForkedFromThread := identity.RelayIdentity.pseudonymize("thread_id", "root-thread")
	require.Equal(t, "account-installation", identity.InstallationID)
	require.Equal(t, wantSession, identity.SessionID)
	require.Equal(t, wantThread, identity.ThreadID)
	require.Equal(t, "account-installation", gjson.GetBytes(transformed, "client_metadata.x-codex-installation-id").String())

	bodyTurnMetadata := gjson.GetBytes(transformed, "client_metadata.x-codex-turn-metadata").String()
	require.Equal(t, "account-installation", gjson.Get(bodyTurnMetadata, "installation_id").String())
	require.Equal(t, wantSession, gjson.Get(bodyTurnMetadata, "session_id").String())
	require.Equal(t, wantThread, gjson.Get(bodyTurnMetadata, "thread_id").String())
	require.Equal(t, wantForkedFromThread, gjson.Get(bodyTurnMetadata, "forked_from_thread_id").String())

	headers := http.Header{"X-Codex-Installation-Id": []string{"client-installation"}}
	applyOpenAICodexUpstreamIdentityHeaders(headers, identity)
	require.Equal(t, "account-installation", headers.Get("x-codex-installation-id"))
	require.Equal(t, wantSession, headers.Get(openAIOfficialSessionIDHeader))
	require.Equal(t, wantThread, headers.Get(openAIOfficialThreadIDHeader))
	require.Equal(t, "account-installation", gjson.Get(headers.Get(openAIWSTurnMetadataHeader), "installation_id").String())
	require.Equal(t, wantForkedFromThread, gjson.Get(headers.Get(openAIWSTurnMetadataHeader), "forked_from_thread_id").String())
}

func TestPrepareOpenAICodexUpstreamIdentityDoesNotPatchCompactBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "compact-session")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "compact-thread")
	c.Set("api_key", &APIKey{ID: 92, Key: "sk-test-key"})

	body := []byte(`{"model":"gpt-5.5","input":[]}`)
	account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	transformed, identity, err := prepareOpenAICodexUpstreamIdentity(c, account, body, true)
	require.NoError(t, err)
	require.Equal(t, identity.RelayIdentity.pseudonymize("session_id", "compact-session"), identity.SessionID)
	require.Equal(t, identity.RelayIdentity.pseudonymize("thread_id", "compact-thread"), identity.ThreadID)
	require.Equal(t, body, transformed)
}

func TestPrepareOpenAICodexUpstreamIdentityDoesNotCreateClientMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "session-only")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "thread-only")
	c.Set("api_key", &APIKey{ID: 93, Key: "sk-test-key"})

	transformed, _, err := prepareOpenAICodexUpstreamIdentity(
		c,
		&Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		[]byte(`{"model":"gpt-5.5","input":[]}`),
		false,
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(transformed, "prompt_cache_key").Exists())
	require.False(t, gjson.GetBytes(transformed, "client_metadata").Exists())
}

func TestPrepareOpenAICodexUpstreamIdentityCarriesSessionAcrossWebSocketFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set("api_key", &APIKey{ID: 94, Key: "sk-test-key"})
	account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	firstBody := []byte(`{"type":"response.create","model":"gpt-5.5","prompt_cache_key":"ws-session","client_metadata":{"session_id":"ws-session","thread_id":"ws-thread","x-codex-parent-thread-id":"ws-parent-thread","x-codex-window-id":"ws-thread:0","x-codex-turn-metadata":"{\"session_id\":\"ws-session\",\"thread_id\":\"ws-thread\",\"parent_thread_id\":\"ws-parent-thread\",\"window_id\":\"ws-thread:0\"}"}}`)
	first, firstIdentity, err := prepareOpenAICodexUpstreamIdentity(c, account, firstBody, false)
	require.NoError(t, err)

	secondBody := []byte(`{"type":"response.create","model":"gpt-5.5","previous_response_id":"resp_1"}`)
	second, secondIdentity, err := prepareOpenAICodexUpstreamIdentity(c, account, secondBody, false)
	require.NoError(t, err)

	require.Equal(t, firstIdentity, secondIdentity)
	require.Equal(t, firstIdentity.SessionID, gjson.GetBytes(first, "prompt_cache_key").String())
	require.False(t, gjson.GetBytes(second, "prompt_cache_key").Exists())
	require.Empty(t, firstIdentity.ParentThreadID)
	require.False(t, gjson.GetBytes(second, "client_metadata").Exists())
}

func TestPrepareOpenAICodexUpstreamIdentityKeepsBodySessionAcrossThreadScopedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type preparedThread struct {
		body     []byte
		identity openAICodexUpstreamIdentity
	}
	prepareThread := func(threadID, parentThreadID string) preparedThread {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set(openAIOfficialThreadIDHeader, threadID)
		if parentThreadID != "" {
			c.Request.Header.Set(openAICodexParentThreadIDHeader, parentThreadID)
		}
		c.Set("api_key", &APIKey{ID: 95, Key: "sk-test-key"})
		body := []byte(`{"model":"gpt-5.5","prompt_cache_key":"shared-session","client_metadata":{"session_id":"shared-session","thread_id":"` + threadID + `","x-codex-turn-metadata":"{\"session_id\":\"shared-session\",\"thread_id\":\"` + threadID + `\",\"parent_thread_id\":\"` + parentThreadID + `\"}"}}`)
		transformed, identity, err := prepareOpenAICodexUpstreamIdentity(c, &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, body, false)
		require.NoError(t, err)
		return preparedThread{body: transformed, identity: identity}
	}

	root := prepareThread("root-thread", "")
	child := prepareThread("child-thread", "root-thread")
	require.NotEqual(t, root.identity.SessionID, child.identity.SessionID)
	require.Equal(t, gjson.GetBytes(root.body, "prompt_cache_key").String(), gjson.GetBytes(child.body, "prompt_cache_key").String())
	require.NotEqual(t, root.identity.ThreadID, child.identity.ThreadID)
	require.Equal(t, root.identity.ThreadID, child.identity.ParentThreadID)
	require.False(t, gjson.GetBytes(child.body, "client_metadata.x-codex-parent-thread-id").Exists())
	childTurnMetadata := gjson.GetBytes(child.body, "client_metadata.x-codex-turn-metadata").String()
	require.Equal(t, root.identity.ThreadID, gjson.Get(childTurnMetadata, "parent_thread_id").String())
}

func TestOpenAIGatewayServiceCodexIdentityLegacyAndPassthroughHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.5","instructions":"stable","tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"tool_choice":"auto","stream":false,"prompt_cache_key":"http-session","client_metadata":{"session_id":"http-session","thread_id":"http-thread","x-codex-parent-thread-id":"http-parent-thread","x-codex-window-id":"http-thread:0","x-codex-turn-metadata":"{\"session_id\":\"http-session\",\"thread_id\":\"http-thread\",\"parent_thread_id\":\"http-parent-thread\",\"window_id\":\"http-thread:0\"}"},"input":[{"type":"message","role":"user","content":"hello"}]}`)
	type capturedRequest struct {
		body     []byte
		headers  http.Header
		identity openAICodexUpstreamIdentity
	}
	capture := func(passthrough bool) capturedRequest {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("User-Agent", codexCLIUserAgent)
		c.Request.Header.Set("originator", "codex_cli_rs")
		c.Request.Header.Set(openAIOfficialSessionIDHeader, "http-session")
		c.Request.Header.Set(openAIOfficialThreadIDHeader, "http-thread")
		c.Request.Header.Set(openAICodexParentThreadIDHeader, "http-parent-thread")
		c.Request.Header.Set("X-OAI-Attestation", "attestation-http")
		c.Request.Header.Set("X-ResponsesAPI-Include-Timing-Metrics", "true")
		c.Set("api_key", &APIKey{ID: 96, Key: "sk-test-key"})

		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"captured"}}`)),
		}}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		account := &Account{
			ID:          801,
			Name:        "codex-http",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{
				"access_token":       "oauth-token",
				"chatgpt_account_id": "chatgpt-account",
			},
			Extra:       map[string]any{"openai_passthrough": passthrough},
			Status:      StatusActive,
			Schedulable: true,
		}

		result, err := svc.Forward(context.Background(), c, account, body)
		require.Error(t, err)
		require.Nil(t, result)
		require.NotNil(t, upstream.lastReq)
		identity, ok := openAICodexUpstreamIdentityFromContext(c)
		require.True(t, ok)
		return capturedRequest{
			body:     append([]byte(nil), upstream.lastBody...),
			headers:  upstream.lastReq.Header.Clone(),
			identity: identity,
		}
	}

	legacy := capture(false)
	passthrough := capture(true)

	for _, captured := range []capturedRequest{legacy, passthrough} {
		relayIdentity := captured.identity.RelayIdentity
		wantSession := relayIdentity.pseudonymize("session_id", "http-session")
		wantThread := relayIdentity.pseudonymize("thread_id", "http-thread")
		wantParentThread := relayIdentity.pseudonymize("thread_id", "http-parent-thread")
		require.Equal(t, wantSession, gjson.GetBytes(captured.body, "prompt_cache_key").String())
		require.Equal(t, wantSession, gjson.GetBytes(captured.body, "client_metadata.session_id").String())
		require.Equal(t, wantThread, gjson.GetBytes(captured.body, "client_metadata.thread_id").String())
		require.Equal(t, wantParentThread, gjson.GetBytes(captured.body, "client_metadata.x-codex-parent-thread-id").String())
		turnMetadata := gjson.GetBytes(captured.body, "client_metadata.x-codex-turn-metadata").String()
		require.Equal(t, wantParentThread, gjson.Get(turnMetadata, "parent_thread_id").String())
		require.Equal(t, wantSession, captured.headers.Get(openAIOfficialSessionIDHeader))
		require.Equal(t, wantThread, captured.headers.Get(openAIOfficialThreadIDHeader))
		require.Empty(t, captured.headers.Get(openAIOfficialClientRequestIDHeader))
		require.Equal(t, wantParentThread, captured.headers.Get(openAICodexParentThreadIDHeader))
		require.Equal(t, "attestation-http", captured.headers.Get("X-OAI-Attestation"))
		require.Equal(t, "true", captured.headers.Get("X-ResponsesAPI-Include-Timing-Metrics"))
	}
}

func TestOpenAIGatewayServiceNativeCodexOAuthUsesStablePatchedBodyAndIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	original := []byte(`{"model":"gpt-5.5","instructions":"stable instructions","tools":[{"type":"function","name":"shell","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],"tool_choice":"auto","parallel_tool_calls":true,"reasoning":{"effort":"high"},"text":{"verbosity":"low"},"prompt_cache_key":"session-http","client_metadata":{"session_id":"session-http","thread_id":"thread-http","x-codex-window-id":"thread-http:0","x-codex-turn-metadata":"{\"session_id\":\"session-http\",\"thread_id\":\"thread-http\",\"window_id\":\"thread-http:0\",\"turn_id\":\"turn-http\"}"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"store":true,"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(original))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", codexCLIUserAgent)
	c.Request.Header.Set("originator", "codex_cli_rs")
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "session-http")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "thread-http")
	c.Request.Header.Set(openAIOfficialClientRequestIDHeader, "thread-http")
	c.Request.Header.Set("x-codex-window-id", "thread-http:0")
	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"session_id":"session-http","thread_id":"thread-http","window_id":"thread-http:0","turn_id":"turn-http"}`)
	c.Set("api_key", &APIKey{ID: 44, Key: "sk-test-key"})

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"captured"}}`)),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAICodexPromptCacheOptimizationEnabled: true,
		}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          800,
		Name:        "native-codex",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-account",
		},
		Status:      StatusActive,
		Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, original)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, upstream.lastReq)

	for _, field := range []string{"instructions", "tools", "input", "text"} {
		require.Equal(t, gjson.GetBytes(original, field).Raw, gjson.GetBytes(upstream.lastBody, field).Raw, field)
	}
	relayIdentity, relayOK := newOpenAICodexRelayIdentity(c, account)
	require.True(t, relayOK)
	wantSession := relayIdentity.pseudonymize("session_id", "session-http")
	wantThread := relayIdentity.pseudonymize("thread_id", "thread-http")
	wantWindow := relayIdentity.pseudonymize("window_id", "thread-http:0")
	require.Equal(t, wantSession, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, wantSession, gjson.GetBytes(upstream.lastBody, "client_metadata.session_id").String())
	require.Equal(t, wantThread, gjson.GetBytes(upstream.lastBody, "client_metadata.thread_id").String())
	require.Equal(t, wantWindow, gjson.GetBytes(upstream.lastBody, "client_metadata.x-codex-window-id").String())
	require.Equal(t, wantSession, upstream.lastReq.Header.Get(openAIOfficialSessionIDHeader))
	require.Equal(t, wantSession, upstream.lastReq.Header.Get("session_id"))
	require.Equal(t, wantThread, upstream.lastReq.Header.Get(openAIOfficialThreadIDHeader))
	require.Equal(t, wantThread, upstream.lastReq.Header.Get(openAIOfficialClientRequestIDHeader))
	require.Equal(t, wantWindow, upstream.lastReq.Header.Get("x-codex-window-id"))
	require.Equal(t, wantSession, gjson.Get(upstream.lastReq.Header.Get(openAIWSTurnMetadataHeader), "session_id").String())
	require.Equal(t, wantThread, gjson.Get(upstream.lastReq.Header.Get(openAIWSTurnMetadataHeader), "thread_id").String())
}
