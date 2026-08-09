package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const accessibleModelsCacheNamespace = "accessible:"

var compositeModelsDiscoveryEndpoints = []string{
	CompositeRouteEndpointMessages,
	CompositeRouteEndpointCountTokens,
	CompositeRouteEndpointResponses,
	CompositeRouteEndpointChatCompletions,
	CompositeRouteEndpointEmbeddings,
	CompositeRouteEndpointImages,
	CompositeRouteEndpointGemini,
}

// ModelAvailabilityDiagnosis describes whether the requested model can be
// served by any persistently eligible account in the group (active with its
// schedulable setting enabled), ignoring transient state such as rate limits,
// overload, temporary unschedulability, and runtime blocks. Handlers use this
// on the "no available accounts" error path to distinguish 404
// model_not_found from 503 service_unavailable.
type ModelAvailabilityDiagnosis struct {
	// HasAccountsInPool is true if the group has at least one persistently
	// eligible account on the queried platform (or, for Anthropic/Gemini, on
	// the platform plus mixed-scheduled Antigravity accounts).
	HasAccountsInPool bool
	// HasModelSupport is true if at least one account's model mapping admits
	// the requested model.
	HasModelSupport bool
}

// ModelAvailabilityDiagnoser is implemented by gateway services that can
// report whether the requested model is configured to be served by any
// account. Both *GatewayService and *OpenAIGatewayService implement this so
// handlers in either package can share a single classifier.
type ModelAvailabilityDiagnoser interface {
	DiagnoseModelAvailabilityForPlatform(
		ctx context.Context,
		groupID *int64,
		requestedModel string,
		platform string,
	) ModelAvailabilityDiagnosis
}

// DiagnoseModelAvailabilityForPlatform inspects accounts enabled for scheduling
// by persistent configuration and returns whether the requested model is
// configured to be served by any of them. The dedicated repository query
// bypasses scheduler snapshots and deliberately ignores transient rate-limit,
// overload, temporary-unschedulable, expiry, quota, and runtime-block state.
//
// Safe to call on the error path: returns {true,true} on any internal failure
// or when the inputs preclude meaningful diagnosis (empty model, etc.), so
// callers stay on the 503 fallback branch.
func (s *GatewayService) DiagnoseModelAvailabilityForPlatform(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	platform string,
) ModelAvailabilityDiagnosis {
	if s == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		// No model specified — cannot decide model_not_found. Caller falls back to 503.
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}
	if strings.TrimSpace(platform) == "" {
		// Without a platform we cannot scope the lookup; bail out to the
		// 503 branch rather than make an unscoped scan.
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	if s.accountRepo == nil {
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	accounts, err := s.listModelAvailabilityCandidatesForPlatform(ctx, groupID, platform)
	if err != nil {
		// Conservative fallback: pretend everything is fine so the caller
		// returns 503 (we don't want to flip to 404 just because a lookup
		// hiccup'd).
		return ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: true}
	}

	diag := ModelAvailabilityDiagnosis{}
	for i := range accounts {
		diag.HasAccountsInPool = true
		if s.isModelSupportedByAccountWithContext(ctx, &accounts[i], requestedModel) {
			diag.HasModelSupport = true
			return diag
		}
	}
	return diag
}

// GetAccessibleModels returns the stable model IDs that the group can serve
// through persistently enabled accounts. Temporary rate limits, overload,
// runtime breakers, and temporary unschedulability do not remove models from
// this discovery list.
func (s *GatewayService) GetAccessibleModels(ctx context.Context, groupID *int64, platform string) []string {
	if s == nil || s.accountRepo == nil {
		return nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return nil
	}
	if platform == PlatformComposite {
		return s.getCompositeAccessibleModels(ctx, groupID)
	}

	cacheKey := modelsListCacheKey(groupID, accessibleModelsCacheNamespace+platform)
	if s.modelsListCache != nil {
		if cached, found := s.modelsListCache.Get(cacheKey); found {
			if models, ok := cached.([]string); ok {
				modelsListCacheHitTotal.Add(1)
				return cloneStringSlice(models)
			}
		}
	}
	modelsListCacheMissTotal.Add(1)

	accounts, err := s.listModelAvailabilityCandidatesForPlatform(ctx, groupID, platform)
	if err != nil {
		return nil
	}
	models := s.accessibleModelsFromAccounts(ctx, platform, accounts)
	if s.modelsListCache != nil {
		s.modelsListCache.Set(cacheKey, cloneStringSlice(models), s.modelsListCacheTTL)
		modelsListCacheStoreTotal.Add(1)
	}
	return cloneStringSlice(models)
}

func (s *GatewayService) listModelAvailabilityCandidatesForPlatform(
	ctx context.Context,
	groupID *int64,
	platform string,
) ([]Account, error) {
	if s == nil || s.accountRepo == nil {
		return nil, nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" || platform == PlatformComposite {
		return nil, nil
	}

	useMixed := platform == PlatformAnthropic || platform == PlatformGemini
	platforms := []string{platform}
	if useMixed {
		platforms = append(platforms, PlatformAntigravity)
	}

	queryGroupID := groupID
	includeGrouped := false
	if useMixed {
		// Preserve the generic scheduler's scope rules: an explicit group wins
		// for mixed scheduling, while group-less simple mode scans all accounts.
		if groupID == nil && s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
			includeGrouped = true
		}
	} else if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		queryGroupID = nil
		includeGrouped = true
	}

	accounts, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, queryGroupID, platforms, includeGrouped)
	if err != nil || !useMixed {
		return accounts, err
	}

	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].Platform == PlatformAntigravity && !accounts[i].IsMixedSchedulingEnabled() {
			continue
		}
		filtered = append(filtered, accounts[i])
	}
	return filtered, nil
}

func (s *GatewayService) accessibleModelsFromAccounts(ctx context.Context, platform string, accounts []Account) []string {
	if len(accounts) == 0 {
		return nil
	}

	candidates := make([]string, 0)
	seenCandidates := make(map[string]struct{})
	appendCandidate := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || strings.HasSuffix(model, "*") {
			return
		}
		if _, ok := seenCandidates[model]; ok {
			return
		}
		seenCandidates[model] = struct{}{}
		candidates = append(candidates, model)
	}

	for _, model := range defaultModelsListCandidateIDs(platform) {
		appendCandidate(model)
	}

	extraCandidates := make([]string, 0)
	for i := range accounts {
		for model := range accounts[i].GetModelMapping() {
			model = strings.TrimSpace(model)
			if model == "" || strings.HasSuffix(model, "*") {
				continue
			}
			if _, ok := seenCandidates[model]; ok {
				continue
			}
			seenCandidates[model] = struct{}{}
			extraCandidates = append(extraCandidates, model)
		}
	}
	sort.Strings(extraCandidates)
	candidates = append(candidates, extraCandidates...)

	models := make([]string, 0, len(candidates))
	for _, model := range candidates {
		for i := range accounts {
			if s.isModelSupportedByAccountWithContext(ctx, &accounts[i], model) {
				models = append(models, model)
				break
			}
		}
	}
	return models
}

func (s *GatewayService) getCompositeAccessibleModels(ctx context.Context, groupID *int64) []string {
	concretePlatforms := []string{PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity, PlatformGrok}
	accessibleByPlatform := make(map[string]map[string]struct{}, len(concretePlatforms))
	candidates := make([]string, 0)
	seenCandidates := make(map[string]struct{})
	appendCandidate := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, ok := seenCandidates[model]; ok {
			return
		}
		seenCandidates[model] = struct{}{}
		candidates = append(candidates, model)
	}

	for _, targetPlatform := range concretePlatforms {
		platformModels := s.GetAccessibleModels(ctx, groupID, targetPlatform)
		modelSet := make(map[string]struct{}, len(platformModels))
		for _, model := range platformModels {
			modelSet[model] = struct{}{}
			appendCandidate(model)
		}
		accessibleByPlatform[targetPlatform] = modelSet
	}

	var routes []CompositeModelRoute
	if groupID != nil && s.compositeResolver != nil && s.compositeResolver.repo != nil {
		var err error
		routes, err = s.compositeResolver.repo.ListByGroup(ctx, *groupID, false)
		if err != nil {
			return nil
		}
		for _, route := range routes {
			if normalizeCompositeRouteMatchType(route.MatchType) == CompositeRouteMatchExact {
				appendCandidate(route.PublicModel)
			}
		}
	}

	poolByPlatform := make(map[string][]Account)
	poolLoaded := make(map[string]bool)
	modelSupportMemo := make(map[string]bool)
	supportsModel := func(targetPlatform, model string) bool {
		targetPlatform = strings.TrimSpace(targetPlatform)
		model = strings.TrimSpace(model)
		if targetPlatform == "" || model == "" {
			return false
		}
		if models := accessibleByPlatform[targetPlatform]; models != nil {
			if _, ok := models[model]; ok {
				return true
			}
		}
		memoKey := targetPlatform + "\x00" + model
		if supported, ok := modelSupportMemo[memoKey]; ok {
			return supported
		}
		if !poolLoaded[targetPlatform] {
			accounts, err := s.listModelAvailabilityCandidatesForPlatform(ctx, groupID, targetPlatform)
			if err == nil {
				poolByPlatform[targetPlatform] = accounts
			}
			poolLoaded[targetPlatform] = true
		}
		supported := false
		for i := range poolByPlatform[targetPlatform] {
			if s.isModelSupportedByAccountWithContext(ctx, &poolByPlatform[targetPlatform][i], model) {
				supported = true
				break
			}
		}
		modelSupportMemo[memoKey] = supported
		return supported
	}

	models := make([]string, 0, len(candidates))
	for _, model := range candidates {
		if compositeDiscoveryModelAccessible(routes, model, supportsModel) {
			models = append(models, model)
		}
	}
	return models
}

func compositeDiscoveryModelAccessible(
	routes []CompositeModelRoute,
	model string,
	supportsModel func(platform, model string) bool,
) bool {
	for _, endpoint := range compositeModelsDiscoveryEndpoints {
		if route, ok := matchCompositeRoute(routes, model, endpoint); ok {
			upstreamModel := strings.TrimSpace(route.UpstreamModel)
			if upstreamModel == "" {
				upstreamModel = model
			}
			if supportsModel(route.TargetPlatform, upstreamModel) {
				return true
			}
			continue
		}

		if targetPlatform, ok := DetectModelPlatform(model); ok && supportsModel(targetPlatform, model) {
			return true
		}
	}
	return false
}
