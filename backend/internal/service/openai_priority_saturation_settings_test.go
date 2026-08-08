package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type prioritySaturationDefaultSettingRepo struct {
	SettingRepository
	values map[string]string
}

func (r *prioritySaturationDefaultSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *prioritySaturationDefaultSettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func TestInitializeDefaultSettingsEnablesPrioritySaturation(t *testing.T) {
	repo := &prioritySaturationDefaultSettingRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})

	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "true", repo.values[SettingKeyOpenAIPrioritySaturationEnabled])
	require.Equal(t, "20", repo.values[SettingKeyOpenAIPrioritySaturationAffinityReservePercent])
	require.Equal(t, "true", repo.values[SettingKeyOpenAIPrioritySaturationPoolBalanceEnabled])
	require.Equal(t, "67", repo.values[SettingKeyOpenAIPrioritySaturationAccountSharePercent])
	require.Equal(t, "33", repo.values[SettingKeyOpenAIPrioritySaturationAPIKeySharePercent])
	require.Equal(t, "70", repo.values[SettingKeyOpenAIPrioritySaturationEnterHighLoadPercent])
	require.Equal(t, "50", repo.values[SettingKeyOpenAIPrioritySaturationExitHighLoadPercent])
	require.Equal(t, "false", repo.values[openAIAdvancedSchedulerSettingKey])
}

func TestValidateOpenAIPrioritySaturationAffinityReservePercent(t *testing.T) {
	for _, percent := range []int{0, 20, 99} {
		require.NoError(t, validateOpenAIPrioritySaturationAffinityReservePercent(percent))
	}
	require.Error(t, validateOpenAIPrioritySaturationAffinityReservePercent(-1))
	require.Error(t, validateOpenAIPrioritySaturationAffinityReservePercent(100))
	require.Equal(t, 20, parseOpenAIPrioritySaturationAffinityReservePercent(""))
	require.Equal(t, 20, parseOpenAIPrioritySaturationAffinityReservePercent("invalid"))
	require.Equal(t, 20, parseOpenAIPrioritySaturationAffinityReservePercent("100"))
	require.Equal(t, 0, parseOpenAIPrioritySaturationAffinityReservePercent("0"))
}

func TestPrioritySaturationSwitchesRequirePersistedTrue(t *testing.T) {
	require.False(t, parseOpenAIPrioritySaturationEnabled(""))
	require.True(t, parseOpenAIPrioritySaturationEnabled("true"))
	require.False(t, parseOpenAIPrioritySaturationEnabled("false"))
	require.False(t, parseOpenAIPrioritySaturationPoolBalanceEnabled(""))
	require.True(t, parseOpenAIPrioritySaturationPoolBalanceEnabled("true"))
	require.False(t, parseOpenAIPrioritySaturationPoolBalanceEnabled("false"))
}

func TestValidateOpenAIPrioritySaturationAPIKeySharePercent(t *testing.T) {
	for _, percent := range []int{1, 33, 99} {
		require.NoError(t, validateOpenAIPrioritySaturationAPIKeySharePercent(percent))
	}
	require.Error(t, validateOpenAIPrioritySaturationAPIKeySharePercent(0))
	require.Error(t, validateOpenAIPrioritySaturationAPIKeySharePercent(100))
	require.Equal(t, 33, parseOpenAIPrioritySaturationAPIKeySharePercent(""))
	require.Equal(t, 33, parseOpenAIPrioritySaturationAPIKeySharePercent("invalid"))
	require.Equal(t, 33, parseOpenAIPrioritySaturationAPIKeySharePercent("100"))
	require.Equal(t, 33, parseOpenAIPrioritySaturationAPIKeySharePercent("0"))
	require.Equal(t, 25, parseOpenAIPrioritySaturationAPIKeySharePercent("25"))
}

func TestValidateOpenAIPrioritySaturationPoolShares(t *testing.T) {
	require.NoError(t, validateOpenAIPrioritySaturationPoolShares(67, 33))
	require.NoError(t, validateOpenAIPrioritySaturationPoolShares(80, 20))
	require.Error(t, validateOpenAIPrioritySaturationPoolShares(60, 30))
	require.Error(t, validateOpenAIPrioritySaturationPoolShares(0, 100))
	account, apiKey := parseOpenAIPrioritySaturationPoolShares("", "33")
	require.Equal(t, 67, account)
	require.Equal(t, 33, apiKey)
	account, apiKey = parseOpenAIPrioritySaturationPoolShares("", "40")
	require.Equal(t, 60, account)
	require.Equal(t, 40, apiKey)
	account, apiKey = parseOpenAIPrioritySaturationPoolShares("80", "")
	require.Equal(t, 80, account)
	require.Equal(t, 20, apiKey)
}

func TestValidateOpenAIPrioritySaturationLoadThresholds(t *testing.T) {
	require.NoError(t, validateOpenAIPrioritySaturationLoadThresholds(70, 50))
	require.NoError(t, validateOpenAIPrioritySaturationLoadThresholds(100, 99))
	require.Error(t, validateOpenAIPrioritySaturationLoadThresholds(50, 50))
	require.Error(t, validateOpenAIPrioritySaturationLoadThresholds(40, 50))
	require.Error(t, validateOpenAIPrioritySaturationLoadThresholds(0, 0))
}

func TestParseSettingsRepairsInvalidAdaptivePoolPairs(t *testing.T) {
	svc := NewSettingService(nil, &config.Config{})
	settings := svc.parseSettings(map[string]string{
		SettingKeyOpenAIPrioritySaturationAccountSharePercent:  "60",
		SettingKeyOpenAIPrioritySaturationAPIKeySharePercent:   "30",
		SettingKeyOpenAIPrioritySaturationEnterHighLoadPercent: "40",
		SettingKeyOpenAIPrioritySaturationExitHighLoadPercent:  "50",
	})

	require.Equal(t, 67, settings.OpenAIPrioritySaturationAccountSharePercent)
	require.Equal(t, 33, settings.OpenAIPrioritySaturationAPIKeySharePercent)
	require.Equal(t, 70, settings.OpenAIPrioritySaturationEnterHighLoadPercent)
	require.Equal(t, 50, settings.OpenAIPrioritySaturationExitHighLoadPercent)
}

func TestRefreshCachedSettingsPreservesPrioritySaturationSwitch(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	service := &SettingService{}
	service.refreshCachedSettings(&SystemSettings{
		OpenAIPrioritySaturationEnabled:                true,
		OpenAIPrioritySaturationAffinityReservePercent: 35,
		OpenAIPrioritySaturationPoolBalanceEnabled:     true,
		OpenAIPrioritySaturationAccountSharePercent:    75,
		OpenAIPrioritySaturationAPIKeySharePercent:     25,
		OpenAIPrioritySaturationEnterHighLoadPercent:   80,
		OpenAIPrioritySaturationExitHighLoadPercent:    55,
	})

	cached, ok := openAIAdvancedSchedulerSettingCache.Load().(*cachedOpenAIAdvancedSchedulerSetting)
	require.True(t, ok)
	require.NotNil(t, cached)
	require.True(t, cached.prioritySaturationEnabled)
	require.Equal(t, 35, cached.affinityReservePercent)
	require.True(t, cached.poolBalanceEnabled)
	require.Equal(t, 75, cached.accountSharePercent)
	require.Equal(t, 25, cached.apiKeySharePercent)
	require.Equal(t, 80, cached.enterHighLoadPercent)
	require.Equal(t, 55, cached.exitHighLoadPercent)
	require.False(t, cached.enabled)
}

func TestPrioritySaturationTakesPrecedenceWhenBothSchedulersAreEnabled(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	svc := &OpenAIGatewayService{}
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:                   true,
		prioritySaturationEnabled: true,
		affinityReservePercent:    20,
		expiresAt:                 time.Now().Add(time.Minute).UnixNano(),
	})

	_, ok := svc.getOpenAIAccountScheduler(t.Context()).(*prioritySaturationOpenAIAccountScheduler)
	require.True(t, ok)
}

func TestOpenAIAccountSchedulersShareRuntimeStatsAcrossPolicySwitch(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	svc := &OpenAIGatewayService{}
	expiresAt := time.Now().Add(time.Minute).UnixNano()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:   true,
		expiresAt: expiresAt,
	})
	weighted, ok := svc.getOpenAIAccountScheduler(t.Context()).(*defaultOpenAIAccountScheduler)
	require.True(t, ok)

	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		prioritySaturationEnabled: true,
		expiresAt:                 expiresAt,
	})
	priority, ok := svc.getOpenAIAccountScheduler(t.Context()).(*prioritySaturationOpenAIAccountScheduler)
	require.True(t, ok)

	require.NotNil(t, weighted.stats)
	require.Same(t, weighted.stats, priority.base.stats)
	require.Same(t, weighted.stats, svc.openaiAccountStats)
}

func TestOpenAIAccountRuntimeStatsConcurrentInitialization(t *testing.T) {
	const workers = 64

	svc := &OpenAIGatewayService{}
	start := make(chan struct{})
	results := make(chan *openAIAccountRuntimeStats, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			<-start
			results <- svc.getOpenAIAccountRuntimeStats()
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	var canonical *openAIAccountRuntimeStats
	for stats := range results {
		require.NotNil(t, stats)
		if canonical == nil {
			canonical = stats
			continue
		}
		require.Same(t, canonical, stats)
	}
	require.Same(t, canonical, svc.openaiAccountStats)
}
