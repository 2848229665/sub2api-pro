package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type liveHTTPUpstreamStub struct {
	request *http.Request
	body    []byte
}

type liveAttestationStub struct {
	header string
	err    error
}

func (s liveAttestationStub) Check(context.Context) error {
	return s.err
}

func (s liveAttestationStub) Generate(context.Context) (string, error) {
	return s.header, s.err
}

func (s *liveHTTPUpstreamStub) Do(
	request *http.Request,
	_ string,
	_ int64,
	_ int,
) (*http.Response, error) {
	s.request = request
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	s.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Location": {"/backend-api/codex/call_test"},
		},
		Body: io.NopCloser(strings.NewReader("v=0\r\n")),
	}, nil
}

func (s *liveHTTPUpstreamStub) DoWithTLS(
	request *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return s.Do(request, proxyURL, accountID, accountConcurrency)
}

func TestLiveCapabilityOnlyAllowsOpenAIOAuth(t *testing.T) {
	require.True(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{Platform: PlatformGrok, Type: AccountTypeOAuth}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			openAIAuthModeCredentialKey: OpenAIAuthModePersonalAccessToken,
		},
	}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			openAIAuthModeCredentialKey: OpenAIAuthModeAgentIdentity,
		},
	}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
}

func TestValidateLiveCallRequestDoesNotRequireDelegation(t *testing.T) {
	request := &LiveCallRequest{
		SDP:     "v=0\r\n",
		Session: json.RawMessage(`{"model":"gpt-live-test","instructions":"hello"}`),
	}
	require.NoError(t, ValidateLiveCallRequest(request))
	require.NotContains(t, string(request.Session), "delegation")
}

func TestCreateUpstreamLiveCallPreservesSession(t *testing.T) {
	upstream := &liveHTTPUpstreamStub{}
	service := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          7,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 2,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acct_test",
		},
	}
	session := json.RawMessage(`{
		"model":"gpt-live-test",
		"delegation":{"type":"client"},
		"custom":{"keep":true}
	}`)

	created, err := service.createUpstreamLiveCall(context.Background(), account, &LiveCallRequest{
		SDP:     "v=offer\r\n",
		Session: session,
	}, `{"v":1,"s":0,"t":"v1.test"}`, liveUpstreamIdentity{
		OpenAIAlpha:     "quicksilver=v2",
		RealtimeSession: "realtime-session",
		SessionID:       "session-id",
		ThreadID:        "thread-id",
		ClientRequestID: "client-request-id",
		ParentThreadID:  "parent-thread-id",
		WindowID:        "window-id",
		InstallationID:  "installation-id",
		TurnMetadata:    `{"turn_id":"turn-id"}`,
	})
	require.NoError(t, err)
	require.Equal(t, "call_test", created.CallID)
	require.Equal(t, []byte("v=0\r\n"), created.SDP)

	var forwarded struct {
		SDP     string          `json:"sdp"`
		Session json.RawMessage `json:"session"`
	}
	require.NoError(t, json.Unmarshal(upstream.body, &forwarded))
	require.Equal(t, "v=offer\r\n", forwarded.SDP)
	require.JSONEq(t, string(session), string(forwarded.Session))
	require.Equal(t, "Bearer test-access-token", upstream.request.Header.Get("Authorization"))
	require.Equal(t, "acct_test", upstream.request.Header.Get("Chatgpt-Account-Id"))
	require.Equal(t, "quicksilver=v2", upstream.request.Header.Get("OpenAI-Alpha"))
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, upstream.request.Header.Get(liveAttestationHeader))
	require.Equal(t, "realtime-session", upstream.request.Header.Get("X-Session-Id"))
	require.Equal(t, "session-id", upstream.request.Header.Get("Session-Id"))
	require.Empty(t, upstream.request.Header.Get("session_id"))
	require.Empty(t, upstream.request.Header.Get("conversation_id"))
	require.Equal(t, "thread-id", upstream.request.Header.Get("Thread-Id"))
	require.Equal(t, "client-request-id", upstream.request.Header.Get("X-Client-Request-Id"))
	require.Equal(t, "parent-thread-id", upstream.request.Header.Get("X-Codex-Parent-Thread-Id"))
	require.Equal(t, "window-id", upstream.request.Header.Get("X-Codex-Window-Id"))
	require.Equal(t, "installation-id", upstream.request.Header.Get("X-Codex-Installation-Id"))
	require.JSONEq(t, `{"turn_id":"turn-id"}`, upstream.request.Header.Get("X-Codex-Turn-Metadata"))
	require.Empty(t, upstream.request.Header.Get("OpenAI-Beta"))
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.request.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.request.Context()))
}

func TestResolveLiveUpstreamIdentityIsStableAndPassesThroughSession(t *testing.T) {
	input := LiveCallIdentity{
		APIKeyID:        77,
		OpenAIAlpha:     "quicksilver=v1",
		RealtimeSession: "realtime-session",
		ThreadID:        "thread",
	}
	first := resolveLiveUpstreamIdentity(input)
	second := resolveLiveUpstreamIdentity(input)

	require.Equal(t, "quicksilver=v1", first.OpenAIAlpha)
	require.Equal(t, first, second)
	// 账号尚未选定时只生成稳定原始值，不提前绑定账号作用域。
	require.Equal(t, "realtime-session", first.RealtimeSession)
	require.Equal(t, first.RealtimeSession, first.SessionID)
	require.Equal(t, "thread", first.ThreadID)
}

func TestScopeLiveUpstreamIdentityUsesRelayIdentity(t *testing.T) {
	apiKey := &APIKey{ID: 77, Key: "sk-live-key"}
	account := &Account{
		ID:       8077,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"openai_device_id": "live-device"},
	}
	raw := liveUpstreamIdentity{
		RealtimeSession: "realtime-session",
		SessionID:       "session-id",
		ThreadID:        "thread-id",
		ClientRequestID: "client-request-id",
		ParentThreadID:  "parent-thread-id",
		WindowID:        "window-id",
		InstallationID:  "client-device",
		TurnMetadata:    `{"installation_id":"metadata-device","thread_id":"metadata-thread","turn_id":"metadata-turn","root_turn_id":"metadata-turn","workspaces":{"/private/project":{}}}`,
	}

	scoped, err := scopeLiveUpstreamIdentity(raw, apiKey, account)
	require.NoError(t, err)
	relayIdentity, ok := newOpenAICodexRelayIdentityForAPIKey(apiKey, account)
	require.True(t, ok)
	require.Equal(t, "realtime-session", scoped.RealtimeSession)
	require.Equal(t, relayIdentity.pseudonymize("session_id", "session-id"), scoped.SessionID)
	require.Equal(t, relayIdentity.pseudonymize("thread_id", "thread-id"), scoped.ThreadID)
	require.Equal(t, relayIdentity.pseudonymize("thread_id", "client-request-id"), scoped.ClientRequestID)
	require.Equal(t, relayIdentity.pseudonymize("thread_id", "parent-thread-id"), scoped.ParentThreadID)
	require.Equal(t, relayIdentity.pseudonymize("window_id", "window-id"), scoped.WindowID)
	require.Equal(t, "live-device", scoped.InstallationID)
	require.Equal(t, "live-device", gjson.Get(scoped.TurnMetadata, "installation_id").String())
	require.Equal(t, relayIdentity.pseudonymize("thread_id", "metadata-thread"), gjson.Get(scoped.TurnMetadata, "thread_id").String())
	require.Equal(t, relayIdentity.pseudonymize("turn_id", "metadata-turn"), gjson.Get(scoped.TurnMetadata, "turn_id").String())
	require.Equal(t, relayIdentity.pseudonymize("turn_id", "metadata-turn"), gjson.Get(scoped.TurnMetadata, "root_turn_id").String())
	require.False(t, gjson.Get(scoped.TurnMetadata, "workspaces").Exists())
}

func TestLiveUpstreamIdentityFromLegacyRecordUsesStableFallback(t *testing.T) {
	record := &LiveCallRecord{
		CallID:   "legacy-call",
		CallHash: hashLiveCallID("legacy-call"),
		APIKeyID: 79,
	}

	first := liveUpstreamIdentityFromRecord(record)
	second := liveUpstreamIdentityFromRecord(record)
	otherAPIKey := liveUpstreamIdentityFromRecord(&LiveCallRecord{
		CallID:   record.CallID,
		CallHash: record.CallHash,
		APIKeyID: 80,
	})

	require.Equal(t, first, second)
	require.Equal(t, "quicksilver=v2", first.OpenAIAlpha)
	require.NotEmpty(t, first.SessionID)
	require.Equal(t, first.SessionID, first.RealtimeSession)
	require.NotEmpty(t, first.ThreadID)
	require.NotEqual(t, first.SessionID, first.ThreadID)
	// 按 API Key 的会话隔离已移除：legacy fallback 只按 call 派生，与 apiKey 无关。
	require.Equal(t, first.SessionID, otherAPIKey.SessionID)
	require.Equal(t, first.ThreadID, otherAPIKey.ThreadID)
}

func TestLiveAttestationCipherRoundTripAndRejectsOtherInstanceKey(t *testing.T) {
	first := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "first-live-secret"},
	})
	second := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "second-live-secret"},
	})
	require.NotNil(t, first)
	require.NotNil(t, second)

	ciphertext, err := first.Encrypt(`{"v":1,"s":0,"t":"v1.opaque"}`)
	require.NoError(t, err)
	require.NotContains(t, ciphertext, "opaque")

	plaintext, err := first.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.opaque"}`, plaintext)

	_, err = second.Decrypt(ciphertext)
	require.Error(t, err)
}

func TestPrepareLiveAttestationEncryptsHeaderAndReturnsExplicitProviderError(t *testing.T) {
	cipher := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "live-attestation-test-secret"},
	})
	service := &OpenAIGatewayService{
		liveAttestation:       liveAttestationStub{header: `{"v":1,"s":0,"t":"v1.test"}`},
		liveAttestationCipher: cipher,
	}
	header, ciphertext, err := service.prepareLiveAttestation(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, header)
	require.NotContains(t, ciphertext, "v1.test")
	decrypted, err := cipher.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, header, decrypted)

	service.liveAttestation = liveAttestationStub{err: errors.New("macOS app missing")}
	_, _, err = service.prepareLiveAttestation(context.Background())
	var unavailable *LiveAttestationUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Contains(t, unavailable.Error(), "macOS app missing")
}

func TestLiveMaxSessionDurationDefaultsAndOverrides(t *testing.T) {
	require.Equal(t, defaultLiveMaxSessionDuration, (&OpenAIGatewayService{}).liveMaxSessionDuration())
	require.Equal(
		t,
		90*time.Second,
		(&OpenAIGatewayService{cfg: &config.Config{
			Gateway: config.GatewayConfig{
				Live: config.GatewayLiveConfig{MaxSessionDurationSeconds: 90},
			},
		}}).liveMaxSessionDuration(),
	)
}

func TestLiveSidebandNormalCloseEndsCall(t *testing.T) {
	normalClose := coderws.CloseError{Code: coderws.StatusNormalClosure}
	require.ErrorIs(t, liveSidebandReadError(normalClose), ErrLiveCallNotFound)

	abnormalClose := coderws.CloseError{Code: coderws.StatusInternalError}
	require.Equal(t, abnormalClose, liveSidebandReadError(abnormalClose))
}

func TestLiveCreateFailoverUsesExistingOpenAIPolicy(t *testing.T) {
	service := &OpenAIGatewayService{}
	require.False(t, service.shouldFailoverLiveCreateError(&UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"message":"invalid session"}}`),
	}))
	require.True(t, service.shouldFailoverLiveCreateError(&UpstreamFailoverError{
		StatusCode: http.StatusForbidden,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(&UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(errors.New("transport failed")))
}

func TestLiveCallIDFromLocation(t *testing.T) {
	callID, err := liveCallIDFromLocation("https://chatgpt.com/backend-api/codex/call_123?intent=quicksilver")
	require.NoError(t, err)
	require.Equal(t, "call_123", callID)

	callID, err = liveCallIDFromLocation("/backend-api/codex/call_456")
	require.NoError(t, err)
	require.Equal(t, "call_456", callID)
}

func TestRequestTypeLive(t *testing.T) {
	require.True(t, RequestTypeLive.IsValid())
	require.Equal(t, "live", RequestTypeLive.String())
	parsed, err := ParseUsageRequestType("live")
	require.NoError(t, err)
	require.Equal(t, RequestTypeLive, parsed)
}
