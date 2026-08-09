package domain

// GroupModelsListConfig controls how a group exposes its /v1/models response.
type GroupModelsListConfig struct {
	Enabled             bool     `json:"enabled"`
	Models              []string `json:"models,omitempty"`
	UseAccessibleModels bool     `json:"use_accessible_models,omitempty"`
}
