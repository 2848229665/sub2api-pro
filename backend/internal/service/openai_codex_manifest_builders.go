package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	configuredCodexModelPriority       = 50
	configuredCodexCustomDescription   = "Custom model routed through Sub2API."
	configuredCodexFallbackContext     = 272_000
	configuredCodexDeepSeekV4Context   = 1_000_000
	configuredCodexGrokContext         = 500_000
	configuredCodexGrokBuildContext    = 256_000
	configuredCodexGPT56MaxContext     = 872_000
	configuredCodexToolOutputMaxTokens = 10_000
)

func (s *OpenAIGatewayService) BuildGroupConfiguredCodexModelsManifest(
	ctx context.Context,
	group *Group,
	ifNoneMatch string,
) (*CodexModelsManifest, bool, error) {
	if s == nil || s.accountRepo == nil || group == nil || group.Platform != PlatformOpenAI {
		return nil, false, nil
	}

	visible, catalog, err := loadCodexGroupCatalogAccounts(ctx, s.accountRepo, group.ID)
	if err != nil {
		return nil, false, fmt.Errorf("load group configured Codex models: %w", err)
	}
	configuredModels := openAIConfiguredCodexModelIDsForGroup(visible, group)
	if len(configuredModels) == 0 {
		return nil, false, nil
	}

	body, err := buildCodexModelsManifestForAccounts(
		PlatformOpenAI,
		configuredModels,
		catalog,
		nil,
		true,
	)
	if err != nil {
		return nil, false, fmt.Errorf("initialize group configured Codex models: %w", err)
	}
	body, _, err = mergeConfiguredCodexModelsManifest(
		body,
		nil,
		group.ModelsListConfig.Models,
		group.CustomModelsListEnabled(),
	)
	if err != nil {
		return nil, false, fmt.Errorf("build group configured Codex models: %w", err)
	}
	manifest := &CodexModelsManifest{
		Body: body,
		ETag: codexModelsManifestBodyETag(body),
	}
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		manifest.Body = nil
		manifest.NotModified = true
	}
	return manifest, true, nil
}

func (s *OpenAIGatewayService) MergeGroupConfiguredCodexModels(
	ctx context.Context,
	group *Group,
	manifest *CodexModelsManifest,
	ifNoneMatch string,
) error {
	if s == nil || s.accountRepo == nil || group == nil || manifest == nil || manifest.NotModified {
		return nil
	}
	if group.Platform != PlatformOpenAI || len(manifest.Body) == 0 {
		return nil
	}

	configuredModels, err := s.groupConfiguredCodexModelIDs(ctx, group)
	if err != nil {
		return fmt.Errorf("load group configured Codex models: %w", err)
	}
	body, changed, err := mergeConfiguredCodexModelsManifest(
		manifest.Body,
		configuredModels,
		group.ModelsListConfig.Models,
		group.CustomModelsListEnabled(),
	)
	if err != nil {
		return fmt.Errorf("merge group configured Codex models: %w", err)
	}
	if changed {
		manifest.Body = body
		manifest.ETag = codexModelsManifestBodyETag(body)
	}
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		manifest.Body = nil
		manifest.NotModified = true
	}
	return nil
}

func (s *OpenAIGatewayService) groupConfiguredCodexModelIDs(ctx context.Context, group *Group) ([]string, error) {
	if group == nil {
		return nil, nil
	}
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, group.ID)
	if err != nil {
		return nil, err
	}
	return openAIConfiguredCodexModelIDsForGroup(accounts, group), nil
}

func loadCodexGroupCatalogAccounts(ctx context.Context, repo AccountRepository, groupID int64) (visible []Account, catalog []Account, err error) {
	if repo == nil {
		return nil, nil, nil
	}
	visible, err = repo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}
	catalog = visible
	groupAccounts, listErr := repo.ListModelAvailabilityCandidates(
		ctx,
		&groupID,
		[]string{
			PlatformAnthropic,
			PlatformOpenAI,
			PlatformGemini,
			PlatformAntigravity,
			PlatformGrok,
			PlatformKimi,
			PlatformZhipu,
			PlatformDeepseek,
		},
		false,
	)
	if listErr != nil {
		return visible, catalog, nil
	}
	return visible, groupAccounts, nil
}

func openAIConfiguredCodexModelIDs(accounts []Account) []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != PlatformOpenAI {
			continue
		}
		for modelID := range account.GetModelMapping() {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" || strings.Contains(modelID, "*") {
				continue
			}
			if _, exists := seen[modelID]; exists {
				continue
			}
			seen[modelID] = struct{}{}
			models = append(models, modelID)
		}
	}
	sort.Strings(models)
	return models
}

func openAIConfiguredCodexModelIDsForGroup(accounts []Account, group *Group) []string {
	models := openAIConfiguredCodexModelIDs(accounts)
	if group == nil || !group.CustomModelsListEnabled() {
		return models
	}

	seen := make(map[string]struct{}, len(models)+len(group.ModelsListConfig.Models))
	for _, modelID := range models {
		seen[modelID] = struct{}{}
	}
	for _, selectedModel := range group.ModelsListConfig.Models {
		selectedModel = strings.TrimSpace(selectedModel)
		if selectedModel == "" || strings.Contains(selectedModel, "*") {
			continue
		}
		for i := range accounts {
			account := &accounts[i]
			if account.Platform != PlatformOpenAI {
				continue
			}
			mappedModel, matched := account.ResolveMappedModel(selectedModel)
			if !matched || strings.TrimSpace(mappedModel) == "" {
				continue
			}
			if _, exists := seen[selectedModel]; !exists {
				seen[selectedModel] = struct{}{}
				models = append(models, selectedModel)
			}
			break
		}
	}
	sort.Strings(models)
	return models
}

func mergeConfiguredCodexModelsManifest(
	body []byte,
	configuredModels []string,
	selectedModels []string,
	filterBySelection bool,
) ([]byte, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false, err
	}
	var upstreamModels []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &upstreamModels); err != nil {
		return nil, false, err
	}

	selected := make(map[string]struct{}, len(selectedModels))
	for _, modelID := range selectedModels {
		modelID = strings.TrimSpace(modelID)
		if modelID != "" {
			selected[modelID] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(upstreamModels)+len(configuredModels))
	merged := make([]json.RawMessage, 0, len(upstreamModels)+len(configuredModels))
	changed := false
	for _, rawModel := range upstreamModels {
		var descriptor struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(rawModel, &descriptor); err != nil || strings.TrimSpace(descriptor.Slug) == "" {
			if filterBySelection {
				changed = true
				continue
			}
			merged = append(merged, rawModel)
			continue
		}
		descriptor.Slug = strings.TrimSpace(descriptor.Slug)
		if isCodexDedicatedMediaModel(descriptor.Slug) {
			changed = true
			continue
		}
		if filterBySelection {
			if _, allowed := selected[descriptor.Slug]; !allowed {
				changed = true
				continue
			}
		}
		if strings.HasPrefix(descriptor.Slug, codexAutoModelPrefix) {
			_, explicitlyEnabled := selected[descriptor.Slug]
			explicitlyEnabled = filterBySelection && explicitlyEnabled
			if !explicitlyEnabled {
				changed = true
				continue
			}
			visibleModel, visibilityChanged, err := codexModelWithVisibility(rawModel, "list")
			if err != nil {
				return nil, false, err
			}
			rawModel = visibleModel
			changed = changed || visibilityChanged
		}
		seen[descriptor.Slug] = struct{}{}
		merged = append(merged, rawModel)
	}

	for _, modelID := range configuredModels {
		if isCodexDedicatedMediaModel(modelID) {
			continue
		}
		if filterBySelection {
			if _, allowed := selected[modelID]; !allowed {
				continue
			}
		}
		if strings.HasPrefix(modelID, codexAutoModelPrefix) {
			if _, explicitlyEnabled := selected[modelID]; !filterBySelection || !explicitlyEnabled {
				continue
			}
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		rawModel, err := json.Marshal(newConfiguredCodexModelDescriptor(modelID))
		if err != nil {
			return nil, false, err
		}
		merged = append(merged, rawModel)
		seen[modelID] = struct{}{}
		changed = true
	}
	if !changed {
		return body, false, nil
	}

	rawModels, err := json.Marshal(merged)
	if err != nil {
		return nil, false, err
	}
	envelope["models"] = rawModels
	mergedBody, err := json.Marshal(envelope)
	if err != nil {
		return nil, false, err
	}
	return mergedBody, true, nil
}

func codexModelWithVisibility(rawModel json.RawMessage, visibility string) (json.RawMessage, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawModel, &fields); err != nil {
		return nil, false, err
	}
	var current string
	if rawVisibility, ok := fields["visibility"]; ok {
		if err := json.Unmarshal(rawVisibility, &current); err == nil && current == visibility {
			return rawModel, false, nil
		}
	}
	rawVisibility, err := json.Marshal(visibility)
	if err != nil {
		return nil, false, err
	}
	fields["visibility"] = rawVisibility
	updated, err := json.Marshal(fields)
	if err != nil {
		return nil, false, err
	}
	return updated, true, nil
}

func buildCodexModelsManifest(modelIDs []string, imageInputModels map[string]bool, metadataModels map[string]string, modelMetadata map[string]codexModelMetadataOverride) ([]byte, error) {
	seen := make(map[string]struct{}, len(modelIDs))
	models := make([]configuredCodexModelDescriptor, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		metadataModelID := strings.TrimSpace(metadataModels[modelID])
		if metadataModelID == "" {
			metadataModelID = modelID
		}
		if isCodexDedicatedMediaModel(modelID) || isCodexDedicatedMediaModel(metadataModelID) {
			continue
		}
		seen[modelID] = struct{}{}
		descriptor := newConfiguredCodexModelDescriptor(metadataModelID)
		descriptor.Slug = modelID
		if imageInputModels[modelID] {
			descriptor.InputModalities = []string{"text", "image"}
		}
		if metadata, ok := modelMetadata[modelID]; ok {
			applyUpstreamModelMetadataToCodexDescriptor(&descriptor, metadata)
		}
		if metadataModelID != modelID {
			descriptor.DisplayName = modelID
			descriptor.Description = configuredCodexCustomDescription
		}
		models = append(models, descriptor)
	}
	return json.Marshal(struct {
		Models []configuredCodexModelDescriptor `json:"models"`
	}{Models: models})
}

func BuildCodexModelsManifest(modelIDs []string) ([]byte, error) {
	return buildCodexModelsManifest(modelIDs, nil, nil, nil)
}

func (s *GatewayService) BuildCodexModelsManifestForGroup(
	ctx context.Context,
	group *Group,
	platformOverride string,
	modelIDs []string,
) ([]byte, error) {
	if s == nil || s.accountRepo == nil || group == nil {
		return BuildCodexModelsManifest(modelIDs)
	}
	effectivePlatform := strings.TrimSpace(platformOverride)
	if effectivePlatform == "" {
		effectivePlatform = group.Platform
	}
	if effectivePlatform != PlatformComposite && !isConcreteRequestPlatform(effectivePlatform) {
		return BuildCodexModelsManifest(modelIDs)
	}

	_, catalog, err := loadCodexGroupCatalogAccounts(ctx, s.accountRepo, group.ID)
	if err != nil {
		return BuildCodexModelsManifest(modelIDs)
	}
	var compositeRoutes []CompositeModelRoute
	compositeRoutesAvailable := true
	if effectivePlatform == PlatformComposite && s.compositeResolver != nil && s.compositeResolver.repo != nil {
		compositeRoutes, err = s.compositeResolver.repo.ListByGroup(ctx, group.ID, false)
		if err != nil {
			compositeRoutesAvailable = false
		}
	}
	return buildCodexModelsManifestForAccounts(
		effectivePlatform,
		modelIDs,
		catalog,
		compositeRoutes,
		compositeRoutesAvailable,
	)
}

func buildCodexModelsManifestForAccounts(
	effectivePlatform string,
	modelIDs []string,
	accounts []Account,
	compositeRoutes []CompositeModelRoute,
	compositeRoutesAvailable bool,
) ([]byte, error) {
	imageInputModels := make(map[string]bool, len(modelIDs))
	metadataModels := codexCatalogMetadataModels(
		effectivePlatform,
		modelIDs,
		accounts,
		compositeRoutes,
		compositeRoutesAvailable,
	)
	modelMetadata := make(map[string]codexModelMetadataOverride, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if groupCodexModelSupportsImageInput(
			effectivePlatform,
			modelID,
			accounts,
			compositeRoutes,
			compositeRoutesAvailable,
		) {
			imageInputModels[modelID] = true
		}
		if metadata, ok := groupCodexModelMetadata(
			effectivePlatform,
			modelID,
			accounts,
			compositeRoutes,
			compositeRoutesAvailable,
		); ok {
			modelMetadata[modelID] = metadata
		}
	}
	return buildCodexModelsManifest(modelIDs, imageInputModels, metadataModels, modelMetadata)
}

func codexCatalogMetadataModels(
	platform string,
	modelIDs []string,
	accounts []Account,
	compositeRoutes []CompositeModelRoute,
	compositeRoutesAvailable bool,
) map[string]string {
	metadataModels := make(map[string]string, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		metadataModelID := resolveCodexCatalogMetadataModel(
			platform,
			modelID,
			accounts,
			compositeRoutes,
			compositeRoutesAvailable,
		)
		if metadataModelID != "" && metadataModelID != modelID {
			metadataModels[modelID] = metadataModelID
		}
	}
	return metadataModels
}

func resolveCodexCatalogMetadataModel(
	platform string,
	modelID string,
	accounts []Account,
	compositeRoutes []CompositeModelRoute,
	compositeRoutesAvailable bool,
) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	if platform == PlatformComposite {
		if !compositeRoutesAvailable {
			return modelID
		}
		if route, matched := matchCompositeRoute(compositeRoutes, modelID, CompositeRouteEndpointResponses); matched {
			if upstreamModel := strings.TrimSpace(route.UpstreamModel); upstreamModel != "" {
				return upstreamModel
			}
			return modelID
		}
		if codexCompositeRouteMatchesModel(compositeRoutes, modelID) {
			return modelID
		}

		claimedPlatforms := make(map[string]struct{})
		for _, account := range accounts {
			accountPlatform := strings.TrimSpace(account.Platform)
			if !isConcreteRequestPlatform(accountPlatform) || !codexExplicitModelMappingClaims(account, modelID) {
				continue
			}
			claimedPlatforms[accountPlatform] = struct{}{}
		}
		if len(claimedPlatforms) > 1 {
			return modelID
		}
		for accountPlatform := range claimedPlatforms {
			return uniqueCodexMappedModel(accounts, accountPlatform, modelID)
		}

		detectedPlatform, detected := DetectModelPlatform(modelID)
		if !detected {
			return modelID
		}
		platform = detectedPlatform
	}
	return uniqueCodexMappedModel(accounts, platform, modelID)
}

func uniqueCodexMappedModel(accounts []Account, platform string, modelID string) string {
	targets := make(map[string]struct{})
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform {
			continue
		}
		mappedModel, matched := account.ResolveMappedModel(modelID)
		mappedModel = strings.TrimSpace(mappedModel)
		if !matched || mappedModel == "" {
			continue
		}
		targets[mappedModel] = struct{}{}
	}
	if len(targets) != 1 {
		return modelID
	}
	for target := range targets {
		return target
	}
	return modelID
}

func groupCodexModelSupportsImageInput(
	platform string,
	modelID string,
	accounts []Account,
	compositeRoutes []CompositeModelRoute,
	compositeRoutesAvailable bool,
) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}
	upstreamModel := modelID
	if platform == PlatformComposite {
		var resolved bool
		platform, upstreamModel, resolved = resolveCodexCompositeModelTarget(
			modelID,
			accounts,
			compositeRoutes,
			compositeRoutesAvailable,
		)
		if !resolved {
			return false
		}
	}
	if platform != PlatformOpenAI && platform != PlatformGrok {
		return false
	}

	candidates := 0
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != platform || !account.IsModelSupported(upstreamModel) {
			continue
		}
		candidates++
		if !accountCodexModelSupportsImageInput(account, account.GetMappedModel(upstreamModel)) {
			return false
		}
	}
	return candidates > 0
}

func accountCodexModelSupportsImageInput(account *Account, upstreamModel string) bool {
	if account == nil {
		return false
	}
	switch account.Platform {
	case PlatformOpenAI:
		if metadata, ok := account.GetUpstreamModelMetadata(upstreamModel); ok {
			if modalities := normalizeCodexInputModalities(metadata.InputModalities); len(modalities) > 0 {
				return stringSliceContains(modalities, "image")
			}
		}
		if !isOpenAICodexImageInputModel(upstreamModel) {
			return false
		}
		if account.IsOpenAIOAuth() {
			return true
		}
		if !account.IsOpenAIApiKey() {
			return false
		}
		return true
	case PlatformGrok:
		if !isOfficialGrokCodexBaseURL(account.GetGrokBaseURL()) {
			return false
		}
		canonical := xai.ResolveGrokTextResponsesModelID(upstreamModel)
		return isGrokCodexImageInputModel(canonical)
	default:
		return false
	}
}

func isGrokCodexImageInputModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "grok-4.3",
		"grok-4.5",
		"grok-4.6",
		"grok-build-0.1",
		"grok-4.20-0309-reasoning",
		"grok-4.20-0309-non-reasoning",
		"grok-4.20-multi-agent-0309":
		return true
	default:
		return false
	}
}

func newConfiguredCodexModelDescriptor(modelID string) configuredCodexModelDescriptor {
	modelID = strings.TrimSpace(modelID)
	noReasoningLevel := "none"
	descriptor := configuredCodexModelDescriptor{
		Slug:                              modelID,
		DisplayName:                       modelID,
		Description:                       configuredCodexCustomDescription,
		DefaultReasoningLevel:             &noReasoningLevel,
		SupportedReasoningLevels:          []configuredCodexReasoningLevel{{Effort: "none", Description: configuredCodexReasoningLevelDescription("none")}},
		ShellType:                         "unified_exec",
		Visibility:                        "list",
		SupportedInAPI:                    true,
		Priority:                          configuredCodexModelPriority,
		AdditionalSpeedTiers:              []string{},
		ServiceTiers:                      []configuredCodexServiceTier{},
		ModelMessages:                     configuredCodexModelMessages{InstructionsTemplate: openai.CodexBaseInstructionsForModel(modelID)},
		SupportsReasoningSummaryParameter: true,
		DefaultReasoningSummary:           "auto",
		WebSearchToolType:                 "text",
		TruncationPolicy:                  configuredCodexTruncationPolicy{Mode: "bytes", Limit: configuredCodexToolOutputMaxTokens},
		ContextWindow:                     configuredCodexFallbackContext,
		MaxContextWindow:                  configuredCodexFallbackContext,
		EffectiveContextWindowPercent:     95,
		ExperimentalSupportedTools:        []string{},
		InputModalities:                   []string{"text"},
	}

	if isDeepSeekCodexModel(modelID) {
		defaultReasoningLevel := "high"
		descriptor.DisplayName = deepSeekCodexDisplayName(modelID)
		descriptor.Description = "DeepSeek coding and reasoning model routed through Sub2API."
		descriptor.DefaultReasoningLevel = &defaultReasoningLevel
		descriptor.SupportedReasoningLevels = []configuredCodexReasoningLevel{
			{Effort: "low", Description: "Fast responses with lighter reasoning"},
			{Effort: "high", Description: "Greater reasoning depth for coding and agent tasks"},
			{Effort: "max", Description: "Maximum reasoning depth for complex tasks"},
		}
		descriptor.SupportsParallelToolCalls = true
		descriptor.ContextWindow = configuredCodexDeepSeekV4Context
		descriptor.MaxContextWindow = configuredCodexDeepSeekV4Context
	}

	if isGrokCodexModel(modelID) {
		descriptor.DisplayName = grokCodexDisplayName(modelID)
		descriptor.Description = "Grok coding and reasoning model routed through Sub2API."
		descriptor.SupportsParallelToolCalls = true
		descriptor.ContextWindow = grokCodexContextWindow(modelID)
		descriptor.MaxContextWindow = descriptor.ContextWindow
		if grokCodexSupportsReasoningEffort(modelID) {
			defaultReasoningLevel := "high"
			descriptor.DefaultReasoningLevel = &defaultReasoningLevel
			descriptor.SupportedReasoningLevels = configuredCodexGrokReasoningLevels(modelID)
		}
	}

	if isClaudeCodexModel(modelID) {
		descriptor.DisplayName = claudeCodexDisplayName(modelID)
		descriptor.Description = "Claude coding and reasoning model routed through Sub2API."
		descriptor.SupportsParallelToolCalls = true
		if levels := configuredCodexClaudeReasoningLevels(modelID); len(levels) > 0 {
			defaultReasoningLevel := claudeCodexDefaultReasoningLevel(levels)
			descriptor.DefaultReasoningLevel = &defaultReasoningLevel
			descriptor.SupportedReasoningLevels = levels
		}
	}

	if isOpenAICodexGPTModel(modelID) {
		descriptor.DisplayName = openaiCodexDisplayName(modelID)
		descriptor.Description = "OpenAI GPT coding model routed through Sub2API."
		descriptor.SupportsParallelToolCalls = true
		if configuredCodexSupportsPriorityServiceTier(modelID) {
			descriptor.ServiceTiers = []configuredCodexServiceTier{{
				ID: "priority", Name: "Fast", Description: "Priority processing for lower latency.",
			}}
		}
		if isOpenAICodexReasoningGPTModel(modelID) {
			defaultReasoningLevel := "medium"
			if getNormalizedCodexModel(modelID) == "gpt-5.6-sol" {
				defaultReasoningLevel = "low"
			}
			descriptor.DefaultReasoningLevel = &defaultReasoningLevel
			descriptor.SupportedReasoningLevels = configuredCodexGPTReasoningLevels(modelID)
			descriptor.DefaultReasoningSummary = "none"
			descriptor.TruncationPolicy = configuredCodexTruncationPolicy{Mode: "tokens", Limit: configuredCodexToolOutputMaxTokens}
			if isOpenAIGPT56Model(modelID) {
				descriptor.MaxContextWindow = configuredCodexGPT56MaxContext
			}
		}
		if SupportsVerbosity(modelID) {
			defaultVerbosity := "low"
			descriptor.SupportVerbosity = true
			descriptor.DefaultVerbosity = &defaultVerbosity
		}
	}

	return descriptor
}

func configuredCodexSupportsPriorityServiceTier(modelID string) bool {
	normalized := canonicalizeOpenAIModelAliasSpelling(modelID)
	for _, family := range []string{"gpt-5.4", "gpt-5.5", "gpt-5.6"} {
		if normalized == family || strings.HasPrefix(normalized, family+"-") {
			return true
		}
	}
	return false
}

func configuredCodexGrokReasoningLevels(modelID string) []configuredCodexReasoningLevel {
	levels := []configuredCodexReasoningLevel{
		{Effort: "low", Description: "Fast responses with lighter reasoning"},
		{Effort: "medium", Description: "Balanced reasoning for most coding tasks"},
		{Effort: "high", Description: "Greater reasoning depth for coding and agent tasks"},
	}
	if GrokSupportsXHighReasoningEffort(modelID) {
		levels = append(levels, configuredCodexReasoningLevel{Effort: "xhigh", Description: "Extra-high reasoning depth for difficult tasks"})
	}
	return levels
}

func configuredCodexClaudeReasoningLevels(modelID string) []configuredCodexReasoningLevel {
	descriptions := map[string]string{
		"low":    "Fast responses with lighter reasoning",
		"medium": "Balanced reasoning for most coding tasks",
		"high":   "Greater reasoning depth for coding and agent tasks",
		"xhigh":  "Extra-high reasoning depth for difficult tasks",
		"max":    "Maximum reasoning depth for complex tasks",
	}
	levels := claude.EffortLevelsForModel(modelID)
	out := make([]configuredCodexReasoningLevel, 0, len(levels))
	for _, effort := range levels {
		out = append(out, configuredCodexReasoningLevel{Effort: effort, Description: descriptions[effort]})
	}
	return out
}

func claudeCodexDefaultReasoningLevel(levels []configuredCodexReasoningLevel) string {
	for _, preferred := range []string{"medium", "high", "low"} {
		for _, level := range levels {
			if level.Effort == preferred {
				return preferred
			}
		}
	}
	if len(levels) == 0 {
		return ""
	}
	return levels[0].Effort
}

func configuredCodexGPTReasoningLevels(modelID string) []configuredCodexReasoningLevel {
	levels := []configuredCodexReasoningLevel{
		{Effort: "low", Description: "Fast responses with lighter reasoning"},
		{Effort: "medium", Description: "Balanced reasoning for most coding tasks"},
		{Effort: "high", Description: "Greater reasoning depth for coding and agent tasks"},
		{Effort: "xhigh", Description: "Extra-high reasoning depth for difficult tasks"},
	}
	normalized := getNormalizedCodexModel(modelID)
	if isOpenAIGPT56Model(modelID) {
		levels = append(levels, configuredCodexReasoningLevel{Effort: "max", Description: "Maximum reasoning depth for complex tasks"})
	}
	if normalized == "gpt-5.6-sol" || normalized == "gpt-5.6-terra" {
		levels = append(levels, configuredCodexReasoningLevel{Effort: "ultra", Description: "Maximum reasoning with automatic task delegation"})
	}
	return levels
}

func isOpenAICodexGPTModel(modelID string) bool {
	normalized := canonicalizeOpenAIModelAliasSpelling(modelID)
	if normalized == "" || strings.HasPrefix(normalized, "gpt-image") {
		return false
	}
	return strings.HasPrefix(normalized, "gpt-")
}

func isOpenAICodexReasoningGPTModel(modelID string) bool {
	normalized := canonicalizeOpenAIModelAliasSpelling(modelID)
	return strings.HasPrefix(normalized, "gpt-5")
}

func isOpenAICodexImageInputModel(modelID string) bool {
	normalized := canonicalizeOpenAIModelAliasSpelling(modelID)
	return strings.HasPrefix(normalized, "gpt-5") ||
		strings.HasPrefix(normalized, "gpt-4o") ||
		strings.HasPrefix(normalized, "gpt-4.1") ||
		strings.HasPrefix(normalized, "gpt-4.5") ||
		strings.HasPrefix(normalized, "gpt-4-turbo") ||
		strings.HasPrefix(normalized, "gpt-4-vision")
}

func isOfficialOpenAICodexCatalogModel(modelID string) bool {
	normalized := strings.ToLower(codexProviderQualifiedModelID(modelID))
	if normalized == "" || isCodexDedicatedMediaModel(normalized) {
		return false
	}
	if strings.HasPrefix(normalized, "codex-") {
		return true
	}
	if strings.HasPrefix(normalized, "o1") || strings.HasPrefix(normalized, "o3") || strings.HasPrefix(normalized, "o4") {
		return true
	}
	if !strings.HasPrefix(normalized, "gpt-") {
		return false
	}
	for _, incompatibleFamily := range []string{"audio", "realtime", "transcribe", "tts"} {
		if strings.Contains(normalized, incompatibleFamily) {
			return false
		}
	}
	return true
}

func openaiCodexDisplayName(modelID string) string {
	normalized := canonicalizeOpenAIModelAliasSpelling(modelID)
	if normalized == "" {
		return modelID
	}
	for _, model := range openai.DefaultModels {
		if strings.EqualFold(model.ID, normalized) && strings.TrimSpace(model.DisplayName) != "" {
			return model.DisplayName
		}
	}
	return modelID
}

func deepSeekCodexDisplayName(modelID string) string {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "deepseek-v4-pro", "deepseek-4-pro":
		return "DeepSeek V4 Pro"
	case "deepseek-v4-flash", "deepseek-4-flash":
		return "DeepSeek V4 Flash"
	default:
		return modelID
	}
}

func isDeepSeekCodexModel(modelID string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "deepseek-")
}

func isGrokCodexModel(modelID string) bool {
	return xai.IsGrokModelID(modelID)
}

func grokCodexSupportsReasoningEffort(modelID string) bool {
	if grokSupportsReasoningEffort(modelID) {
		return true
	}
	canonical := xai.ResolveGrokTextResponsesModelID(modelID)
	if canonical == "" || strings.EqualFold(canonical, modelID) {
		return false
	}
	return grokSupportsReasoningEffort(canonical)
}

func grokCodexDisplayName(modelID string) string {
	normalized := strings.ToLower(xai.StripGrokProviderPrefix(strings.TrimSpace(modelID)))
	if normalized == "" {
		return modelID
	}
	if name := grokDefaultDisplayName(normalized); name != "" {
		return name
	}
	canonical := strings.ToLower(xai.ResolveGrokTextResponsesModelID(normalized))
	if canonical != "" && canonical != normalized {
		if name := grokDefaultDisplayName(canonical); name != "" {
			return name
		}
	}
	return modelID
}

func grokDefaultDisplayName(modelID string) string {
	for _, model := range xai.DefaultModels() {
		if model.ID == modelID {
			return strings.TrimSpace(model.DisplayName)
		}
	}
	return ""
}

func grokCodexContextWindow(modelID string) int64 {
	normalized := strings.ToLower(xai.StripGrokProviderPrefix(strings.TrimSpace(modelID)))
	if strings.HasPrefix(normalized, "grok-build") {
		return configuredCodexGrokBuildContext
	}
	return configuredCodexGrokContext
}

func isClaudeCodexModel(modelID string) bool {
	platform, detected := DetectModelPlatform(modelID)
	return detected && platform == PlatformAnthropic
}

func isOfficialGrokCodexBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return false
	}
	return xai.IsOfficialBaseURLHost(strings.TrimSuffix(parsed.Hostname(), "."))
}

func claudeCodexDisplayName(modelID string) string {
	normalized := strings.ToLower(codexProviderQualifiedModelID(modelID))
	normalized = strings.TrimPrefix(normalized, "anthropic.")
	if normalized == "" {
		return modelID
	}
	for _, model := range claude.DefaultModels {
		if strings.EqualFold(model.ID, normalized) && strings.TrimSpace(model.DisplayName) != "" {
			return model.DisplayName
		}
	}
	if canonical, ok := claude.ModelIDOverrides[normalized]; ok {
		for _, model := range claude.DefaultModels {
			if model.ID == canonical && strings.TrimSpace(model.DisplayName) != "" {
				return model.DisplayName
			}
		}
	}
	return modelID
}
