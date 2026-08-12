package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type openAIWaitRecheckConcurrencyCache struct {
	ConcurrencyCache
	acquireLimits []int
	releaseCount  int
	acquireResult bool
}

func (c *openAIWaitRecheckConcurrencyCache) AcquireAccountSlot(
	_ context.Context,
	_ int64,
	maxConcurrency int,
	_ string,
) (bool, error) {
	c.acquireLimits = append(c.acquireLimits, maxConcurrency)
	return c.acquireResult, nil
}

func (c *openAIWaitRecheckConcurrencyCache) ReleaseAccountSlot(
	_ context.Context,
	_ int64,
	_ string,
) error {
	c.releaseCount++
	return nil
}

func openAIWaitRecheckAccount(id int64, concurrency int) Account {
	return Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: concurrency,
		Extra:       map[string]any{},
	}
}

func TestFinalizeAcquiredOpenAISelection_RevalidatesWaitPlanCapacity(t *testing.T) {
	const accountID = int64(48001)
	stale := openAIWaitRecheckAccount(accountID, 4)
	fresh := openAIWaitRecheckAccount(accountID, 2)
	concurrencyCache := &openAIWaitRecheckConcurrencyCache{acquireResult: true}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{fresh}},
		cache:              &stubGatewayCache{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	initialReleaseCount := 0
	req := OpenAIAccountScheduleRequest{
		Platform:               PlatformOpenAI,
		SessionHash:            "wait-plan-capacity",
		AffinityReservePercent: intPtrForTest(50),
	}
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:     &stale,
		Acquired:    true,
		ReleaseFunc: func() { initialReleaseCount++ },
		WaitPlan: &AccountWaitPlan{
			AccountID:      accountID,
			MaxConcurrency: stale.GeneralConcurrencyLimit(50),
			Timeout:        time.Second,
			MaxWaiting:     2,
		},
	}, req)

	finalized, err := svc.FinalizeAcquiredOpenAISelection(
		context.Background(),
		nil,
		req.SessionHash,
		selection,
	)

	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.True(t, finalized.Acquired)
	require.Nil(t, finalized.WaitPlan)
	require.Equal(t, 2, finalized.Account.Concurrency)
	require.Equal(t, 1, initialReleaseCount)
	require.Equal(t, []int{fresh.GeneralConcurrencyLimit(50)}, concurrencyCache.acquireLimits)

	finalized.ReleaseFunc()
	require.Equal(t, 1, concurrencyCache.releaseCount)
}

func TestFinalizeAcquiredOpenAISelection_ReschedulesAfterWaitTargetBecameIneligible(t *testing.T) {
	const accountID = int64(48002)
	stale := openAIWaitRecheckAccount(accountID, 4)
	ineligible := stale
	ineligible.Schedulable = false
	fallback := openAIWaitRecheckAccount(48003, 3)
	concurrencyCache := &openAIWaitRecheckConcurrencyCache{acquireResult: true}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{ineligible, fallback}},
		cache:              &stubGatewayCache{},
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	initialReleaseCount := 0
	req := OpenAIAccountScheduleRequest{
		Platform:               PlatformOpenAI,
		SessionHash:            "wait-plan-ineligible",
		AffinityReservePercent: intPtrForTest(34),
	}
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:     &stale,
		Acquired:    true,
		ReleaseFunc: func() { initialReleaseCount++ },
		WaitPlan: &AccountWaitPlan{
			AccountID:      accountID,
			MaxConcurrency: stale.GeneralConcurrencyLimit(34),
			Timeout:        time.Second,
			MaxWaiting:     2,
		},
	}, req)

	finalized, err := svc.FinalizeAcquiredOpenAISelection(
		context.Background(),
		nil,
		req.SessionHash,
		selection,
	)

	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.True(t, finalized.Acquired)
	require.NotNil(t, finalized.Account)
	require.Equal(t, fallback.ID, finalized.Account.ID)
	require.Equal(t, 1, initialReleaseCount)
	require.Equal(t, []int{fallback.GeneralConcurrencyLimit(34)}, concurrencyCache.acquireLimits)

	finalized.ReleaseFunc()
	require.Equal(t, 1, concurrencyCache.releaseCount)
}

func TestFinalizeAcquiredOpenAISelection_ReschedulesAfterWaitTargetLosesRequiredPrivacy(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)

	groupID := int64(48020)
	const (
		targetID   = int64(48021)
		fallbackID = int64(48022)
	)
	stale := openAIWaitRecheckAccount(targetID, 4)
	stale.GroupIDs = []int64{groupID}
	stale.Extra["privacy_mode"] = PrivacyModeTrainingOff
	ineligible := stale
	ineligible.Extra = map[string]any{}
	fallback := openAIWaitRecheckAccount(fallbackID, 3)
	fallback.GroupIDs = []int64{groupID}
	fallback.Extra["privacy_mode"] = PrivacyModeTrainingOff

	concurrencyCache := &openAIWaitRecheckConcurrencyCache{acquireResult: true}
	settingRepo := &openAIAdvancedSchedulerSettingRepoStub{values: map[string]string{
		openAIAdvancedSchedulerSettingKey:                      "false",
		SettingKeyOpenAIPrioritySaturationEnabled:              "true",
		SettingKeyOpenAIOAuthSchedulingRateMultiplier:          "1",
		SettingKeyOpenAILowUpstreamRatePriorityEnabled:         "false",
		SettingKeyOpenAIAdvancedSchedulerStickyWeightedEnabled: "false",
	}}
	svc := &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{
			schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{
				accounts: []Account{ineligible, fallback},
			},
		},
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
		rateLimitService: &RateLimitService{
			settingService: NewSettingService(settingRepo, &config.Config{}),
		},
	}

	group := &Group{
		ID:                groupID,
		Platform:          PlatformOpenAI,
		Status:            StatusActive,
		Hydrated:          true,
		RequirePrivacySet: true,
	}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	initialReleaseCount := 0
	req := OpenAIAccountScheduleRequest{
		GroupID:                &groupID,
		Platform:               PlatformOpenAI,
		RequirePrivacySet:      true,
		AffinityReservePercent: intPtrForTest(34),
	}
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:     &stale,
		Acquired:    true,
		ReleaseFunc: func() { initialReleaseCount++ },
		WaitPlan: &AccountWaitPlan{
			AccountID:      targetID,
			MaxConcurrency: stale.GeneralConcurrencyLimit(34),
			Timeout:        time.Second,
			MaxWaiting:     2,
		},
	}, req)

	finalized, err := svc.FinalizeAcquiredOpenAISelection(ctx, &groupID, "", selection)

	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.True(t, finalized.Acquired)
	require.Equal(t, fallbackID, finalized.Account.ID)
	require.Equal(t, 1, initialReleaseCount)
	require.Equal(t, []int{fallback.GeneralConcurrencyLimit(34)}, concurrencyCache.acquireLimits)

	finalized.ReleaseFunc()
	require.Equal(t, 1, concurrencyCache.releaseCount)
}

func TestFinalizeAcquiredOpenAISelection_MigratesIneligibleWaitOwner(t *testing.T) {
	const (
		ownerID    = int64(48004)
		fallbackID = int64(48005)
	)
	staleOwner := openAIWaitRecheckAccount(ownerID, 4)
	ineligibleOwner := staleOwner
	ineligibleOwner.Schedulable = false
	fallback := openAIWaitRecheckAccount(fallbackID, 3)
	sessionHash := "wait-owner-ineligible"
	sessionCache := &prioritySaturationSessionCache{
		schedulerTestGatewayCache: schedulerTestGatewayCache{
			sessionBindings: map[string]int64{"openai:" + sessionHash: ownerID},
		},
	}
	concurrencyCache := &openAIWaitRecheckConcurrencyCache{acquireResult: true}
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{
			accounts: []Account{ineligibleOwner, fallback},
		},
		cache:              sessionCache,
		cfg:                &config.Config{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	initialReleaseCount := 0
	req := OpenAIAccountScheduleRequest{
		Platform:               PlatformOpenAI,
		SessionHash:            sessionHash,
		StickyAccountID:        ownerID,
		CanTemporarilyOverflow: true,
	}
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:        &staleOwner,
		Acquired:       true,
		ReleaseFunc:    func() { initialReleaseCount++ },
		SessionOwnerID: ownerID,
		WaitPlan: &AccountWaitPlan{
			AccountID:      ownerID,
			MaxConcurrency: staleOwner.ConcurrencyLimitForAffinity(true),
			Timeout:        time.Second,
			MaxWaiting:     2,
		},
	}, req)

	finalized, err := svc.FinalizeAcquiredOpenAISelection(
		context.Background(),
		nil,
		sessionHash,
		selection,
	)

	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.True(t, finalized.Acquired)
	require.Equal(t, fallbackID, finalized.Account.ID)
	require.Equal(t, fallbackID, finalized.SessionOwnerID)
	require.False(t, finalized.PreserveStickyBinding)
	require.Equal(t, 1, initialReleaseCount)

	sessionCache.mu.Lock()
	require.Equal(t, fallbackID, sessionCache.sessionBindings["openai:"+sessionHash])
	sessionCache.mu.Unlock()

	finalized.ReleaseFunc()
	require.Equal(t, 1, concurrencyCache.releaseCount)
}

// A budget-limited wait plan (priority-saturation Key-pool snapshot path)
// carries a dynamic per-request budget in MaxConcurrency, not the account's
// static C/R/G. The handler already acquired the slot under that budget, so
// revalidation must keep it rather than release and re-acquire under the looser
// static G, which would silently bypass the aggregate Key-pool budget.
func TestFinalizeAcquiredOpenAISelection_KeepsBudgetLimitedWaitPlanSlot(t *testing.T) {
	const accountID = int64(48050)
	fresh := openAIWaitRecheckAccount(accountID, 4)
	// Static G at 50% reserve is 2; the dynamic budget below is deliberately 1
	// so the "capacity changed" branch would fire and re-acquire under G if the
	// BudgetLimited flag were ignored.
	require.Equal(t, 2, fresh.GeneralConcurrencyLimit(50))
	concurrencyCache := &openAIWaitRecheckConcurrencyCache{acquireResult: true}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{fresh}},
		cache:              &stubGatewayCache{},
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	stale := openAIWaitRecheckAccount(accountID, 4)
	initialReleaseCount := 0
	req := OpenAIAccountScheduleRequest{
		Platform:               PlatformOpenAI,
		SessionHash:            "budget-limited-wait",
		AffinityReservePercent: intPtrForTest(50),
	}
	selection := attachOpenAISelectionRequest(&AccountSelectionResult{
		Account:     &stale,
		Acquired:    true,
		ReleaseFunc: func() { initialReleaseCount++ },
		WaitPlan: &AccountWaitPlan{
			AccountID:      accountID,
			MaxConcurrency: 1,
			BudgetLimited:  true,
			Timeout:        time.Second,
			MaxWaiting:     2,
		},
	}, req)

	finalized, err := svc.FinalizeAcquiredOpenAISelection(
		context.Background(),
		nil,
		req.SessionHash,
		selection,
	)

	require.NoError(t, err)
	require.NotNil(t, finalized)
	require.True(t, finalized.Acquired)
	require.Nil(t, finalized.WaitPlan)
	// The originally acquired budget-limited slot is kept: no release and no
	// re-acquire under the looser static limit.
	require.Zero(t, initialReleaseCount)
	require.Empty(t, concurrencyCache.acquireLimits)

	finalized.ReleaseFunc()
	require.Equal(t, 1, initialReleaseCount, "the originally acquired slot's release func is preserved")
	require.Zero(t, concurrencyCache.releaseCount)
}
