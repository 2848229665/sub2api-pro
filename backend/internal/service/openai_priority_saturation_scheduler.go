package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var prioritySaturationUnlimitedLeadingWarnings sync.Map

const prioritySaturationWaitDiagnosticCandidateLimit = 32

const (
	prioritySaturationPoolAccount = OpenAIAdaptivePoolAccount
	prioritySaturationPoolAPIKey  = OpenAIAdaptivePoolAPIKey

	prioritySaturationExitHighLoadStableFor = 5 * time.Second
)

// prioritySaturationOpenAIAccountScheduler preserves the original
// priority-saturation behavior when pool balancing is disabled. With pool
// balancing enabled, general requests use adaptive account/API-key pools, then
// fill members in Priority/ID order before moving to the next member;
// established affinity still uses the full account concurrency so unrelated
// sessions cannot consume its reserve.
type prioritySaturationOpenAIAccountScheduler struct {
	base *defaultOpenAIAccountScheduler

	poolCursors sync.Map // compatibility path: map[group/platform] *atomic.Uint64
}

func newPrioritySaturationOpenAIAccountScheduler(service *OpenAIGatewayService, stats *openAIAccountRuntimeStats) OpenAIAccountScheduler {
	if stats == nil {
		stats = newOpenAIAccountRuntimeStats()
	}
	return &prioritySaturationOpenAIAccountScheduler{
		base: &defaultOpenAIAccountScheduler{service: service, stats: stats},
	}
}

func (s *prioritySaturationOpenAIAccountScheduler) Select(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (selection *AccountSelectionResult, decision OpenAIAccountScheduleDecision, err error) {
	startedAt := time.Now()
	defer func() {
		decision.LatencyMs = time.Since(startedAt).Milliseconds()
		s.base.metrics.recordSelect(decision)
	}()

	route, err := s.base.service.routeOpenAIAffinity(ctx, req, nil)
	applyOpenAIAffinityRouteDecision(&decision, route, req)
	if err != nil {
		return nil, decision, err
	}
	if route.Selection != nil {
		return route.Selection, decision, nil
	}
	req = route.Request
	overflowAffinityAccount := route.OverflowAccount
	canWaitForOverflow := canWaitForOpenAIAffinityOverflow(req, overflowAffinityAccount)

	selection, candidateCount, generalRejectCount, err := s.selectGeneralAccount(ctx, req)
	decision.Layer = openAIAccountScheduleLayerLoadBalance
	decision.CandidateCount = candidateCount
	decision.GeneralRejectCount = generalRejectCount
	if err != nil {
		if canWaitForOverflow &&
			(errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts)) {
			selection = s.base.service.openAIAffinityWaitSelection(req, overflowAffinityAccount)
			decision.AffinityWait = true
			setOpenAIAccountScheduleDecisionAccount(&decision, selection.Account, req.affinityReservePercent())
			return selection, decision, nil
		}
		return nil, decision, err
	}
	if selection != nil && selection.WaitPlan != nil && canWaitForOverflow {
		selection = s.base.service.openAIAffinityWaitSelection(req, overflowAffinityAccount)
		decision.AffinityWait = true
	}
	if selection != nil {
		applyPrioritySaturationPoolDecision(&decision, selection.prioritySaturationPoolDecision)
		setOpenAIAccountScheduleDecisionAccount(&decision, selection.Account, req.affinityReservePercent())
		decision.TemporaryOverflow = overflowAffinityAccount != nil &&
			selection.Acquired &&
			selection.Account != nil &&
			selection.Account.ID != overflowAffinityAccount.ID
	}
	return selection, decision, nil
}

func applyPrioritySaturationPoolDecision(
	decision *OpenAIAccountScheduleDecision,
	poolDecision *prioritySaturationPoolDecision,
) {
	if decision == nil || poolDecision == nil {
		return
	}
	decision.PreferredPool = poolDecision.preferredPool
	decision.SelectedPool = poolDecision.selectedPool
	decision.AccountActive = poolDecision.accountActive
	decision.KeyActive = poolDecision.keyActive
	decision.AccountWaiting = poolDecision.accountWaiting
	decision.KeyWaiting = poolDecision.keyWaiting
	decision.AccountCapacity = poolDecision.accountCapacity
	decision.KeyCapacity = poolDecision.keyCapacity
	decision.KeyBudget = poolDecision.keyBudget
	decision.FallbackReason = poolDecision.fallbackReason
	decision.BudgetReason = poolDecision.budgetReason
	decision.PoolMode = poolDecision.mode
}

func (s *prioritySaturationOpenAIAccountScheduler) selectGeneralAccount(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) (*AccountSelectionResult, int, int, error) {
	accounts, filterStats, err := s.eligibleAccounts(ctx, req)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(accounts) == 0 {
		return nil, 0, 0, noAvailableOpenAISelectionError(req.RequestedModel, false, filterStats.summary("priority_pool_empty"))
	}
	if !s.base.service.isOpenAIPrioritySaturationPoolBalanceEnabled(ctx) {
		return s.selectGeneralAccountPriority(ctx, req, accounts, filterStats)
	}

	return s.selectGeneralAccountBalanced(ctx, req, accounts, filterStats)
}

func (s *prioritySaturationOpenAIAccountScheduler) selectGeneralAccountPriority(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
	filterStats openAISelectionFilterStats,
) (*AccountSelectionResult, int, int, error) {

	waitableAccountIDs := make([]int64, 0, len(accounts))
	waitCandidates := make([]AccountWaitCandidateDiagnostic, 0, len(accounts))
	for _, account := range accounts {
		if req.PreserveStickyBinding && account.ID == req.StickyAccountID {
			waitCandidates = append(waitCandidates, prioritySaturationWaitCandidate(
				account,
				req.affinityReservePercent(),
				"preserved_sticky_binding",
			))
			continue
		}
		selection, waitable, acquired, acquireErr := s.acquireWithFreshLimit(ctx, req, account, false)
		if acquireErr != nil {
			return nil, len(accounts), len(waitableAccountIDs), acquireErr
		}
		if !acquired || selection == nil || selection.Account == nil {
			if waitable != nil {
				waitableAccountIDs = append(waitableAccountIDs, waitable.ID)
				waitCandidates = append(waitCandidates, prioritySaturationWaitCandidate(
					waitable,
					req.affinityReservePercent(),
					"busy",
				))
			} else {
				waitCandidates = append(waitCandidates, prioritySaturationWaitCandidate(
					account,
					req.affinityReservePercent(),
					"stale_after_immediate_acquire",
				))
			}
			continue
		}

		selection.PreserveStickyBinding = req.PreserveStickyBinding
		selection, settleErr := s.base.service.settleAcquiredOpenAISelection(ctx, req, selection)
		if settleErr != nil {
			return nil, len(accounts), len(waitableAccountIDs), settleErr
		}
		return selection, len(accounts), len(waitableAccountIDs), nil
	}

	for _, accountID := range waitableAccountIDs {
		fresh := s.freshEligibleAccount(ctx, req, accountID)
		if fresh == nil {
			markPrioritySaturationWaitCandidate(waitCandidates, accountID, nil, req.affinityReservePercent(), "stale_before_wait")
			continue
		}
		markPrioritySaturationWaitCandidate(waitCandidates, accountID, fresh, req.affinityReservePercent(), "selected_for_wait")
		selection := attachOpenAISelectionRequest(
			s.waitPlan(fresh, fresh.ConcurrencyLimitForAffinity(false, req.affinityReservePercent()), false),
			req,
		)
		selection.WaitPlan.Reason = "all_general_candidates_busy"
		selection.WaitPlan.CandidateCount = len(waitCandidates)
		selection.WaitPlan.Candidates, selection.WaitPlan.CandidatesTruncated = boundedPrioritySaturationWaitCandidates(
			waitCandidates,
			accountID,
		)
		return selection, len(accounts), len(waitableAccountIDs), nil
	}
	return nil, len(accounts), len(waitableAccountIDs), noAvailableOpenAISelectionError(req.RequestedModel, false, filterStats.summary("priority_wait_candidate_stale"))
}

type prioritySaturationPoolStats struct {
	accounts    []*Account
	active      map[int64]int
	activeTotal int
	capacity    int
	unlimited   bool
}

type prioritySaturationPoolPlan struct {
	accountPool  prioritySaturationPoolStats
	keyPool      prioritySaturationPoolStats
	keyLimits    map[int64]int
	preferred    string
	mode         string
	keyBudget    int
	budgetReason string
}

type prioritySaturationPoolAttempt struct {
	waitableIDs []int64
	candidates  []AccountWaitCandidateDiagnostic
	rejects     int
}

// prioritySaturationPoolDecision is attached to the selection so the outer
// scheduler can emit one coherent decision log after affinity settlement.
type prioritySaturationPoolDecision struct {
	preferredPool   string
	selectedPool    string
	mode            string
	fallbackReason  string
	budgetReason    string
	accountActive   int
	keyActive       int
	accountWaiting  int
	keyWaiting      int
	accountCapacity int
	keyCapacity     int
	keyBudget       int
}

func prioritySaturationAdaptivePoolScope(req OpenAIAccountScheduleRequest) string {
	group := "ungrouped"
	if req.GroupID != nil {
		group = fmt.Sprintf("group:%d", *req.GroupID)
	}
	return fmt.Sprintf(
		"%s|platform:%s|model:%s|transport:%v|capability:%v|image:%v|compact:%t|privacy:%t",
		group,
		normalizeOpenAICompatiblePlatform(req.Platform),
		req.RequestedModel,
		req.RequiredTransport,
		req.RequiredCapability,
		req.RequiredImageCapability,
		req.RequireCompact,
		req.RequirePrivacySet,
	)
}

func prioritySaturationPoolDecisionFromAdaptive(
	decision *OpenAIAdaptivePoolAcquireDecision,
) *prioritySaturationPoolDecision {
	if decision == nil {
		return nil
	}
	return &prioritySaturationPoolDecision{
		preferredPool:   decision.PreferredPool,
		selectedPool:    decision.SelectedPool,
		mode:            decision.Mode,
		fallbackReason:  decision.FallbackReason,
		budgetReason:    decision.BudgetReason,
		accountActive:   decision.AccountActive,
		keyActive:       decision.APIKeyActive,
		accountWaiting:  decision.AccountWaiting,
		keyWaiting:      decision.APIKeyWaiting,
		accountCapacity: decision.AccountCapacity,
		keyCapacity:     decision.APIKeyCapacity,
		keyBudget:       decision.APIKeyBudget,
	}
}

func (s *prioritySaturationOpenAIAccountScheduler) selectGeneralAccountBalancedAtomic(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
) (*AccountSelectionResult, int, int, bool, error) {
	if s == nil || s.base == nil || s.base.service == nil || s.base.service.concurrencyService == nil {
		return nil, len(accounts), 0, false, nil
	}

	candidates := make([]OpenAIAdaptivePoolCandidate, 0, len(accounts))
	accountByID := make(map[int64]*Account, len(accounts))
	limitByID := make(map[int64]int, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		pool := prioritySaturationPoolAccount
		if isPrioritySaturationAPIKeyAccount(account) {
			pool = prioritySaturationPoolAPIKey
		}
		limit := account.GeneralConcurrencyLimit(req.affinityReservePercent())
		candidates = append(candidates, OpenAIAdaptivePoolCandidate{
			AccountID:      account.ID,
			Pool:           pool,
			Priority:       account.Priority,
			MaxConcurrency: limit,
		})
		accountByID[account.ID] = account
		limitByID[account.ID] = limit
	}

	enterHighLoadPercent, exitHighLoadPercent := s.base.service.openAIPrioritySaturationLoadThresholds(ctx)
	distributed, supported, err := s.base.service.concurrencyService.AcquireOpenAIAdaptivePoolSlot(
		ctx,
		OpenAIAdaptivePoolAcquireRequest{
			Scope:                 prioritySaturationAdaptivePoolScope(req),
			Candidates:            candidates,
			AccountSharePercent:   s.base.service.openAIPrioritySaturationAccountSharePercent(ctx),
			APIKeySharePercent:    s.base.service.openAIPrioritySaturationAPIKeySharePercent(ctx),
			EnterHighLoadPercent:  enterHighLoadPercent,
			ExitHighLoadPercent:   exitHighLoadPercent,
			ExitHighLoadStableFor: prioritySaturationExitHighLoadStableFor,
		},
	)
	if !supported || err != nil {
		return nil, len(accounts), 0, supported, err
	}
	poolDecision := prioritySaturationPoolDecisionFromAdaptive(distributed)
	if distributed == nil || distributed.AccountID <= 0 {
		return nil, len(accounts), len(accounts), true, noAvailableOpenAISelectionError(
			req.RequestedModel,
			false,
			"adaptive_pool_no_wait_candidate",
		)
	}

	selected := accountByID[distributed.AccountID]
	if selected == nil {
		if distributed.ReleaseFunc != nil {
			distributed.ReleaseFunc()
		}
		return nil, len(accounts), 1, true, fmt.Errorf(
			"OpenAI adaptive pool selected unknown account %d",
			distributed.AccountID,
		)
	}
	fresh := s.freshEligibleAccount(ctx, req, selected.ID)
	if fresh == nil {
		if distributed.ReleaseFunc != nil {
			distributed.ReleaseFunc()
		}
		return nil, len(accounts), 1, true, fmt.Errorf(
			"OpenAI adaptive pool account %d became ineligible",
			selected.ID,
		)
	}
	freshLimit := fresh.GeneralConcurrencyLimit(req.affinityReservePercent())
	if distributed.Acquired && freshLimit != limitByID[selected.ID] {
		if distributed.ReleaseFunc != nil {
			distributed.ReleaseFunc()
		}
		return nil, len(accounts), 1, true, fmt.Errorf(
			"OpenAI adaptive pool account %d concurrency changed from %d to %d",
			selected.ID,
			limitByID[selected.ID],
			freshLimit,
		)
	}

	if distributed.Acquired {
		selection := &AccountSelectionResult{
			Account:                        fresh,
			Acquired:                       true,
			ReleaseFunc:                    distributed.ReleaseFunc,
			PreserveStickyBinding:          req.PreserveStickyBinding,
			prioritySaturationPoolDecision: poolDecision,
		}
		selection, settleErr := s.base.service.settleAcquiredOpenAISelection(ctx, req, selection)
		return selection, len(accounts), 0, true, settleErr
	}

	waitCandidates := make([]AccountWaitCandidateDiagnostic, 0, len(accounts))
	for _, account := range accounts {
		result := "busy"
		if account != nil && account.ID == fresh.ID {
			result = "selected_for_wait"
		}
		waitCandidates = append(waitCandidates, prioritySaturationWaitCandidate(
			account,
			req.affinityReservePercent(),
			result,
		))
	}
	selection := attachOpenAISelectionRequest(s.waitPlan(fresh, freshLimit, false), req)
	selection.WaitPlan.Reason = "adaptive_pools_busy"
	selection.WaitPlan.CandidateCount = len(waitCandidates)
	selection.WaitPlan.Candidates, selection.WaitPlan.CandidatesTruncated = boundedPrioritySaturationWaitCandidates(
		waitCandidates,
		fresh.ID,
	)
	selection.prioritySaturationPoolDecision = poolDecision
	return selection, len(accounts), len(accounts), true, nil
}

func (s *prioritySaturationOpenAIAccountScheduler) selectGeneralAccountBalanced(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
	filterStats openAISelectionFilterStats,
) (*AccountSelectionResult, int, int, error) {
	selection, candidateCount, generalRejectCount, supported, err := s.selectGeneralAccountBalancedAtomic(ctx, req, accounts)
	if supported && err == nil {
		return selection, candidateCount, generalRejectCount, nil
	}
	if err != nil {
		slog.Warn("openai_adaptive_pool_atomic_acquire_failed", "error", err)
	}

	// Compatibility/failure path: remain pool- and load-aware. Falling back to
	// the legacy Priority order here would let a transient Redis feature failure
	// silently bypass the account/Key policy that the administrator enabled.
	return s.selectGeneralAccountBalancedSnapshot(ctx, req, accounts, filterStats)
}

func (s *prioritySaturationOpenAIAccountScheduler) selectGeneralAccountBalancedSnapshot(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
	filterStats openAISelectionFilterStats,
) (*AccountSelectionResult, int, int, error) {
	plan, ok := s.buildBalancedPoolPlan(ctx, req, accounts)
	if !ok {
		return nil, len(accounts), 0, noAvailableOpenAISelectionError(
			req.RequestedModel,
			false,
			filterStats.summary("adaptive_pool_load_unavailable"),
		)
	}

	decision := &prioritySaturationPoolDecision{
		preferredPool:   plan.preferred,
		mode:            plan.mode,
		budgetReason:    plan.budgetReason,
		accountActive:   plan.accountPool.activeTotal,
		keyActive:       plan.keyPool.activeTotal,
		accountCapacity: plan.accountPool.observedCapacity(),
		keyCapacity:     plan.keyPool.observedCapacity(),
		keyBudget:       plan.keyBudget,
	}

	poolOrder := []string{plan.preferred}
	if plan.preferred == prioritySaturationPoolAccount && len(plan.keyPool.accounts) > 0 {
		poolOrder = append(poolOrder, prioritySaturationPoolAPIKey)
	} else if plan.preferred == prioritySaturationPoolAPIKey && len(plan.accountPool.accounts) > 0 {
		poolOrder = append(poolOrder, prioritySaturationPoolAccount)
	}

	allWaitableIDs := make([]int64, 0, len(accounts))
	allWaitCandidates := make([]AccountWaitCandidateDiagnostic, 0, len(accounts))
	generalRejectCount := 0
	for index, pool := range poolOrder {
		poolAccounts := plan.accountPool.accounts
		limits := map[int64]int(nil)
		if pool == prioritySaturationPoolAPIKey {
			poolAccounts = plan.keyPool.accounts
			limits = plan.keyLimits
		}
		attempt, selection, err := s.tryBalancedPool(ctx, req, pool, poolAccounts, limits)
		if err != nil {
			return nil, len(accounts), generalRejectCount + attempt.rejects, err
		}
		generalRejectCount += attempt.rejects
		if selection != nil && selection.Account != nil {
			decision.selectedPool = prioritySaturationPoolForAccount(selection.Account)
			if index > 0 || decision.selectedPool != decision.preferredPool {
				decision.fallbackReason = "preferred_pool_unavailable"
			}
			selection.prioritySaturationPoolDecision = decision
			return selection, len(accounts), generalRejectCount, nil
		}
		allWaitableIDs = append(allWaitableIDs, attempt.waitableIDs...)
		allWaitCandidates = append(allWaitCandidates, attempt.candidates...)
	}

	for _, accountID := range allWaitableIDs {
		fresh := s.freshEligibleAccount(ctx, req, accountID)
		if fresh == nil {
			markPrioritySaturationWaitCandidate(allWaitCandidates, accountID, nil, req.affinityReservePercent(), "stale_before_wait")
			continue
		}
		pool := prioritySaturationPoolAccount
		limit := fresh.GeneralConcurrencyLimit(req.affinityReservePercent())
		if isPrioritySaturationAPIKeyAccount(fresh) {
			pool = prioritySaturationPoolAPIKey
			if keyLimit, exists := plan.keyLimits[fresh.ID]; exists {
				limit = keyLimit
			}
		}
		if pool == prioritySaturationPoolAPIKey && limit <= 0 {
			// A zero dynamic limit means the aggregate Key budget is currently
			// exhausted. Keep the wait plan bounded to one slot so the normal
			// account wait path cannot interpret zero as unlimited.
			limit = 1
		}
		markPrioritySaturationWaitCandidateWithLimit(allWaitCandidates, accountID, fresh, limit, "selected_for_wait")
		selection := attachOpenAISelectionRequest(s.waitPlan(fresh, limit, false), req)
		selection.WaitPlan.Reason = "balanced_pools_busy"
		selection.WaitPlan.CandidateCount = len(allWaitCandidates)
		selection.WaitPlan.Candidates, selection.WaitPlan.CandidatesTruncated = boundedPrioritySaturationWaitCandidates(
			allWaitCandidates,
			accountID,
		)
		decision.selectedPool = pool
		decision.fallbackReason = "both_pools_busy"
		selection.prioritySaturationPoolDecision = decision
		return selection, len(accounts), generalRejectCount, nil
	}

	return nil, len(accounts), generalRejectCount, noAvailableOpenAISelectionError(
		req.RequestedModel,
		false,
		filterStats.summary("balanced_wait_candidate_stale"),
	)
}

func (p prioritySaturationPoolStats) effectiveCapacity() int {
	if p.unlimited {
		return prioritySaturationMaxInt()
	}
	return p.capacity
}

func (p prioritySaturationPoolStats) observedCapacity() int {
	if p.unlimited {
		return 0
	}
	return p.capacity
}

func prioritySaturationMaxInt() int {
	return int(^uint(0) >> 1)
}

func (s *prioritySaturationOpenAIAccountScheduler) buildBalancedPoolPlan(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
) (prioritySaturationPoolPlan, bool) {
	accountPool := make([]*Account, 0, len(accounts))
	keyPool := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if isPrioritySaturationAPIKeyAccount(account) {
			keyPool = append(keyPool, account)
		} else {
			accountPool = append(accountPool, account)
		}
	}

	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	active := make(map[int64]int, len(ids))
	if s.base.service.concurrencyService != nil && len(ids) > 0 {
		load, err := s.base.service.concurrencyService.GetAccountConcurrencyBatch(ctx, ids)
		if err != nil {
			slog.Warn("openai_priority_saturation_pool_load_snapshot_failed", "error", err)
			return prioritySaturationPoolPlan{}, false
		}
		for id, value := range load {
			if value > 0 {
				active[id] = value
			}
		}
	}

	plan := prioritySaturationPoolPlan{
		accountPool: buildPrioritySaturationPoolStats(accountPool, active, req.affinityReservePercent()),
		keyPool:     buildPrioritySaturationPoolStats(keyPool, active, req.affinityReservePercent()),
		keyLimits:   make(map[int64]int, len(keyPool)),
		mode:        OpenAIAdaptivePoolModeLow,
	}
	if len(accountPool) == 0 && len(keyPool) == 0 {
		return plan, true
	}
	plan.accountPool.accounts = orderPrioritySaturationPoolAccounts(plan.accountPool.accounts)
	plan.keyPool.accounts = orderPrioritySaturationPoolAccounts(plan.keyPool.accounts)

	demand := saturatingPrioritySaturationAdd(plan.accountPool.activeTotal, plan.keyPool.activeTotal)
	demand = saturatingPrioritySaturationAdd(demand, 1)
	share := s.base.service.openAIPrioritySaturationAPIKeySharePercent(ctx)
	enterHighLoadPercent, _ := s.base.service.openAIPrioritySaturationLoadThresholds(ctx)
	if prioritySaturationPoolUsagePercent(plan.accountPool) >= enterHighLoadPercent ||
		(!plan.accountPool.unlimited && plan.accountPool.capacity > 0 && plan.accountPool.activeTotal >= plan.accountPool.capacity) {
		plan.mode = OpenAIAdaptivePoolModeHigh
	}
	baseBudget := prioritySaturationPercentCeil(demand, share)
	overflowBudget := 0
	if !plan.accountPool.unlimited && demand > plan.accountPool.capacity {
		overflowBudget = demand - plan.accountPool.capacity
	}
	plan.keyBudget = baseBudget
	plan.budgetReason = "base_ratio"
	if overflowBudget > plan.keyBudget {
		plan.keyBudget = overflowBudget
		plan.budgetReason = "account_overflow"
	}
	if plan.keyBudget > plan.keyPool.effectiveCapacity() {
		plan.keyBudget = plan.keyPool.effectiveCapacity()
	}
	if plan.mode == OpenAIAdaptivePoolModeHigh {
		plan.budgetReason = "high_concurrency_headroom"
		plan.keyBudget = plan.keyPool.observedCapacity()
		for _, account := range plan.keyPool.accounts {
			if account == nil {
				continue
			}
			limit := account.GeneralConcurrencyLimit(req.affinityReservePercent())
			if limit <= 0 {
				limit = prioritySaturationMaxInt()
			}
			plan.keyLimits[account.ID] = limit
		}
	} else if len(keyPool) > 0 {
		plan.keyLimits = allocatePrioritySaturationKeyLimits(plan.keyPool, plan.keyBudget, req.affinityReservePercent())
	}

	hasAccount := len(accountPool) > 0
	hasKey := len(keyPool) > 0
	plan.preferred = s.prioritySaturationPreferredPool(req, plan, hasAccount, hasKey, share)
	return plan, true
}

func prioritySaturationPoolUsagePercent(pool prioritySaturationPoolStats) int {
	if pool.unlimited || pool.capacity <= 0 || pool.activeTotal <= 0 {
		return 0
	}
	return pool.activeTotal * 100 / pool.capacity
}

func prioritySaturationPercentCeil(value, percent int) int {
	if value <= 0 || percent <= 0 {
		return 0
	}
	whole := (value / 100) * percent
	remainder := value % 100
	return saturatingPrioritySaturationAdd(whole, (remainder*percent+99)/100)
}

func buildPrioritySaturationPoolStats(accounts []*Account, active map[int64]int, reservePercent int) prioritySaturationPoolStats {
	stats := prioritySaturationPoolStats{
		accounts: accounts,
		active:   active,
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		current := active[account.ID]
		stats.activeTotal = saturatingPrioritySaturationAdd(stats.activeTotal, current)
		limit := account.GeneralConcurrencyLimit(reservePercent)
		if limit <= 0 {
			stats.unlimited = true
			continue
		}
		stats.capacity = saturatingPrioritySaturationAdd(stats.capacity, limit)
	}
	return stats
}

func allocatePrioritySaturationKeyLimits(
	pool prioritySaturationPoolStats,
	budget int,
	reservePercent int,
) map[int64]int {
	limits := make(map[int64]int, len(pool.accounts))
	remaining := budget - pool.activeTotal
	if remaining < 0 {
		remaining = 0
	}
	for _, account := range pool.accounts {
		if account == nil {
			continue
		}
		current := pool.active[account.ID]
		configured := account.GeneralConcurrencyLimit(reservePercent)
		room := prioritySaturationAvailableRoom(configured, current)
		allocation := room
		if allocation > remaining {
			allocation = remaining
		}
		if allocation < 0 {
			allocation = 0
		}
		limits[account.ID] = saturatingPrioritySaturationAdd(current, allocation)
		remaining -= allocation
		if remaining <= 0 {
			remaining = 0
		}
	}
	return limits
}

func prioritySaturationAvailableRoom(configured, current int) int {
	if configured <= 0 {
		return prioritySaturationMaxInt()
	}
	if current >= configured {
		return 0
	}
	return configured - current
}

func saturatingPrioritySaturationAdd(a, b int) int {
	maxInt := prioritySaturationMaxInt()
	if b > 0 && a > maxInt-b {
		return maxInt
	}
	if b < 0 && a < -maxInt-b {
		return -maxInt
	}
	return a + b
}

func isPrioritySaturationAPIKeyAccount(account *Account) bool {
	return account != nil && account.Type == AccountTypeAPIKey
}

func prioritySaturationPoolForAccount(account *Account) string {
	if isPrioritySaturationAPIKeyAccount(account) {
		return prioritySaturationPoolAPIKey
	}
	return prioritySaturationPoolAccount
}

func (s *prioritySaturationOpenAIAccountScheduler) tryBalancedPool(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	pool string,
	accounts []*Account,
	limits map[int64]int,
) (prioritySaturationPoolAttempt, *AccountSelectionResult, error) {
	attempt := prioritySaturationPoolAttempt{
		waitableIDs: make([]int64, 0, len(accounts)),
		candidates:  make([]AccountWaitCandidateDiagnostic, 0, len(accounts)),
	}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		if req.PreserveStickyBinding && account.ID == req.StickyAccountID {
			attempt.candidates = append(attempt.candidates, prioritySaturationWaitCandidate(
				account,
				req.affinityReservePercent(),
				"preserved_sticky_binding",
			))
			continue
		}

		limit := account.GeneralConcurrencyLimit(req.affinityReservePercent())
		limitOverride := (*int)(nil)
		if pool == prioritySaturationPoolAPIKey {
			var exists bool
			limit, exists = limits[account.ID]
			if !exists {
				limit = 0
			}
			limitOverride = &limit
			if limit <= 0 {
				// ConcurrencyService treats a non-positive limit as unlimited. A
				// zero dynamic Key limit means that the aggregate budget is full,
				// so skip acquisition and leave a wait candidate instead.
				attempt.waitableIDs = append(attempt.waitableIDs, account.ID)
				attempt.candidates = append(attempt.candidates, prioritySaturationWaitCandidateWithLimit(account, 1, "busy_key_budget"))
				attempt.rejects++
				continue
			}
		}

		selection, waitable, acquired, acquireErr := s.acquireWithFreshLimitOverride(ctx, req, account, false, limitOverride)
		if acquireErr != nil {
			return attempt, nil, acquireErr
		}
		if !acquired || selection == nil || selection.Account == nil {
			attempt.rejects++
			if waitable != nil {
				attempt.waitableIDs = append(attempt.waitableIDs, waitable.ID)
				waitLimit := waitable.GeneralConcurrencyLimit(req.affinityReservePercent())
				if pool == prioritySaturationPoolAPIKey && limitOverride != nil {
					waitLimit = *limitOverride
				}
				if waitLimit <= 0 {
					waitLimit = 1
				}
				attempt.candidates = append(attempt.candidates, prioritySaturationWaitCandidateWithLimit(waitable, waitLimit, "busy"))
			} else {
				attempt.candidates = append(attempt.candidates, prioritySaturationWaitCandidateWithLimit(
					account,
					func() int {
						if limitOverride != nil && *limitOverride > 0 {
							return *limitOverride
						}
						return limit
					}(),
					"stale_after_immediate_acquire",
				))
			}
			continue
		}

		selection.PreserveStickyBinding = req.PreserveStickyBinding
		selection, settleErr := s.base.service.settleAcquiredOpenAISelection(ctx, req, selection)
		if settleErr != nil {
			return attempt, nil, settleErr
		}
		return attempt, selection, nil
	}
	return attempt, nil, nil
}

func prioritySaturationGroupKey(req OpenAIAccountScheduleRequest) string {
	groupKey := "ungrouped"
	if req.GroupID != nil {
		groupKey = fmt.Sprintf("%d", *req.GroupID)
	}
	return groupKey + ":" + normalizeOpenAICompatiblePlatform(req.Platform)
}

func orderPrioritySaturationPoolAccounts(accounts []*Account) []*Account {
	ordered := append([]*Account(nil), accounts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i] == nil {
			return false
		}
		if ordered[j] == nil {
			return true
		}
		if ordered[i].Priority != ordered[j].Priority {
			return ordered[i].Priority < ordered[j].Priority
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered
}

func prioritySaturationProjectedPoolScore(pool prioritySaturationPoolStats) float64 {
	capacity := pool.capacity
	if pool.unlimited {
		capacity = pool.activeTotal + (len(pool.accounts) * 1000)
	}
	if capacity <= 0 {
		capacity = 1
	}
	return float64(pool.activeTotal+1) / float64(capacity)
}

func (s *prioritySaturationOpenAIAccountScheduler) prioritySaturationPreferredPool(
	req OpenAIAccountScheduleRequest,
	plan prioritySaturationPoolPlan,
	hasAccount, hasKey bool,
	keySharePercent int,
) string {
	if !hasAccount {
		return prioritySaturationPoolAPIKey
	}
	if !hasKey {
		return prioritySaturationPoolAccount
	}

	groupKey := prioritySaturationGroupKey(req)
	if plan.mode == OpenAIAdaptivePoolModeHigh {
		accountScore := prioritySaturationProjectedPoolScore(plan.accountPool)
		keyScore := prioritySaturationProjectedPoolScore(plan.keyPool)
		if keyScore < accountScore {
			return prioritySaturationPoolAPIKey
		}
		if accountScore < keyScore {
			return prioritySaturationPoolAccount
		}
		cursorValue, _ := s.poolCursors.LoadOrStore(groupKey+":high_pool", &atomic.Uint64{})
		cursor, _ := cursorValue.(*atomic.Uint64)
		if (cursor.Add(1)-1)%2 == 1 {
			return prioritySaturationPoolAPIKey
		}
		return prioritySaturationPoolAccount
	}

	totalActive := plan.accountPool.activeTotal + plan.keyPool.activeTotal
	normalizedShare := normalizeOpenAIPrioritySaturationAPIKeySharePercent(keySharePercent)
	if totalActive > 0 && plan.keyPool.activeTotal*100 >= totalActive*normalizedShare {
		return prioritySaturationPoolAccount
	}
	if totalActive >= 2 &&
		(plan.keyPool.activeTotal+1)*100 <= (totalActive+1)*min(normalizedShare+1, 100) {
		return prioritySaturationPoolAPIKey
	}

	cursorValue, _ := s.poolCursors.LoadOrStore(groupKey+":low_pool", &atomic.Uint64{})
	cursor, _ := cursorValue.(*atomic.Uint64)
	index := cursor.Add(1) - 1
	if prioritySaturationIndexPrefersKey(index, keySharePercent) {
		return prioritySaturationPoolAPIKey
	}
	return prioritySaturationPoolAccount
}

func prioritySaturationIndexPrefersKey(index uint64, keySharePercent int) bool {
	normalizedShare := normalizeOpenAIPrioritySaturationAPIKeySharePercent(keySharePercent)
	if normalizedShare == DefaultOpenAIPrioritySaturationAPIKeySharePercent {
		// The default has an explicit low-volume contract: account, account,
		// Key. Keep that exact three-request rhythm instead of approximating
		// 33% over a longer 100-slot window.
		return index%3 == 2
	}
	share := uint64(normalizedShare)
	position := index % 100
	before := (position*share + 1) / 100
	after := ((position+1)*share + 1) / 100
	return after > before
}

func boundedPrioritySaturationWaitCandidates(
	candidates []AccountWaitCandidateDiagnostic,
	selectedAccountID int64,
) ([]AccountWaitCandidateDiagnostic, bool) {
	if len(candidates) <= prioritySaturationWaitDiagnosticCandidateLimit {
		return append([]AccountWaitCandidateDiagnostic(nil), candidates...), false
	}
	bounded := append([]AccountWaitCandidateDiagnostic(nil), candidates[:prioritySaturationWaitDiagnosticCandidateLimit]...)
	for _, candidate := range bounded {
		if candidate.AccountID == selectedAccountID {
			return bounded, true
		}
	}
	for _, candidate := range candidates[prioritySaturationWaitDiagnosticCandidateLimit:] {
		if candidate.AccountID == selectedAccountID {
			bounded[len(bounded)-1] = candidate
			break
		}
	}
	return bounded, true
}

func prioritySaturationWaitCandidate(account *Account, reservePercent int, result string) AccountWaitCandidateDiagnostic {
	if account == nil {
		return AccountWaitCandidateDiagnostic{Result: result}
	}
	return prioritySaturationWaitCandidateWithLimit(account, account.GeneralConcurrencyLimit(reservePercent), result)
}

func prioritySaturationWaitCandidateWithLimit(account *Account, limit int, result string) AccountWaitCandidateDiagnostic {
	if account == nil {
		return AccountWaitCandidateDiagnostic{Result: result}
	}
	return AccountWaitCandidateDiagnostic{
		AccountID:    account.ID,
		Priority:     account.Priority,
		GeneralLimit: limit,
		HardLimit:    account.Concurrency,
		Result:       result,
	}
}

func markPrioritySaturationWaitCandidate(
	candidates []AccountWaitCandidateDiagnostic,
	accountID int64,
	fresh *Account,
	reservePercent int,
	result string,
) {
	for i := range candidates {
		if candidates[i].AccountID != accountID {
			continue
		}
		if fresh != nil {
			candidates[i] = prioritySaturationWaitCandidate(fresh, reservePercent, result)
		} else {
			candidates[i].Result = result
		}
		return
	}
}

func markPrioritySaturationWaitCandidateWithLimit(
	candidates []AccountWaitCandidateDiagnostic,
	accountID int64,
	fresh *Account,
	limit int,
	result string,
) {
	for i := range candidates {
		if candidates[i].AccountID != accountID {
			continue
		}
		if fresh != nil {
			candidates[i] = prioritySaturationWaitCandidateWithLimit(fresh, limit, result)
		} else {
			candidates[i].Result = result
		}
		return
	}
}

func (s *prioritySaturationOpenAIAccountScheduler) eligibleAccounts(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
) ([]*Account, openAISelectionFilterStats, error) {
	accounts, err := s.base.service.listSchedulableAccounts(ctx, req.GroupID, req.Platform)
	stats := openAISelectionFilterStats{pool: len(accounts)}
	if err != nil {
		return nil, stats, err
	}

	eligible := make([]*Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if _, excluded := req.ExcludedIDs[account.ID]; excluded {
			stats.exclude("excluded")
			continue
		}
		if !account.IsSchedulable() || account.Platform != normalizeOpenAICompatiblePlatform(req.Platform) || !account.IsOpenAICompatible() {
			stats.exclude("not_schedulable")
			continue
		}
		if compatible, reason := s.base.isAccountRequestCompatibleReason(ctx, account, req); !compatible {
			stats.exclude(reason)
			continue
		}
		if !s.base.isAccountTransportCompatible(account, req.RequiredTransport) {
			stats.exclude("transport_incompatible")
			continue
		}
		if req.RequireCompact && openAICompactSupportTier(account) == 0 {
			stats.exclude("compact_unsupported")
			continue
		}
		eligible = append(eligible, account)
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].Priority != eligible[j].Priority {
			return eligible[i].Priority < eligible[j].Priority
		}
		return eligible[i].ID < eligible[j].ID
	})
	if len(eligible) > 0 && eligible[0].Concurrency <= 0 {
		warnPrioritySaturationUnlimitedLeadingAccount(eligible[0])
	}
	return eligible, stats, nil
}

func warnPrioritySaturationUnlimitedLeadingAccount(account *Account) {
	if account == nil {
		return
	}
	signature := fmt.Sprintf("%d:%d", account.Priority, account.Concurrency)
	if !storeAccountWarningSignature(&prioritySaturationUnlimitedLeadingWarnings, account.ID, signature) {
		return
	}
	slog.Warn(
		"OpenAI priority saturation leading account has unlimited concurrency and will absorb general traffic",
		"account_id", account.ID,
		"priority", account.Priority,
		"concurrency", account.Concurrency,
	)
}

func storeAccountWarningSignature(warnings *sync.Map, accountID int64, signature string) bool {
	if warnings == nil || accountID <= 0 {
		return false
	}
	for {
		previous, loaded := warnings.LoadOrStore(accountID, signature)
		if !loaded {
			return true
		}
		if previous == signature {
			return false
		}
		if warnings.CompareAndSwap(accountID, previous, signature) {
			return true
		}
	}
}

func (s *prioritySaturationOpenAIAccountScheduler) freshEligibleAccount(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accountID int64,
) *Account {
	return s.base.freshEligibleAccount(ctx, req, accountID)
}

func (s *prioritySaturationOpenAIAccountScheduler) acquireWithFreshLimit(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	account *Account,
	affinity bool,
) (*AccountSelectionResult, *Account, bool, error) {
	return s.acquireWithFreshLimitOverride(ctx, req, account, affinity, nil)
}

func (s *prioritySaturationOpenAIAccountScheduler) acquireWithFreshLimitOverride(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	account *Account,
	affinity bool,
	limitOverride *int,
) (*AccountSelectionResult, *Account, bool, error) {
	if account == nil {
		return nil, nil, false, nil
	}
	limit := account.ConcurrencyLimitForAffinity(affinity, req.affinityReservePercent())
	if limitOverride != nil {
		if *limitOverride <= 0 {
			return nil, account, false, nil
		}
		limit = prioritySaturationEffectiveLimit(limit, *limitOverride)
	}
	result, err := s.base.service.tryAcquireAccountSlot(ctx, account.ID, limit)
	if err != nil || result == nil || !result.Acquired {
		return nil, account, false, err
	}

	fresh := s.freshEligibleAccount(ctx, req, account.ID)
	if fresh == nil {
		result.ReleaseFunc()
		return nil, nil, false, nil
	}
	freshLimit := fresh.ConcurrencyLimitForAffinity(affinity, req.affinityReservePercent())
	if limitOverride != nil {
		freshLimit = prioritySaturationEffectiveLimit(freshLimit, *limitOverride)
	}
	if freshLimit != limit {
		result.ReleaseFunc()
		result, err = s.base.service.tryAcquireAccountSlot(ctx, fresh.ID, freshLimit)
		if err != nil || result == nil || !result.Acquired {
			return nil, fresh, false, err
		}
	}
	return &AccountSelectionResult{
		Account:     fresh,
		Acquired:    true,
		ReleaseFunc: result.ReleaseFunc,
	}, nil, true, nil
}

func prioritySaturationEffectiveLimit(configured, dynamic int) int {
	if dynamic <= 0 {
		return 0
	}
	if configured <= 0 || dynamic < configured {
		return dynamic
	}
	return configured
}

func (s *prioritySaturationOpenAIAccountScheduler) waitPlan(account *Account, limit int, affinity bool) *AccountSelectionResult {
	if account == nil {
		return nil
	}
	cfg := s.base.service.schedulingConfig()
	timeout := cfg.FallbackWaitTimeout
	maxWaiting := cfg.FallbackMaxWaiting
	if affinity {
		timeout = cfg.StickySessionWaitTimeout
		maxWaiting = cfg.StickySessionMaxWaiting
	}
	return &AccountSelectionResult{
		Account: account,
		WaitPlan: &AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: limit,
			Timeout:        timeout,
			MaxWaiting:     maxWaiting,
		},
	}
}

func setOpenAIAccountScheduleDecisionAccount(
	decision *OpenAIAccountScheduleDecision,
	account *Account,
	reservePercent int,
) {
	if decision == nil || account == nil {
		return
	}
	decision.SelectedAccountID = account.ID
	decision.SelectedAccountType = account.Type
	if decision.SelectedPool == "" {
		decision.SelectedPool = prioritySaturationPoolForAccount(account)
	}
	decision.GeneralLimit = account.GeneralConcurrencyLimit(reservePercent)
	decision.HardLimit = account.Concurrency
	decision.AffinityReserve = account.GetAffinityConcurrencyReserve(reservePercent)
}

func (s *prioritySaturationOpenAIAccountScheduler) ReportResult(accountID int64, success bool, firstTokenMs *int) {
	s.base.ReportResult(accountID, success, firstTokenMs)
}

func (s *prioritySaturationOpenAIAccountScheduler) ReportSwitch() {
	s.base.ReportSwitch()
}

func (s *prioritySaturationOpenAIAccountScheduler) SnapshotMetrics() OpenAIAccountSchedulerMetricsSnapshot {
	return s.base.SnapshotMetrics()
}
