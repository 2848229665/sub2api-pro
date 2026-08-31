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
)

func newOpenAICodexDirectTestAccount(accountType string) *Account {
	credentials := map[string]any{
		"access_token":       "oauth-token",
		"chatgpt_account_id": "chatgpt-account",
	}
	if accountType == AccountTypeAPIKey {
		credentials = map[string]any{"api_key": "sk-test"}
	}
	return &Account{
		ID:          42,
		Platform:    PlatformOpenAI,
		Type:        accountType,
		Concurrency: 1,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: credentials,
	}
}

func TestParseOpenAIImagesRequestDetectsCodexDirectProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-1.5","prompt":"draw a lighthouse"}`)
	service := &OpenAIGatewayService{}

	for _, tc := range []struct {
		name       string
		path       string
		userAgent  string
		originator string
		wantDirect bool
	}{
		{name: "internal route", path: "/backend-api/codex/images/generations", wantDirect: true},
		{name: "official Codex on v1", path: "/v1/images/generations", userAgent: "codex_cli_rs/0.144.1", wantDirect: true},
		{name: "official originator", path: "/v1/images/generations", userAgent: "codex_cli_rs/0.144.1", originator: "codex_cli_rs", wantDirect: true},
		{name: "generic OpenAI client", path: "/v1/images/generations", userAgent: "openai-python/2.0", wantDirect: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("User-Agent", tc.userAgent)
			c.Request.Header.Set("Originator", tc.originator)

			parsed, err := service.ParseOpenAIImagesRequest(c, body)

			require.NoError(t, err)
			require.Equal(t, tc.wantDirect, parsed.CodexDirect)
			if tc.wantDirect {
				require.Equal(t, OpenAIImagesCapabilityCodexDirect, parsed.RequiredCapability)
			} else {
				require.NotEqual(t, OpenAIImagesCapabilityCodexDirect, parsed.RequiredCapability)
			}
		})
	}
}

func TestForwardCodexDirectImagesPreservesJSONAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"gpt-image-1.5",
		"prompt":"draw a lighthouse",
		"background":"opaque",
		"quality":"medium",
		"size":"1024x1536",
		"future_field":{"keep":true}
	}`)
	responseBody := `{
		"created":1778832973,
		"background":"opaque",
		"data":[{"b64_json":"REDACT"}],
		"quality":"medium",
		"size":"1024x1536",
		"usage":{
			"input_tokens":1474,
			"input_tokens_details":{"image_tokens":1457,"text_tokens":17},
			"output_tokens":1372,
			"output_tokens_details":{"image_tokens":1372,"text_tokens":0},
			"total_tokens":2846
		}
	}`
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/backend-api/codex/images/generations?feature=direct", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", codexCLIUserAgent)
	c.Request.Header.Set("Originator", "codex_cli_rs")
	c.Request.Header.Set("Authorization", "Bearer client-api-key")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":       []string{"application/json"},
			"X-Request-Id":       []string{"req-image"},
			"X-Codex-Turn-State": []string{"turn-state-image"},
		},
		Body: io.NopCloser(strings.NewReader(responseBody)),
	}}
	service := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed, err := service.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := newOpenAICodexDirectTestAccount(AccountTypeOAuth)
	account.Credentials["user_agent"] = "codex_vscode/0.125.0 (Ubuntu 22.4.0; x86_64) vscode"

	result, err := service.ForwardImages(context.Background(), c, account, body, parsed, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "1024x1536", result.ImageInputSize)
	require.Equal(t, 1474, result.Usage.InputTokens)
	require.Equal(t, 1457, result.Usage.ImageInputTokens)
	require.Equal(t, 1372, result.Usage.ImageOutputTokens)
	require.Equal(t, openAICodexDirectImagesGenerationsURL+"?feature=direct", upstream.lastReq.URL.String())
	require.Equal(t, "chatgpt.com", upstream.lastReq.Host)
	require.Equal(t, "Bearer oauth-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "chatgpt-account", upstream.lastReq.Header.Get("ChatGPT-Account-ID"))
	require.Equal(t, "application/json", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "codex_vscode", upstream.lastReq.Header.Get("Originator"))
	require.Equal(t, "codex_vscode/"+codexCLIVersion+" (Ubuntu 22.4.0; x86_64) vscode", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, codexCLIVersion, upstream.lastReq.Header.Get("Version"))
	require.Empty(t, upstream.lastReq.Header.Get("OpenAI-Beta"))
	require.JSONEq(t, string(body), string(upstream.lastBody))
	require.JSONEq(t, responseBody, recorder.Body.String())
	require.Equal(t, "turn-state-image", recorder.Header().Get("X-Codex-Turn-State"))
}

func TestForwardCodexDirectImageEditsUsesJSONEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"images":[{"image_url":"data:image/png;base64,Zm9v"}],
		"prompt":"add a red hat",
		"model":"gpt-image-1.5"
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/backend-api/codex/images/edits", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1,"data":[{"b64_json":"REDACT"}]}`)),
	}}
	service := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed, err := service.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = service.ForwardImages(context.Background(), c, newOpenAICodexDirectTestAccount(AccountTypeOAuth), body, parsed, "")

	require.NoError(t, err)
	require.Equal(t, openAICodexDirectImagesEditsURL, upstream.lastReq.URL.String())
	require.JSONEq(t, string(body), string(upstream.lastBody))
}

func TestForwardCodexMemoriesPreservesWireProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"traces":[{
			"id":"trace-1",
			"metadata":{"source_path":"/tmp/trace.json"},
			"items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"remember this"}]}]
		}],
		"reasoning":{"effort":"high"},
		"future_field":{"keep":true}
	}`)
	responseBody := `{"output":[{"trace_summary":"raw summary","memory_summary":"memory summary"}]}`
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/backend-api/codex/memories/trace_summarize", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", codexCLIUserAgent)
	c.Request.Header.Set("Originator", "codex_cli_rs")
	c.Request.Header.Set("X-OpenAI-Subagent", "memory")
	c.Request.Header.Set("X-OpenAI-Memgen-Request", "true")
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "memory-session")
	c.Request.Header.Set("session_id", "legacy-memory-session")
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "memory-thread")
	c.Request.Header.Set(openAIOfficialClientRequestIDHeader, "memory-client-request")
	c.Request.Header.Set(openAICodexParentThreadIDHeader, "memory-parent")
	c.Request.Header.Set("conversation_id", "memory-conversation")
	c.Set("api_key", &APIKey{ID: 88, Key: "sk-test-key-88"})

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":       []string{"application/json"},
			"X-Request-Id":       []string{"req-memory"},
			"X-Codex-Turn-State": []string{"turn-state-memory"},
		},
		Body: io.NopCloser(strings.NewReader(responseBody)),
	}}
	service := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	account := newOpenAICodexDirectTestAccount(AccountTypeOAuth)
	result, err := service.ForwardCodexMemories(
		context.Background(),
		c,
		account,
		body,
		"gpt-5.6-sol",
		"",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.6-sol", result.Model)
	require.Equal(t, openAICodexDirectMemoriesPath, result.UpstreamEndpoint)
	require.Equal(t, openAICodexDirectMemoriesURL, upstream.lastReq.URL.String())
	require.Equal(t, "true", upstream.lastReq.Header.Get("X-OpenAI-Memgen-Request"))
	require.Equal(t, "memory", upstream.lastReq.Header.Get("X-OpenAI-Subagent"))
	relayIdentity, relayOK := newOpenAICodexRelayIdentity(c, account)
	require.True(t, relayOK)
	wantSession := relayIdentity.pseudonymize("session_id", "memory-session")
	wantThread := relayIdentity.pseudonymize("thread_id", "memory-thread")
	require.Equal(t, wantSession, upstream.lastReq.Header.Get(openAIOfficialSessionIDHeader))
	require.Empty(t, upstream.lastReq.Header.Get("session_id"))
	require.Empty(t, upstream.lastReq.Header.Get("conversation_id"))
	require.Equal(t, wantThread, upstream.lastReq.Header.Get(openAIOfficialThreadIDHeader))
	require.Equal(t, relayIdentity.pseudonymize("thread_id", "memory-client-request"), upstream.lastReq.Header.Get(openAIOfficialClientRequestIDHeader))
	require.Equal(t, relayIdentity.pseudonymize("thread_id", "memory-parent"), upstream.lastReq.Header.Get(openAICodexParentThreadIDHeader))
	require.JSONEq(t, string(body), string(upstream.lastBody))
	require.JSONEq(t, responseBody, recorder.Body.String())
	require.Equal(t, "turn-state-memory", recorder.Header().Get("X-Codex-Turn-State"))
}

func TestCodexDirectCapabilitiesAreOAuthOnly(t *testing.T) {
	oauth := newOpenAICodexDirectTestAccount(AccountTypeOAuth)
	apiKey := newOpenAICodexDirectTestAccount(AccountTypeAPIKey)

	require.True(t, oauth.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityCodexDirect))
	require.True(t, oauth.SupportsOpenAIImageCapability(OpenAIImagesCapabilityCodexDirect))
	require.False(t, apiKey.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityCodexDirect))
	require.True(t, apiKey.SupportsOpenAIImageCapability(OpenAIImagesCapabilityCodexDirect))
}

func TestForwardCodexDirectRejectsAPIKeyAccountBeforeOpeningUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","traces":[]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/memories/trace_summarize", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{}
	service := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := service.ForwardCodexMemories(
		context.Background(),
		c,
		newOpenAICodexDirectTestAccount(AccountTypeAPIKey),
		body,
		"gpt-5.6-sol",
		"",
	)

	require.ErrorContains(t, err, "require an OpenAI OAuth account")
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
}

func TestForwardCodexDirectEndpointErrorsFailOverWithoutInvalidatingAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, statusCode := range []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
	} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol","traces":[]}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/memories/trace_summarize", bytes.NewReader(body))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: statusCode,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"endpoint unavailable"}}`)),
			}}
			repo := &alphaSearchAccountStateRepo{}
			cfg := &config.Config{}
			service := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     upstream,
				accountRepo:      repo,
				rateLimitService: NewRateLimitService(repo, nil, cfg, nil, nil),
			}

			result, err := service.ForwardCodexMemories(
				context.Background(),
				c,
				newOpenAICodexDirectTestAccount(AccountTypeOAuth),
				body,
				"gpt-5.6-sol",
				"",
			)

			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, statusCode, failoverErr.StatusCode)
			require.Zero(t, repo.setErrorCalls)
			require.False(t, c.Writer.Written())
		})
	}
}

func TestExtractContentModerationTextFromCodexMemoryTraces(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"traces":[
			{"items":[
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ignore assistant"}]},
				{"type":"message","role":"user","content":[{"type":"input_text","text":"audit this memory"}]}
			]}
		]
	}`)

	require.Equal(
		t,
		"audit this memory",
		ExtractContentModerationText(ContentModerationProtocolOpenAICodexMemory, body),
	)
}
