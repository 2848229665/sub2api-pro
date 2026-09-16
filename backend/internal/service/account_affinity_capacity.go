package service

// AccountExtraAffinityConcurrencyReserve is retained only for compatibility
// with legacy persisted Extra data and older clients. Scheduling ignores it.
const AccountExtraAffinityConcurrencyReserve = "affinity_concurrency_reserve"

// GetAffinityConcurrencyReserve returns the protected capacity derived from
// the global reserve percentage. Non-positive concurrency keeps the existing
// unlimited convention and therefore has no finite reserve.
func (a *Account) GetAffinityConcurrencyReserve(reservePercent ...int) int {
	if a == nil {
		return 0
	}
	concurrency := a.Mode1EffectiveConcurrency()
	if concurrency <= 0 {
		return 0
	}
	percent := DefaultOpenAIPrioritySaturationAffinityReservePercent
	if len(reservePercent) > 0 {
		percent = normalizeOpenAIPrioritySaturationAffinityReservePercent(reservePercent[0])
	}
	return (concurrency/100)*percent + (concurrency%100)*percent/100
}

// GeneralConcurrencyLimit is the admission limit for new sessions and
// temporary overflow requests.
func (a *Account) GeneralConcurrencyLimit(reservePercent ...int) int {
	if a == nil {
		return 0
	}
	concurrency := a.Mode1EffectiveConcurrency()
	if concurrency <= 0 {
		return concurrency
	}
	return concurrency - a.GetAffinityConcurrencyReserve(reservePercent...)
}

// ConcurrencyLimitForAffinity applies the reserve contract consistently across
// every OpenAI scheduler. Affinity traffic may use the full account capacity;
// new sessions and temporary overflow may use only the general partition.
// The ceiling is Mode1EffectiveConcurrency so anti-degrade caps apply first.
func (a *Account) ConcurrencyLimitForAffinity(affinity bool, reservePercent ...int) int {
	if a == nil {
		return 0
	}
	if affinity {
		return a.Mode1EffectiveConcurrency()
	}
	return a.GeneralConcurrencyLimit(reservePercent...)
}
