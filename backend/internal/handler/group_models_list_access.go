package handler

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func groupModelsListAccessModel(c *gin.Context, model string) string {
	return clientRequestedModel(c, model)
}

func groupModelsListAllowsModel(c *gin.Context, apiKey *service.APIKey, model string) bool {
	return apiKey == nil || apiKey.Group == nil || apiKey.Group.AllowsModelsListModel(groupModelsListAccessModel(c, model))
}

func groupModelsListModelNotFoundMessage(c *gin.Context, model string) string {
	return fmt.Sprintf(
		"Model %q is not available in this group's /v1/models list",
		strings.TrimSpace(groupModelsListAccessModel(c, model)),
	)
}
