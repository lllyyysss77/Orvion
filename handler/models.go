package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service"
)

func OpenAIModelsHandler(c *gin.Context) {
	ctx := c.Request.Context()
	models, err := service.ModelsByTypes(ctx, consts.StyleOpenAI, consts.StyleOpenAIRes, consts.StyleCodexAuths, consts.StyleIFlow, consts.StyleIFlowAuths)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	resModels := make([]providers.Model, 0)
	for _, model := range models {
		resModels = append(resModels, providers.Model{
			ID:      model.Name,
			Object:  "model",
			Created: model.CreatedAt.Unix(),
			OwnedBy: "github.com/racio/orvion",
		})
	}
	common.SuccessRaw(c, providers.ModelList{
		Object: "list",
		Data:   resModels,
	})
}
