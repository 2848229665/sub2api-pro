package responseheaders

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestFilterHeadersDisabledUsesDefaultAllowlist(t *testing.T) {
	src := http.Header{}
	src.Add("Content-Type", "application/json")
	src.Add("X-Request-Id", "req-123")
	src.Add("X-Codex-Turn-State", "turn-state-123")
	src.Add("X-Codex-Primary-Reset-At", "1777283883")
	src.Add("X-Codex-Credits-Balance", "12.50")
	src.Add("X-Codex-Other-Limit-Name", "gpt-5.6-sol")
	src.Add("X-Models-Etag", `"models-v2"`)
	src.Add("OpenAI-Model", "gpt-5.6-sol")
	src.Add("X-Reasoning-Included", "true")
	src.Add("X-Codex-Safety-Buffering-Enabled", "true")
	src.Add("X-Codex-Safety-Buffering-Faster-Model", "gpt-5.6-mini")
	src.Add("X-OpenAI-Authorization-Error", "workspace_required")
	src.Add("X-Test", "ok")
	src.Add("Connection", "keep-alive")
	src.Add("Content-Length", "123")

	cfg := config.ResponseHeaderConfig{
		Enabled:     false,
		ForceRemove: []string{"x-request-id"},
	}

	filtered := FilterHeaders(src, CompileHeaderFilter(cfg))
	if filtered.Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type passthrough, got %q", filtered.Get("Content-Type"))
	}
	if filtered.Get("X-Request-Id") != "req-123" {
		t.Fatalf("expected X-Request-Id allowed, got %q", filtered.Get("X-Request-Id"))
	}
	if filtered.Get("X-Codex-Turn-State") != "turn-state-123" {
		t.Fatalf("expected X-Codex-Turn-State allowed, got %q", filtered.Get("X-Codex-Turn-State"))
	}
	for key, want := range map[string]string{
		"X-Codex-Primary-Reset-At":              "1777283883",
		"X-Codex-Credits-Balance":               "12.50",
		"X-Codex-Other-Limit-Name":              "gpt-5.6-sol",
		"X-Models-Etag":                         `"models-v2"`,
		"OpenAI-Model":                          "gpt-5.6-sol",
		"X-Reasoning-Included":                  "true",
		"X-Codex-Safety-Buffering-Enabled":      "true",
		"X-Codex-Safety-Buffering-Faster-Model": "gpt-5.6-mini",
		"X-OpenAI-Authorization-Error":          "workspace_required",
	} {
		if got := filtered.Get(key); got != want {
			t.Fatalf("expected %s allowed with %q, got %q", key, want, got)
		}
	}
	if filtered.Get("X-Test") != "" {
		t.Fatalf("expected X-Test removed, got %q", filtered.Get("X-Test"))
	}
	if filtered.Get("Connection") != "" {
		t.Fatalf("expected Connection to be removed, got %q", filtered.Get("Connection"))
	}
	if filtered.Get("Content-Length") != "" {
		t.Fatalf("expected Content-Length to be removed, got %q", filtered.Get("Content-Length"))
	}
}

func TestFilterHeadersForceRemoveCanBlockDynamicCodexHeader(t *testing.T) {
	src := http.Header{
		"X-Codex-Other-Primary-Reset-At": []string{"1777283883"},
	}
	filtered := FilterHeaders(src, CompileHeaderFilter(config.ResponseHeaderConfig{
		Enabled:     true,
		ForceRemove: []string{"x-codex-other-primary-reset-at"},
	}))
	if got := filtered.Get("X-Codex-Other-Primary-Reset-At"); got != "" {
		t.Fatalf("expected forced Codex header removal, got %q", got)
	}
}

func TestFilterHeadersEnabledUsesAllowlist(t *testing.T) {
	src := http.Header{}
	src.Add("Content-Type", "application/json")
	src.Add("X-Extra", "ok")
	src.Add("X-Remove", "nope")
	src.Add("X-Blocked", "nope")

	cfg := config.ResponseHeaderConfig{
		Enabled:           true,
		AdditionalAllowed: []string{"x-extra"},
		ForceRemove:       []string{"x-remove"},
	}

	filtered := FilterHeaders(src, CompileHeaderFilter(cfg))
	if filtered.Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type allowed, got %q", filtered.Get("Content-Type"))
	}
	if filtered.Get("X-Extra") != "ok" {
		t.Fatalf("expected X-Extra allowed, got %q", filtered.Get("X-Extra"))
	}
	if filtered.Get("X-Remove") != "" {
		t.Fatalf("expected X-Remove removed, got %q", filtered.Get("X-Remove"))
	}
	if filtered.Get("X-Blocked") != "" {
		t.Fatalf("expected X-Blocked removed, got %q", filtered.Get("X-Blocked"))
	}
}
