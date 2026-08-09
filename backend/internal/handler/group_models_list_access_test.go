package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupModelsListAllowsModel(t *testing.T) {
	apiKey := &service.APIKey{Group: &service.Group{
		ModelsListConfig: service.GroupModelsListConfig{
			UseAccessibleModels: true,
			Models:              []string{"gpt-5.4", "claude-sonnet-4-6"},
		},
	}}

	require.True(t, groupModelsListAllowsModel(nil, apiKey, "gpt-5.4"))
	require.False(t, groupModelsListAllowsModel(nil, apiKey, "gpt-5.5"))
	require.Contains(t, groupModelsListModelNotFoundMessage(nil, "gpt-5.5"), "/v1/models")
}

func TestGrokCountTokens_ModelsListRestrictionUsesPublicModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	run := func(t *testing.T, models []string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(
			http.MethodPost,
			"/v1/messages/count_tokens",
			strings.NewReader(`{"model":"grok-3","messages":[{"role":"user","content":"hello"}]}`),
		)
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(service.WithCompositeRouteDecision(req.Context(), service.CompositeRouteDecision{
			Matched:        true,
			PublicModel:    "public-grok",
			TargetPlatform: service.PlatformGrok,
			UpstreamModel:  "grok-3",
		}))
		c.Request = req
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{
			ModelsListConfig: service.GroupModelsListConfig{
				UseAccessibleModels: true,
				Models:              models,
			},
		}})

		(&OpenAIGatewayHandler{}).GrokCountTokens(c)
		return rec
	}

	t.Run("listed public model is allowed after composite rewrite", func(t *testing.T) {
		rec := run(t, []string{"public-grok"})
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "input_tokens")
	})

	t.Run("unlisted public model is rejected", func(t *testing.T) {
		rec := run(t, []string{"other-model"})
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Contains(t, rec.Body.String(), "model_not_found")
		require.Contains(t, rec.Body.String(), "public-grok")
	})
}
