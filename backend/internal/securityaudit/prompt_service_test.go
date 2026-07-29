package securityaudit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type staticSettingRepository struct {
	values map[string]string
}

func (r staticSettingRepository) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (r staticSettingRepository) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}
func (r staticSettingRepository) Set(context.Context, string, string) error { return nil }
func (r staticSettingRepository) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}
func (r staticSettingRepository) SetMultiple(context.Context, map[string]string) error { return nil }
func (r staticSettingRepository) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r staticSettingRepository) Delete(context.Context, string) error { return nil }

type structuredCaptureScanner struct {
	chunks []PromptScanChunk
}

func (s *structuredCaptureScanner) Scan(_ context.Context, _ ActiveEndpoint, text string, _ []string) (*NormalizedResult, error) {
	return s.capture(PromptScanChunk{Text: text})
}

func (s *structuredCaptureScanner) ScanStructured(_ context.Context, _ ActiveEndpoint, chunk PromptScanChunk, _ []string) (*NormalizedResult, error) {
	return s.capture(chunk)
}

func (s *structuredCaptureScanner) capture(chunk PromptScanChunk) (*NormalizedResult, error) {
	s.chunks = append(s.chunks, chunk)
	return &NormalizedResult{
		Decision: EventPass, RiskLevel: RiskLow, Action: ActionAllow,
		ScannerScores: map[string]float64{}, ScannerEvidence: map[string]string{},
	}, nil
}

func TestPromptServiceHasExplicitIdempotentLifecycle(t *testing.T) {
	config := NewConfigManager(nil, staticSettingRepository{values: map[string]string{
		SettingKeyPromptAuditConfig: "",
		SettingKeyRiskControl:       "false",
	}}, nil, prefixEncryptor{}, testTotpKeyConfig())
	service := NewPromptService(
		config,
		NewPostgreSQLRepository(nil),
		NewRedisPayloadStore(nil),
		NewOpenAICompatibleScanner(),
		NewAtomicMetrics(),
	)

	require.Nil(t, service.cancel, "construction must not start background work")
	require.NoError(t, service.Start(context.Background()))
	require.NotNil(t, service.cancel)
	require.NoError(t, service.Start(context.Background()), "Start must be idempotent")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, service.Shutdown(ctx))
	require.Nil(t, service.cancel)
	require.NoError(t, service.Shutdown(ctx), "Shutdown must be idempotent")
}

func TestPromptServiceStartReportsDependencyFailureWithoutPanic(t *testing.T) {
	service := &PromptService{}
	require.Error(t, service.Start(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, service.Shutdown(ctx))
}

func TestPromptServiceGPTOSSSafeguardExcludesToolOutputAndAttachments(t *testing.T) {
	cfg := ActiveConfig{
		RiskControlEnabled: true,
		Enabled:            true,
		BlockingEnabled:    true,
		AllGroups:          true,
		ConfigVersion:      8,
		Scanners:           []string{"jailbreak"},
		Endpoints: []ActiveEndpoint{{
			ID: "groq-safeguard", Enabled: true, Protocol: EndpointProtocolGroqSafeguard,
			Model: DefaultGroqSafeguardModel, TimeoutMS: 1000, InputLimit: 1000,
		}},
	}
	scanner := &structuredCaptureScanner{}
	service := &PromptService{
		config:    &fakeConfigStore{cfg: cfg, active: true},
		evaluator: newGuardEvaluator(scanner, nil, NewAtomicMetrics(), 2, 2),
	}

	decision, err := service.Evaluate(context.Background(), Request{
		Protocol: "openai_responses",
		Body: []byte(`{
			"instructions":"INSTRUCTIONS_CANARY",
			"input":[
				{"type":"message","role":"system","content":[{"type":"input_text","text":"SYSTEM_CANARY"}]},
				{"type":"message","role":"developer","content":[{"type":"input_text","text":"DEVELOPER_CANARY"}]},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ASSISTANT_CANARY"}]},
				{"type":"function_call_output","call_id":"call_1","output":"TOOL_OUTPUT_CANARY"},
				{"type":"message","role":"user","content":[
					{"type":"input_text","text":"user text"},
					{"type":"input_image","image_url":"data:image/png;base64,IMAGE_CANARY"},
					{"type":"input_file","filename":"FILE_CANARY.txt","file_data":"FILE_DATA_CANARY"}
				]}
			]
		}`),
	})

	require.NoError(t, err)
	require.Equal(t, DecisionAllow, decision.Kind)
	require.Len(t, scanner.chunks, 1)
	require.Equal(t, []PromptAuditMessage{
		{Role: "developer", Content: "INSTRUCTIONS_CANARY"},
		{Role: "system", Content: "SYSTEM_CANARY"},
		{Role: "developer", Content: "DEVELOPER_CANARY"},
		{Role: "assistant", Content: "ASSISTANT_CANARY"},
		{Role: "user", Content: "user text"},
	}, scanner.chunks[0].Messages)
	joined := flattenPromptAuditMessages(scanner.chunks[0].Messages)
	for _, excluded := range []string{"TOOL_OUTPUT_CANARY", "IMAGE_CANARY", "FILE_CANARY", "FILE_DATA_CANARY"} {
		require.NotContains(t, joined, excluded)
	}
}

func TestPromptServiceRejectsInvalidDeleteConfirmationClaims(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	filter := EventFilter{Decision: string(EventCritical), StartAt: &start, EndAt: &end}
	const snapshotMaxID int64 = 10
	filterHash := FilterHash(filter, snapshotMaxID)
	validClaims := deleteClaims{
		FilterHash: filterHash, SnapshotMaxID: snapshotMaxID, AdminID: 7,
		IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}
	claimsToken := func(claims deleteClaims) string {
		raw, err := json.Marshal(claims)
		require.NoError(t, err)
		return string(raw)
	}
	validRequest := DeleteByFilterRequest{
		Filter: filter, SnapshotMaxID: snapshotMaxID, FilterHash: filterHash,
		ConfirmationToken: claimsToken(validClaims), Confirm: true,
	}

	tests := []struct {
		name    string
		request DeleteByFilterRequest
		adminID int64
	}{
		{name: "confirm false", request: func() DeleteByFilterRequest { value := validRequest; value.Confirm = false; return value }(), adminID: 7},
		{name: "malformed token", request: func() DeleteByFilterRequest {
			value := validRequest
			value.ConfirmationToken = "not-json"
			return value
		}(), adminID: 7},
		{name: "different administrator", request: validRequest, adminID: 8},
		{name: "filter hash mismatch", request: func() DeleteByFilterRequest {
			value := validRequest
			value.FilterHash = strings.Repeat("b", 64)
			return value
		}(), adminID: 7},
		{name: "snapshot mismatch", request: func() DeleteByFilterRequest { value := validRequest; value.SnapshotMaxID++; return value }(), adminID: 7},
		{name: "expired", request: func() DeleteByFilterRequest {
			value := validRequest
			claims := validClaims
			claims.ExpiresAt = now
			value.ConfirmationToken = claimsToken(claims)
			return value
		}(), adminID: 7},
	}

	service := &PromptService{config: &fakeConfigStore{}, clock: fixedClock{now: now}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.DeleteByFilter(context.Background(), test.request, test.adminID)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
}
