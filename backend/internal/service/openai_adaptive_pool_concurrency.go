package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	OpenAIAdaptivePoolAccount = "account"
	OpenAIAdaptivePoolAPIKey  = "key"

	OpenAIAdaptivePoolModeLow  = "low"
	OpenAIAdaptivePoolModeHigh = "high"
)

// OpenAIAdaptivePoolCandidate is the complete Redis-visible scheduling input
// for one eligible account. Lower Priority values are selected first, with the
// account ID as the tie-breaker. MaxConcurrency is the general-session limit
// after the affinity reserve has been removed; a non-positive value is
// unlimited.
type OpenAIAdaptivePoolCandidate struct {
	AccountID      int64
	Pool           string
	Priority       int
	MaxConcurrency int
}

// OpenAIAdaptivePoolAcquireRequest describes one atomic pool/member decision.
// Scope must be stable for requests sharing the same eligible scheduling pool.
type OpenAIAdaptivePoolAcquireRequest struct {
	Scope                 string
	Candidates            []OpenAIAdaptivePoolCandidate
	AccountSharePercent   int
	APIKeySharePercent    int
	EnterHighLoadPercent  int
	ExitHighLoadPercent   int
	ExitHighLoadStableFor time.Duration
}

// OpenAIAdaptivePoolAcquireDecision contains the distributed scheduling facts
// observed at the exact instant the selected account slot was acquired.
type OpenAIAdaptivePoolAcquireDecision struct {
	Acquired        bool
	AccountID       int64
	PreferredPool   string
	SelectedPool    string
	Mode            string
	AccountActive   int
	APIKeyActive    int
	AccountCapacity int
	APIKeyCapacity  int
	AccountWaiting  int
	APIKeyWaiting   int
	APIKeyBudget    int
	FallbackReason  string
	BudgetReason    string
	ReleaseFunc     func()
}

// OpenAIAdaptivePoolSlotCache is optional so existing ConcurrencyCache test
// doubles and non-Redis implementations remain source-compatible. The Redis
// repository implements it to make pool selection and account-slot admission
// one atomic operation across gateway instances.
type OpenAIAdaptivePoolSlotCache interface {
	AcquireOpenAIAdaptivePoolSlot(
		ctx context.Context,
		req OpenAIAdaptivePoolAcquireRequest,
		requestID string,
	) (*OpenAIAdaptivePoolAcquireDecision, error)
}

// AcquireOpenAIAdaptivePoolSlot atomically selects a pool, selects its highest
// priority eligible member with remaining capacity, and acquires that member's
// account slot. supported is false only when the configured cache has no
// distributed adaptive-pool implementation, allowing the scheduler to use its
// compatibility path.
func (s *ConcurrencyService) AcquireOpenAIAdaptivePoolSlot(
	ctx context.Context,
	req OpenAIAdaptivePoolAcquireRequest,
) (decision *OpenAIAdaptivePoolAcquireDecision, supported bool, err error) {
	if s == nil || s.cache == nil {
		return nil, false, nil
	}
	cache, ok := s.cache.(OpenAIAdaptivePoolSlotCache)
	if !ok {
		return nil, false, nil
	}
	if strings.TrimSpace(req.Scope) == "" {
		return nil, true, fmt.Errorf("openai adaptive pool scope is empty")
	}
	if len(req.Candidates) == 0 {
		return &OpenAIAdaptivePoolAcquireDecision{}, true, nil
	}
	if err := validateOpenAIPrioritySaturationPoolShares(req.AccountSharePercent, req.APIKeySharePercent); err != nil {
		return nil, true, err
	}
	if err := validateOpenAIPrioritySaturationLoadThresholds(req.EnterHighLoadPercent, req.ExitHighLoadPercent); err != nil {
		return nil, true, err
	}

	requestID := generateRequestID()
	decision, err = cache.AcquireOpenAIAdaptivePoolSlot(ctx, req, requestID)
	if err != nil || decision == nil || !decision.Acquired {
		return decision, true, err
	}
	if decision.AccountID <= 0 {
		return nil, true, fmt.Errorf("openai adaptive pool acquired an invalid account id")
	}

	accountID := decision.AccountID
	decision.ReleaseFunc = func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if releaseErr := s.cache.ReleaseAccountSlot(bgCtx, accountID, requestID); releaseErr != nil {
			logger.LegacyPrintf(
				"service.concurrency",
				"Warning: failed to release OpenAI adaptive pool slot for account %d (req=%s): %v",
				accountID,
				requestID,
				releaseErr,
			)
		}
	}
	return decision, true, nil
}
