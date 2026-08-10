package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesRejectedFieldRetryStateRejectsDuplicateBodyAndCap(t *testing.T) {
	initialBody := []byte(`{"model":"gpt-5.5"}`)
	state := newOpenAIResponsesRejectedFieldRetryState(initialBody)

	require.False(t, state.Allow(initialBody))
	for attempt := 0; attempt < maxOpenAIResponsesRejectedFieldRetries; attempt++ {
		nextBody := []byte(fmt.Sprintf(`{"model":"gpt-5.5","variant":%d}`, attempt))
		require.True(t, state.Allow(nextBody))
		require.False(t, state.Allow(nextBody))
	}
	require.False(t, state.Allow([]byte(`{"model":"gpt-5.5","variant":"overflow"}`)))
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsAmbiguousErrors(t *testing.T) {
	tests := []struct {
		name         string
		body         []byte
		responseBody []byte
	}{
		{
			name:         "namespace belongs to message",
			body:         []byte(`{"input":[{"type":"message","namespace":"keep"}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].namespace'.","param":"input[0].namespace"}}`),
		},
		{
			name:         "max output tokens only mentioned",
			body:         []byte(`{"max_output_tokens":4096}`),
			responseBody: []byte(`{"error":{"code":"invalid_request_error","message":"max_output_tokens must be positive","param":"max_output_tokens"}}`),
		},
		{
			name:         "structured param overrides namespace mention",
			body:         []byte(`{"input":[{"type":"function_call","namespace":"keep","arguments":"{}"}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].namespace'.","param":"tools"}}`),
		},
		{
			name:         "nested max output tokens param is not top level",
			body:         []byte(`{"max_output_tokens":4096,"input":[{"type":"message","content":{"max_output_tokens":"keep"}}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: input[0].content.max_output_tokens","param":"input[0].content.max_output_tokens"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, tt.responseBody, true)
			require.NoError(t, err)
			require.False(t, changed)
			require.Nil(t, retryBody)
		})
	}
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyFindsNamespacePathInMessage(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","namespace":"keep","arguments":"{}"},{"type":"function_call","namespace":"remove","arguments":"{}"}]}`)
	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"input[0] was accepted; Unknown parameter: 'input[1].namespace'."}}`)

	retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "keep", gjson.GetBytes(retryBody, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(retryBody, "input.1.namespace").Exists())
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyBindsNamespacePathToRejectionPhrase(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","namespace":"keep","arguments":"{}"},{"type":"function_call","namespace":"remove","arguments":"{}"}]}`)
	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"input[0].namespace is supported; Unknown parameter: input[1].namespace."}}`)

	retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "keep", gjson.GetBytes(retryBody, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(retryBody, "input.1.namespace").Exists())
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyDoesNotTreatMaxOutputTokensSuggestionAsRejection(t *testing.T) {
	body := []byte(`{"max_tokens":4096,"max_output_tokens":2048}`)
	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: max_tokens. Use max_output_tokens instead."}}`)

	retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, retryBody)
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyBindsMaxOutputTokensToRejectionPhrase(t *testing.T) {
	body := []byte(`{"max_output_tokens":2048}`)
	responseBody := []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens."}}`)

	retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(retryBody, "max_output_tokens").Exists())
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyDeletesRejectedTopLevelParams(t *testing.T) {
	tests := []struct {
		name         string
		body         []byte
		responseBody []byte
		deletedPath  string
	}{
		{
			name:         "reasoning_effort from message only",
			body:         []byte(`{"model":"gpt-5.5","reasoning_effort":"high"}`),
			responseBody: []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: reasoning_effort"}}`),
			deletedPath:  "reasoning_effort",
		},
		{
			name:         "reasoning summary via param",
			body:         []byte(`{"model":"gpt-5.3-codex-spark","reasoning":{"effort":"low","summary":"auto"}}`),
			responseBody: []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: 'reasoning.summary' is not supported with the 'gpt-5.3-codex-spark' model.","param":"reasoning.summary"}}`),
			deletedPath:  "reasoning.summary",
		},
		{
			name:         "background via message",
			body:         []byte(`{"model":"gpt-5.5","background":true}`),
			responseBody: []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: background"}}`),
			deletedPath:  "background",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, tt.responseBody, true)
			require.NoError(t, err)
			require.True(t, changed)
			require.False(t, gjson.GetBytes(retryBody, tt.deletedPath).Exists())
		})
	}
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyKeepsSiblingReasoningFields(t *testing.T) {
	body := []byte(`{"model":"gpt-5.3-codex-spark","reasoning":{"effort":"low","summary":"auto"}}`)
	responseBody := []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: 'reasoning.summary'.","param":"reasoning.summary"}}`)

	retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "low", gjson.GetBytes(retryBody, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(retryBody, "reasoning.summary").Exists())
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyHandlesItemReference(t *testing.T) {
	t.Run("deletes item_reference field", func(t *testing.T) {
		body := []byte(`{"input":[{"type":"message","role":"user","content":"hi"},{"type":"message","item_reference":"rs_123","role":"user","content":"again"}]}`)
		responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[1].item_reference'.","param":"input[1].item_reference"}}`)

		retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(retryBody, "input.1.item_reference").Exists())
		require.Equal(t, "again", gjson.GetBytes(retryBody, "input.1.content").String())
	})

	t.Run("removes item_reference typed item", func(t *testing.T) {
		body := []byte(`{"input":[{"type":"item_reference","id":"rs_123"},{"type":"message","role":"user","content":"hi"}]}`)
		responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].item_reference'."}}`)

		retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, int64(1), gjson.GetBytes(retryBody, "input.#").Int())
		require.Equal(t, "message", gjson.GetBytes(retryBody, "input.0.type").String())
	})
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyEmptiesContentOnMaxLengthZero(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","content":[{"type":"reasoning_text","text":"thought"}],"summary":[]}]}`)
	responseBody := []byte(`{"error":{"code":"invalid_value","message":"Invalid 'input[1].content': array too long. Expected an array with maximum length 0, but got an array with length 1 instead.","param":"input[1].content"}}`)

	retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, int64(0), gjson.GetBytes(retryBody, "input.1.content.#").Int())
	require.True(t, gjson.GetBytes(retryBody, "input.1.content").IsArray())
	require.Equal(t, "hi", gjson.GetBytes(retryBody, "input.0.content").String())
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyIgnoresNonZeroContentMaxLength(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","content":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}]}]}`)
	responseBody := []byte(`{"error":{"code":"invalid_value","message":"Invalid 'input[0].content': array too long. Expected an array with maximum length 1, but got an array with length 2 instead."}}`)

	retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, true)

	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, retryBody)
}

func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRestrictsLegacyBehaviorWhenRectifierDisabled(t *testing.T) {
	t.Run("legacy max_output_tokens still handled", func(t *testing.T) {
		body := []byte(`{"max_output_tokens":2048}`)
		responseBody := []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens."}}`)

		retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody, false)

		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(retryBody, "max_output_tokens").Exists())
	})

	t.Run("extended params ignored", func(t *testing.T) {
		tests := []struct {
			name         string
			body         []byte
			responseBody []byte
		}{
			{
				name:         "reasoning_effort",
				body:         []byte(`{"reasoning_effort":"high"}`),
				responseBody: []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: reasoning_effort"}}`),
			},
			{
				name:         "item_reference",
				body:         []byte(`{"input":[{"type":"item_reference","id":"rs_123"}]}`),
				responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].item_reference'."}}`),
			},
			{
				name:         "content too long",
				body:         []byte(`{"input":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"t"}]}]}`),
				responseBody: []byte(`{"error":{"code":"invalid_value","message":"Invalid 'input[0].content': array too long. Expected an array with maximum length 0, but got an array with length 1 instead."}}`),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				retryBody, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, tt.responseBody, false)
				require.NoError(t, err)
				require.False(t, changed)
				require.Nil(t, retryBody)
			})
		}
	})
}

func TestOpenAIGatewayService_APIKeyStripsAllIndexedNamespacesBeforeFirstForward(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"function_call","name":"first","namespace":"remove-first","arguments":"{}"},{"type":"custom_tool_call","name":"second","namespace":"remove-second","input":"{}"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestAccount(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 1)
	require.False(t, gjson.GetBytes(upstream.bodies[0], "input.0.namespace").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "input.1.namespace").Exists())
}

func TestOpenAIGatewayService_OpenAIHTTPStripsInputNamespacesBeforeFirstForward(t *testing.T) {
	accounts := []struct {
		name    string
		account *Account
	}{
		{name: "oauth", account: newOpenAIOAuthNamespaceTestAccount()},
		{name: "apikey", account: newOpenAIRejectedFieldTestAccount()},
	}
	for _, tt := range accounts {
		for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
			t.Run(tt.name+path, func(t *testing.T) {
				body := []byte(`{"model":"gpt-5.5","stream":false,"instructions":"test","input":[{"type":"message","role":"user","namespace":"remove","content":[{"type":"input_text","text":"hello","namespace":"nested-keep"}]}]}`)
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_namespace_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
				}}
				c := newOpenAIRejectedFieldTestContext(body)
				c.Request.URL.Path = path

				result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
					context.Background(),
					c,
					tt.account,
					body,
				)

				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 1, "namespace must be removed before the first upstream request")
				require.False(t, gjson.GetBytes(upstream.bodies[0], "input.0.namespace").Exists())
				require.Equal(t, "nested-keep", gjson.GetBytes(upstream.bodies[0], "input.0.content.0.namespace").String())
			})
		}
	}
}

func TestOpenAIGatewayService_RetriesExplicitMaxOutputTokensRejection(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":4096,"input":[{"type":"message","role":"user","content":{"max_output_tokens":"keep"}}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens","type":"invalid_request_error"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestAccount(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Equal(t, int64(4096), gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
	require.Equal(t, "keep", gjson.GetBytes(upstream.bodies[1], "input.0.content.max_output_tokens").String())
}

func TestOpenAIGatewayService_ComposesProactiveNamespaceStripWithRejectedFieldRetry(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","stream":false,"max_output_tokens":2048,"input":[{"type":"function_call","name":"first","namespace":"remove-first","arguments":"{}"},{"type":"custom_tool_call","name":"second","namespace":"remove-second","input":"{}"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"output":[],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`),
	}}

	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
		context.Background(),
		newOpenAIRejectedFieldTestContext(body),
		newOpenAIRejectedFieldTestAccount(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	for _, forwardedBody := range upstream.bodies {
		require.False(t, gjson.GetBytes(forwardedBody, "input.0.namespace").Exists())
		require.False(t, gjson.GetBytes(forwardedBody, "input.1.namespace").Exists())
	}
	require.Equal(t, int64(2048), gjson.GetBytes(upstream.bodies[0], "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
}

func newOpenAIRejectedFieldTestService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg: &config.Config{
			Security: config.SecurityConfig{
				URLAllowlist: config.URLAllowlistConfig{Enabled: false},
			},
			Gateway: config.GatewayConfig{OpenAIResponsesRectifierEnabled: true},
		},
		httpUpstream: upstream,
	}
}

func newOpenAIRejectedFieldTestContext(body []byte) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "curl/8.0")
	return c
}

func newOpenAIRejectedFieldTestAccount() *Account {
	return &Account{
		ID:          5107,
		Name:        "responses-compatible",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat.example",
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesMode:      string(openai_compat.ResponsesSupportModeAuto),
			openai_compat.ExtraKeyResponsesSupported: true,
		},
		Status:      StatusActive,
		Schedulable: true,
	}
}

func newOpenAIOAuthNamespaceTestAccount() *Account {
	return &Account{
		ID:          5108,
		Name:        "openai-oauth-namespace",
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
}

func newOpenAIRejectedFieldTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
