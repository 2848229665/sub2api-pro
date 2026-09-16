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
	if apiKey == nil || apiKey.Group == nil {
		return true
	}
	return apiKey.Group.ModelAllowlist.Allows(groupModelsListAccessModel(c, model))
}

func groupModelsListModelNotFoundMessage(c *gin.Context, model string) string {
	return fmt.Sprintf(
		"Model %q is not available for this group",
		strings.TrimSpace(groupModelsListAccessModel(c, model)),
	)
}
