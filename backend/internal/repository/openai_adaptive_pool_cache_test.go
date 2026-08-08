package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newOpenAIAdaptivePoolCacheTest(t *testing.T) (*concurrencyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	return cache, server
}

func openAIAdaptivePoolTestRequest(candidates ...service.OpenAIAdaptivePoolCandidate) service.OpenAIAdaptivePoolAcquireRequest {
	return service.OpenAIAdaptivePoolAcquireRequest{
		Scope:                 "group:1|platform:openai|test",
		Candidates:            candidates,
		AccountSharePercent:   67,
		APIKeySharePercent:    33,
		EnterHighLoadPercent:  70,
		ExitHighLoadPercent:   50,
		ExitHighLoadStableFor: 5 * time.Second,
	}
}

func TestOpenAIAdaptivePoolLowVolumeUsesConfiguredAccountAccountKeyRhythm(t *testing.T) {
	cache, _ := newOpenAIAdaptivePoolCacheTest(t)
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, MaxConcurrency: 20},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAPIKey, MaxConcurrency: 20},
	)

	wantPools := []string{
		service.OpenAIAdaptivePoolAccount,
		service.OpenAIAdaptivePoolAccount,
		service.OpenAIAdaptivePoolAPIKey,
		service.OpenAIAdaptivePoolAccount,
		service.OpenAIAdaptivePoolAccount,
		service.OpenAIAdaptivePoolAPIKey,
	}
	for index, wantPool := range wantPools {
		requestID := fmt.Sprintf("low-%d", index)
		decision, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, requestID)
		require.NoError(t, err)
		require.True(t, decision.Acquired)
		require.Equal(t, service.OpenAIAdaptivePoolModeLow, decision.Mode)
		require.Equal(t, wantPool, decision.SelectedPool)
		require.NoError(t, cache.ReleaseAccountSlot(t.Context(), decision.AccountID, requestID))
	}
}

func TestOpenAIAdaptivePoolLowVolumeHonorsCustomPoolShares(t *testing.T) {
	cache, _ := newOpenAIAdaptivePoolCacheTest(t)
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, MaxConcurrency: 20},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAPIKey, MaxConcurrency: 20},
	)
	req.AccountSharePercent = 80
	req.APIKeySharePercent = 20

	poolCounts := map[string]int{}
	for index := range 100 {
		requestID := fmt.Sprintf("custom-share-%d", index)
		decision, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, requestID)
		require.NoError(t, err)
		require.True(t, decision.Acquired)
		require.Equal(t, service.OpenAIAdaptivePoolModeLow, decision.Mode)
		poolCounts[decision.SelectedPool]++
		require.NoError(t, cache.ReleaseAccountSlot(t.Context(), decision.AccountID, requestID))
	}

	require.Equal(t, 80, poolCounts[service.OpenAIAdaptivePoolAccount])
	require.Equal(t, 20, poolCounts[service.OpenAIAdaptivePoolAPIKey])
}

func TestOpenAIAdaptivePoolCorrectsLowModeFromCurrentConcurrency(t *testing.T) {
	cache, _ := newOpenAIAdaptivePoolCacheTest(t)
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, MaxConcurrency: 20},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAPIKey, MaxConcurrency: 20},
	)
	require.True(t, mustAcquireAccountSlot(t, cache, 10, 20, "seed-account-1"))
	require.True(t, mustAcquireAccountSlot(t, cache, 10, 20, "seed-account-2"))

	decision, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "correct-key")
	require.NoError(t, err)
	require.True(t, decision.Acquired)
	require.Equal(t, service.OpenAIAdaptivePoolAPIKey, decision.SelectedPool)
	require.Equal(t, 2, decision.AccountActive)
	require.Zero(t, decision.APIKeyActive)
}

func TestOpenAIAdaptivePoolHighModeUsesBothPoolsByProjectedHeadroom(t *testing.T) {
	cache, _ := newOpenAIAdaptivePoolCacheTest(t)
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, MaxConcurrency: 10},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAPIKey, MaxConcurrency: 10},
	)
	for index := range 7 {
		require.True(t, mustAcquireAccountSlot(t, cache, 10, 10, fmt.Sprintf("high-seed-%d", index)))
	}

	decision, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "high-key")
	require.NoError(t, err)
	require.True(t, decision.Acquired)
	require.Equal(t, service.OpenAIAdaptivePoolModeHigh, decision.Mode)
	require.Equal(t, service.OpenAIAdaptivePoolAPIKey, decision.SelectedPool)
	require.Equal(t, "high_concurrency_headroom", decision.BudgetReason)
}

func TestOpenAIAdaptivePoolHonorsConfiguredThresholdsAndExitHysteresis(t *testing.T) {
	cache, server := newOpenAIAdaptivePoolCacheTest(t)
	now := time.Now().Truncate(time.Second)
	server.SetTime(now)
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, MaxConcurrency: 10},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAPIKey, MaxConcurrency: 10},
	)
	req.EnterHighLoadPercent = 60
	req.ExitHighLoadPercent = 40
	for index := range 6 {
		require.True(t, mustAcquireAccountSlot(t, cache, 10, 10, fmt.Sprintf("threshold-seed-%d", index)))
	}

	high, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "threshold-high")
	require.NoError(t, err)
	require.Equal(t, service.OpenAIAdaptivePoolModeHigh, high.Mode)
	require.NoError(t, cache.ReleaseAccountSlot(t.Context(), high.AccountID, "threshold-high"))
	for index := range 6 {
		require.NoError(t, cache.ReleaseAccountSlot(t.Context(), 10, fmt.Sprintf("threshold-seed-%d", index)))
	}

	firstLowObservation, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "threshold-observe-1")
	require.NoError(t, err)
	require.Equal(t, service.OpenAIAdaptivePoolModeHigh, firstLowObservation.Mode)
	require.NoError(t, cache.ReleaseAccountSlot(t.Context(), firstLowObservation.AccountID, "threshold-observe-1"))

	server.SetTime(now.Add(6 * time.Second))
	low, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "threshold-observe-2")
	require.NoError(t, err)
	require.Equal(t, service.OpenAIAdaptivePoolModeLow, low.Mode)
}

func TestOpenAIAdaptivePoolTreatsEitherPoolQueueAsHighLoadPressure(t *testing.T) {
	cache, server := newOpenAIAdaptivePoolCacheTest(t)
	now := time.Now().Truncate(time.Second)
	server.SetTime(now)
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, MaxConcurrency: 10},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAPIKey, MaxConcurrency: 10},
	)
	require.NoError(t, cache.rdb.Set(t.Context(), accountWaitKey(20), 1, time.Minute).Err())

	high, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "key-queue-high")
	require.NoError(t, err)
	require.Equal(t, service.OpenAIAdaptivePoolModeHigh, high.Mode)
	require.Equal(t, 1, high.APIKeyWaiting)
	require.NoError(t, cache.ReleaseAccountSlot(t.Context(), high.AccountID, "key-queue-high"))
	require.NoError(t, cache.rdb.Del(t.Context(), accountWaitKey(20)).Err())

	firstNoQueue, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "key-queue-clear-1")
	require.NoError(t, err)
	require.Equal(t, service.OpenAIAdaptivePoolModeHigh, firstNoQueue.Mode)
	require.NoError(t, cache.ReleaseAccountSlot(t.Context(), firstNoQueue.AccountID, "key-queue-clear-1"))

	server.SetTime(now.Add(6 * time.Second))
	low, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "key-queue-clear-2")
	require.NoError(t, err)
	require.Equal(t, service.OpenAIAdaptivePoolModeLow, low.Mode)
}

func TestOpenAIAdaptivePoolFillsMembersByPriorityThenID(t *testing.T) {
	cache, _ := newOpenAIAdaptivePoolCacheTest(t)
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 30, Pool: service.OpenAIAdaptivePoolAccount, Priority: 1, MaxConcurrency: 2},
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, Priority: 2, MaxConcurrency: 2},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAccount, Priority: 1, MaxConcurrency: 2},
	)

	wantIDs := []int64{20, 20, 30, 30, 10}
	for index, wantID := range wantIDs {
		requestID := fmt.Sprintf("priority-fill-%d", index)
		decision, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, requestID)
		require.NoError(t, err)
		require.True(t, decision.Acquired)
		require.Equal(t, wantID, decision.AccountID)
	}

	require.NoError(t, cache.ReleaseAccountSlot(t.Context(), 20, "priority-fill-0"))
	refill, err := cache.AcquireOpenAIAdaptivePoolSlot(t.Context(), req, "priority-refill")
	require.NoError(t, err)
	require.True(t, refill.Acquired)
	require.Equal(t, int64(20), refill.AccountID)
}

func TestOpenAIAdaptivePoolAtomicAdmissionAcrossCacheInstances(t *testing.T) {
	cache, server := newOpenAIAdaptivePoolCacheTest(t)
	secondClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, secondClient.Close()) })
	second := &concurrencyCache{rdb: secondClient, slotTTLSeconds: cache.slotTTLSeconds, waitQueueTTLSeconds: cache.waitQueueTTLSeconds}
	req := openAIAdaptivePoolTestRequest(
		service.OpenAIAdaptivePoolCandidate{AccountID: 10, Pool: service.OpenAIAdaptivePoolAccount, MaxConcurrency: 30},
		service.OpenAIAdaptivePoolCandidate{AccountID: 20, Pool: service.OpenAIAdaptivePoolAPIKey, MaxConcurrency: 30},
	)

	const requests = 100
	start := make(chan struct{})
	results := make(chan *service.OpenAIAdaptivePoolAcquireDecision, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	wg.Add(requests)
	for index := range requests {
		index := index
		go func() {
			defer wg.Done()
			<-start
			selectedCache := cache
			if index%2 == 1 {
				selectedCache = second
			}
			decision, err := selectedCache.AcquireOpenAIAdaptivePoolSlot(
				context.Background(),
				req,
				fmt.Sprintf("burst-%d", index),
			)
			if err != nil {
				errs <- err
				return
			}
			results <- decision
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	acquired := 0
	poolCounts := map[string]int{}
	for decision := range results {
		if decision.Acquired {
			acquired++
			poolCounts[decision.SelectedPool]++
		}
	}
	require.Equal(t, 60, acquired)
	require.Positive(t, poolCounts[service.OpenAIAdaptivePoolAccount])
	require.Positive(t, poolCounts[service.OpenAIAdaptivePoolAPIKey])
	accountConcurrency, err := cache.GetAccountConcurrency(t.Context(), 10)
	require.NoError(t, err)
	keyConcurrency, err := cache.GetAccountConcurrency(t.Context(), 20)
	require.NoError(t, err)
	require.LessOrEqual(t, accountConcurrency, 30)
	require.LessOrEqual(t, keyConcurrency, 30)
	require.Equal(t, acquired, accountConcurrency+keyConcurrency)
}

func mustAcquireAccountSlot(
	t *testing.T,
	cache *concurrencyCache,
	accountID int64,
	limit int,
	requestID string,
) bool {
	t.Helper()
	acquired, err := cache.AcquireAccountSlot(t.Context(), accountID, limit, requestID)
	require.NoError(t, err)
	return acquired
}
