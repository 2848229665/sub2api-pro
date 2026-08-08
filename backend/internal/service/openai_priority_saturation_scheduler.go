package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

var prioritySaturationUnlimitedLeadingWarnings sync.Map

const prioritySaturationWaitDiagnosticCandidateLimit = 32

const (
	prioritySaturationPoolAccount   = "account"
	prioritySaturationPoolAPIKey    = "key"
	prioritySaturationAssignmentTTL = 5 * time.Minute
)

// prioritySaturationOpenAIAccountScheduler deterministically fills lower
// numeric priorities before moving to the next account. New and overflow
// requests use GeneralConcurrencyLimit; established affinity uses the full
// account concurrency so the configured reserve cannot be consumed by
// unrelated sessions.
type prioritySaturationOpenAIAccountScheduler struct {
	base *defaultOpenAIAccountScheduler

	poolCursors              sync.Map // map[group/platform] *atomic.Uint64
	assignments              sync.Map // map[request/group/platform] prioritySaturationPoolAssignment
	assignmentCleanupCounter atomic.Uint64
}

type prioritySaturationPoolAssignment struct {
	pool      string
	createdAt time.Time
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
	decision.AccountCapacity = poolDecision.accountCapacity
	decision.KeyCapacity = poolDecision.keyCapacity
	decision.KeyBudget = poolDecision.keyBudget
	decision.FallbackReason = poolDecision.fallbackReason
	decision.BudgetReason = poolDecision.budgetReason
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
	fallbackReason  string
	budgetReason    string
	accountActive   int
	keyActive       int
	accountCapacity int
	keyCapacity     int
	keyBudget       int
}

func (s *prioritySaturationOpenAIAccountScheduler) selectGeneralAccountBalanced(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	accounts []*Account,
	filterStats openAISelectionFilterStats,
) (*AccountSelectionResult, int, int, error) {
	plan, ok := s.buildBalancedPoolPlan(ctx, req, accounts)
	if !ok {
		// A load snapshot is advisory. If Redis is temporarily unavailable, keep
		// the established priority scheduler as the fail-safe behavior rather than
		// turning a scheduling telemetry failure into a gateway outage.
		return s.selectGeneralAccountPriority(ctx, req, accounts, filterStats)
	}

	decision := &prioritySaturationPoolDecision{
		preferredPool:   plan.preferred,
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
	}
	if len(accountPool) == 0 && len(keyPool) == 0 {
		return plan, true
	}

	demand := saturatingPrioritySaturationAdd(plan.accountPool.activeTotal, plan.keyPool.activeTotal)
	demand = saturatingPrioritySaturationAdd(demand, 1)
	share := s.base.service.openAIPrioritySaturationAPIKeySharePercent(ctx)
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
	if len(keyPool) > 0 {
		plan.keyLimits = allocatePrioritySaturationKeyLimits(plan.keyPool, plan.keyBudget, req.affinityReservePercent())
	}

	hasAccount := len(accountPool) > 0
	hasKey := len(keyPool) > 0
	plan.preferred = s.prioritySaturationPreferredPool(ctx, req, hasAccount, hasKey, share)
	return plan, true
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

func (s *prioritySaturationOpenAIAccountScheduler) prioritySaturationPreferredPool(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	hasAccount, hasKey bool,
	keySharePercent int,
) string {
	if !hasAccount {
		return prioritySaturationPoolAPIKey
	}
	if !hasKey {
		return prioritySaturationPoolAccount
	}

	groupKey := "ungrouped"
	if req.GroupID != nil {
		groupKey = fmt.Sprintf("%d", *req.GroupID)
	}
	groupKey += ":" + normalizeOpenAICompatiblePlatform(req.Platform)
	requestKey := prioritySaturationRequestAssignmentKey(ctx, groupKey)
	if requestKey != "" {
		if value, ok := s.assignments.Load(requestKey); ok {
			if assignment, ok := value.(prioritySaturationPoolAssignment); ok && time.Since(assignment.createdAt) < prioritySaturationAssignmentTTL {
				return assignment.pool
			}
			s.assignments.Delete(requestKey)
		}
	}

	// Requests that are already carrying exclusions are failover attempts. If
	// no request identity is available, do not advance the global cursor again;
	// prefer the account pool and let normal fallback rules pick a Key if needed.
	if requestKey == "" && len(req.ExcludedIDs) > 0 {
		return prioritySaturationPoolAccount
	}

	cursorValue, _ := s.poolCursors.LoadOrStore(groupKey, &atomic.Uint64{})
	cursor, _ := cursorValue.(*atomic.Uint64)
	index := cursor.Add(1) - 1
	preferred := prioritySaturationPoolAccount
	if prioritySaturationIndexPrefersKey(index, keySharePercent) {
		preferred = prioritySaturationPoolAPIKey
	}
	if requestKey != "" {
		s.assignments.Store(requestKey, prioritySaturationPoolAssignment{pool: preferred, createdAt: time.Now()})
		s.cleanupPrioritySaturationAssignments()
	}
	return preferred
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

func prioritySaturationRequestAssignmentKey(ctx context.Context, groupKey string) string {
	if ctx != nil {
		if requestID, _ := ctx.Value(ctxkey.RequestID).(string); strings.TrimSpace(requestID) != "" {
			return groupKey + ":request:" + strings.TrimSpace(requestID)
		}
	}
	return ""
}

func (s *prioritySaturationOpenAIAccountScheduler) cleanupPrioritySaturationAssignments() {
	if s.assignmentCleanupCounter.Add(1)%1024 != 0 {
		return
	}
	cutoff := time.Now().Add(-prioritySaturationAssignmentTTL)
	s.assignments.Range(func(key, value any) bool {
		assignment, ok := value.(prioritySaturationPoolAssignment)
		if ok && assignment.createdAt.Before(cutoff) {
			s.assignments.Delete(key)
		}
		return true
	})
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
