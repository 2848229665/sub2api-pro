package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type openAIAccountWaitCacheStub struct {
	helperConcurrencyCacheStub
	accountWaitAllowed        bool
	accountWaitIncrementCalls int
	accountWaitDecrementCalls int
}

func (s *openAIAccountWaitCacheStub) IncrementAccountWaitCount(_ context.Context, _ int64, _ int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountWaitIncrementCalls++
	return s.accountWaitAllowed, nil
}

func (s *openAIAccountWaitCacheStub) DecrementAccountWaitCount(_ context.Context, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountWaitDecrementCalls++
	return nil
}

func TestAcquireResponsesAccountSlotSharesReleaseGuardWithSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx)

	gatewayService := &service.OpenAIGatewayService{}
	h := &OpenAIGatewayHandler{gatewayService: gatewayService}
	releaseCount := 0
	selection := &service.AccountSelectionResult{
		Account: &service.Account{
			ID:       31,
			Platform: service.PlatformGrok,
		},
		Acquired: true,
		ReleaseFunc: func() {
			releaseCount++
		},
	}
	streamStarted := false

	release, slotResult, _ := h.acquireResponsesAccountSlot(
		c,
		nil,
		"",
		selection,
		false,
		false,
		&streamStarted,
		zap.NewNop(),
	)

	require.Equal(t, openAISlotAcquireOK, slotResult)
	require.NotNil(t, release)
	require.NoError(t, gatewayService.RejectAcquiredOpenAISelection(ctx, nil, "", selection))
	require.Equal(t, 1, releaseCount)

	release()
	cancel()
	require.Equal(t, 1, releaseCount)
}

func TestAcquireResponsesAccountSlotReturnsPoolModeSessionHash(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
	selection := &service.AccountSelectionResult{
		Account: &service.Account{
			ID:       32,
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeAPIKey,
			Credentials: map[string]any{
				"pool_mode": true,
			},
		},
		Acquired:    true,
		ReleaseFunc: func() {},
	}
	streamStarted := false

	release, slotResult, sessionHash := h.acquireResponsesAccountSlot(
		c,
		nil,
		"",
		selection,
		true,
		false,
		&streamStarted,
		zap.NewNop(),
	)

	require.Equal(t, openAISlotAcquireOK, slotResult)
	require.NotNil(t, release)
	require.NotEmpty(t, sessionHash)
	require.Contains(t, sessionHash, "openai-pool-retry-")
	release()
}

func TestAcquireResponsesAccountSlotLogsAccountWaitQueueFull(t *testing.T) {
	cache := &openAIAccountWaitCacheStub{
		helperConcurrencyCacheStub: helperConcurrencyCacheStub{
			accountSeq:         []bool{false},
			accountLoadCurrent: 2,
			accountLoadWaiting: 100,
		},
		accountWaitAllowed: false,
	}
	h := &OpenAIGatewayHandler{
		gatewayService:    &service.OpenAIGatewayService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second),
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	selection := openAIAccountWaitTestSelection()
	streamStarted := false
	core, observedLogs := observer.New(zap.InfoLevel)

	release, slotResult, _ := h.acquireResponsesAccountSlot(
		c,
		nil,
		"",
		selection,
		false,
		false,
		&streamStarted,
		zap.New(core),
	)

	require.Nil(t, release)
	require.Equal(t, openAISlotAcquireFailed, slotResult)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	queueLogs := observedLogs.FilterMessage("openai.account_wait_queue_full").All()
	require.Len(t, queueLogs, 1)
	fields := queueLogs[0].ContextMap()
	require.EqualValues(t, 68, fields["account_id"])
	require.EqualValues(t, 10, fields["account_priority"])
	require.EqualValues(t, 2, fields["effective_concurrency_limit"])
	require.EqualValues(t, 100, fields["max_waiting"])
	require.EqualValues(t, 2, fields["active_count"])
	require.EqualValues(t, 100, fields["waiting_count"])
	require.Equal(t, "all_general_candidates_busy", fields["wait_reason"])
	require.EqualValues(t, 2, fields["wait_candidate_count"])
	require.Equal(t, true, fields["terminal_event"])
	require.EqualValues(t, http.StatusTooManyRequests, fields["mapped_status_code"])
	require.Equal(t, 1, cache.accountWaitIncrementCalls)
	require.Equal(t, 0, cache.accountWaitDecrementCalls)
}

func TestAcquireResponsesAccountSlotLogsAccountWaitLifecycle(t *testing.T) {
	cache := &openAIAccountWaitCacheStub{
		helperConcurrencyCacheStub: helperConcurrencyCacheStub{
			accountSeq:         []bool{false, false, true},
			accountLoadCurrent: 2,
			accountLoadWaiting: 1,
		},
		accountWaitAllowed: true,
	}
	h := &OpenAIGatewayHandler{
		gatewayService:    &service.OpenAIGatewayService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second),
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	selection := openAIAccountWaitTestSelection()
	selection.WaitPlan.Timeout = time.Second
	streamStarted := false
	core, observedLogs := observer.New(zap.InfoLevel)

	release, slotResult, _ := h.acquireResponsesAccountSlot(
		c,
		nil,
		"",
		selection,
		false,
		false,
		&streamStarted,
		zap.New(core),
	)

	require.Equal(t, openAISlotAcquireOK, slotResult)
	require.NotNil(t, release)
	release()
	startedLogs := observedLogs.FilterMessage("openai.account_wait_started").All()
	require.Len(t, startedLogs, 1)
	require.Equal(t, true, startedLogs[0].ContextMap()["queue_counted"])
	acquiredLogs := observedLogs.FilterMessage("openai.account_wait_acquired").All()
	require.Len(t, acquiredLogs, 1)
	fields := acquiredLogs[0].ContextMap()
	require.Equal(t, "acquired", fields["phase"])
	waitMs, ok := fields["wait_ms"].(int64)
	require.True(t, ok)
	require.GreaterOrEqual(t, waitMs, int64(100))
	require.EqualValues(t, 2, fields["wait_candidate_count"])
	require.Equal(t, 1, cache.accountWaitIncrementCalls)
	require.Equal(t, 1, cache.accountWaitDecrementCalls)
}

func openAIAccountWaitTestSelection() *service.AccountSelectionResult {
	return &service.AccountSelectionResult{
		Account: &service.Account{
			ID:          68,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Priority:    10,
			Concurrency: 3,
		},
		WaitPlan: &service.AccountWaitPlan{
			AccountID:      68,
			MaxConcurrency: 2,
			Timeout:        30 * time.Second,
			MaxWaiting:     100,
			Reason:         "all_general_candidates_busy",
			Candidates: []service.AccountWaitCandidateDiagnostic{
				{AccountID: 68, Priority: 10, GeneralLimit: 2, HardLimit: 3, Result: "selected_for_wait"},
				{AccountID: 90, Priority: 40, GeneralLimit: 2, HardLimit: 3, Result: "busy"},
			},
		},
	}
}
