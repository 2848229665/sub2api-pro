package domain

// GroupModelsListConfig controls the group's explicit /v1/models response and
// whether that response is also enforced as a request-model allowlist.
type GroupModelsListConfig struct {
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models,omitempty"`
	// UseAccessibleModels is kept as the persisted/API field name for backward
	// compatibility. Its current meaning is "restrict requests to Models"; it
	// no longer enables account-derived model discovery.
	UseAccessibleModels bool `json:"use_accessible_models,omitempty"`
}
