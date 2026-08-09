package service

import "testing"

func TestGroupAllowsModelsListModel(t *testing.T) {
	tests := []struct {
		name  string
		group *Group
		model string
		want  bool
	}{
		{name: "nil group", group: nil, model: "gpt-5.4", want: true},
		{name: "restriction disabled", group: &Group{}, model: "gpt-5.4", want: true},
		{
			name: "exact configured model",
			group: &Group{ModelsListConfig: GroupModelsListConfig{
				UseAccessibleModels: true,
				Models:              []string{" gpt-5.4 ", "claude-sonnet-4-6"},
			}},
			model: "gpt-5.4",
			want:  true,
		},
		{
			name: "unlisted model",
			group: &Group{ModelsListConfig: GroupModelsListConfig{
				UseAccessibleModels: true,
				Models:              []string{"gpt-5.4"},
			}},
			model: "gpt-5.5",
			want:  false,
		},
		{
			name: "empty restricted list fails closed",
			group: &Group{ModelsListConfig: GroupModelsListConfig{
				UseAccessibleModels: true,
			}},
			model: "gpt-5.4",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.group.AllowsModelsListModel(tt.model); got != tt.want {
				t.Fatalf("AllowsModelsListModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}
