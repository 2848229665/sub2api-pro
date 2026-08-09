package service

import "strings"

func normalizeGroupModelsListConfig(cfg GroupModelsListConfig) GroupModelsListConfig {
	out := GroupModelsListConfig{
		Enabled:             cfg.Enabled,
		UseAccessibleModels: cfg.UseAccessibleModels,
	}
	if len(cfg.Models) == 0 {
		return out
	}

	seen := make(map[string]struct{}, len(cfg.Models))
	out.Models = make([]string, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out.Models = append(out.Models, model)
	}
	if len(out.Models) == 0 {
		out.Models = nil
	}
	return out
}

func (g *Group) CustomModelsListEnabled() bool {
	return g != nil && g.ModelsListConfig.Enabled && len(g.ModelsListConfig.Models) > 0
}

// ModelsListAccessRestricted reports whether the configured /v1/models list
// is also the request-model allowlist for this group.
func (g *Group) ModelsListAccessRestricted() bool {
	return g != nil && g.ModelsListConfig.UseAccessibleModels
}

// AllowsModelsListModel checks the exact public model ID requested by the
// client. The configured list is authoritative when access restriction is on;
// an empty list therefore denies every model.
func (g *Group) AllowsModelsListModel(model string) bool {
	if !g.ModelsListAccessRestricted() {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, allowed := range g.ModelsListConfig.Models {
		if strings.TrimSpace(allowed) == model {
			return true
		}
	}
	return false
}
