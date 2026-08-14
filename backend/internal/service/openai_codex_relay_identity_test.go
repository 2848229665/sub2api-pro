package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAICodexRelayIdentityMatchesReferenceVector(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("api_key", &APIKey{ID: 17, Key: "sk-downstream-1"})
	account := &Account{ID: 800, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	identity, ok := newOpenAICodexRelayIdentity(c, account)
	require.True(t, ok)

	got := identity.pseudonymize("session_id", "session-http")
	require.Equal(t, "753921d3-c579-5816-909a-32ec8d6f1539", got)
	parsed, err := uuid.Parse(got)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(5), parsed.Version())
	require.Equal(t, uuid.RFC4122, parsed.Variant())
}

func TestOpenAICodexRelaySessionSeedUsesProtocolPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := []byte(`{"prompt_cache_key":"body-session"}`)

	require.Equal(t, "body-session", openAICodexRelaySessionSeed(c, body))
	c.Request.Header.Set("conversation_id", "conversation-session")
	require.Equal(t, "conversation-session", openAICodexRelaySessionSeed(c, body))
	c.Request.Header.Set("session_id", "legacy-session")
	require.Equal(t, "legacy-session", openAICodexRelaySessionSeed(c, body))
	c.Request.Header.Set(openAIOfficialThreadIDHeader, "thread-session")
	require.Equal(t, "thread-session", openAICodexRelaySessionSeed(c, body))
	c.Request.Header.Set(openAIOfficialSessionIDHeader, "official-session")
	require.Equal(t, "official-session", openAICodexRelaySessionSeed(c, body))
}

func TestOpenAICodexRelayIdentityIsStableAndScoped(t *testing.T) {
	base := openAICodexRelayIdentity{apiKeyHash: "api-key-hash", accountID: "41"}
	require.Equal(
		t,
		base.pseudonymize("session_id", "client-id"),
		base.pseudonymize("session_id", "client-id"),
	)
	require.NotEqual(
		t,
		base.pseudonymize("session_id", "client-id"),
		base.pseudonymize("thread_id", "client-id"),
	)
	require.NotEqual(
		t,
		base.pseudonymize("session_id", "client-id"),
		(openAICodexRelayIdentity{apiKeyHash: "api-key-hash", accountID: "42"}).pseudonymize("session_id", "client-id"),
	)
	require.NotEqual(
		t,
		base.pseudonymize("session_id", "client-id"),
		(openAICodexRelayIdentity{apiKeyHash: "other-api-key-hash", accountID: "41"}).pseudonymize("session_id", "client-id"),
	)
}

func TestOpenAICodexRelayIdentityUsesConfiguredDevice(t *testing.T) {
	derived := openAICodexRelayIdentity{apiKeyHash: "api-key-hash", accountID: "41"}
	require.Equal(
		t,
		derived.pseudonymize("installation_id", "client-device"),
		derived.installationID("client-device"),
	)
	require.Empty(t, derived.installationID(""))

	fixed := openAICodexRelayIdentity{apiKeyHash: "api-key-hash", accountID: "41", deviceID: "device-41"}
	require.Equal(t, "device-41", fixed.installationID("client-device"))
	require.Equal(t, "device-41", fixed.installationID(""))
}

func TestOpenAICodexRelayIdentityRewritesTurnMetadata(t *testing.T) {
	identity := openAICodexRelayIdentity{apiKeyHash: "api-key-hash", accountID: "41", deviceID: "device-41"}
	raw := `{"session_id":"raw-session","thread_id":"raw-thread","turn_id":"raw-turn","root_turn_id":"raw-turn","window_id":"raw-window","parent_thread_id":"raw-parent-thread","parent_turn_id":"raw-parent-turn","workspaces":{"/private/work":{"associated_remote_urls":{"origin":"ssh://secret"}}}}`

	rewritten, err := identity.rewriteTurnMetadata(raw)
	require.NoError(t, err)
	require.Equal(t, "device-41", gjson.Get(rewritten, "installation_id").String())
	require.Equal(t, identity.pseudonymize("session_id", "raw-session"), gjson.Get(rewritten, "session_id").String())
	require.Equal(t, identity.pseudonymize("thread_id", "raw-thread"), gjson.Get(rewritten, "thread_id").String())
	require.Equal(t, identity.pseudonymize("turn_id", "raw-turn"), gjson.Get(rewritten, "turn_id").String())
	require.Equal(t, identity.pseudonymize("turn_id", "raw-turn"), gjson.Get(rewritten, "root_turn_id").String())
	require.Equal(t, identity.pseudonymize("window_id", "raw-window"), gjson.Get(rewritten, "window_id").String())
	require.Equal(t, identity.pseudonymize("thread_id", "raw-parent-thread"), gjson.Get(rewritten, "parent_thread_id").String())
	require.Equal(t, identity.pseudonymize("turn_id", "raw-parent-turn"), gjson.Get(rewritten, "parent_turn_id").String())
	require.False(t, gjson.Get(rewritten, "workspaces").Exists())
}

func TestPrepareOpenAICodexUpstreamIdentityIgnoresLegacyFingerprintMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prepare := func(mode string) ([]byte, openAICodexUpstreamIdentity) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set(openAIOfficialSessionIDHeader, "raw-session")
		c.Set("api_key", &APIKey{ID: 17, Key: "sk-downstream-1"})
		account := &Account{
			ID:       800,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Extra:    map[string]any{codexFingerprintModeExtraKey: mode},
		}
		body := []byte(`{"model":"gpt-5.5","prompt_cache_key":"raw-session","client_metadata":{"session_id":"raw-session"}}`)
		transformed, identity, err := prepareOpenAICodexUpstreamIdentity(c, account, body, false)
		require.NoError(t, err)
		return transformed, identity
	}

	offBody, offIdentity := prepare("off")
	fullBody, fullIdentity := prepare("full")
	require.Equal(t, offBody, fullBody)
	require.Equal(t, offIdentity, fullIdentity)
}

func TestPrepareOpenAICodexUpstreamIdentityRejectsInvalidMetadataTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "null client metadata", body: `{"model":"gpt-5.5","client_metadata":null}`, wantErr: "client_metadata must be an object"},
		{name: "non-string session", body: `{"model":"gpt-5.5","client_metadata":{"session_id":42}}`, wantErr: "client_metadata.session_id must be a string"},
		{name: "invalid turn metadata", body: `{"model":"gpt-5.5","client_metadata":{"x-codex-turn-metadata":""}}`, wantErr: "x-codex-turn-metadata must be valid JSON"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Set("api_key", &APIKey{ID: 17, Key: "sk-downstream-1"})
			_, _, err := prepareOpenAICodexUpstreamIdentity(
				c,
				&Account{ID: 800, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
				[]byte(testCase.body),
				false,
			)
			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

func TestPrepareOpenAICodexUpstreamIdentitySkipsMissingAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	body := []byte(`{"model":"gpt-5.5","prompt_cache_key":"raw-session"}`)
	transformed, identity, err := prepareOpenAICodexUpstreamIdentity(
		c,
		&Account{ID: 800, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		body,
		false,
	)
	require.NoError(t, err)
	require.Equal(t, body, transformed)
	require.Equal(t, openAICodexUpstreamIdentity{}, identity)
}
