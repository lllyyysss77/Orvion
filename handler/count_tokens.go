package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"gorm.io/gorm"
)

func CountTokens(c *gin.Context) {
	ctx := c.Request.Context()

	config, err := gorm.G[models.Config](models.DB).Where("key = ?", models.KeyAnthropicCountTokens).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Anthropic count tokens config not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve Anthropic count tokens config: "+err.Error())
		return
	}

	var anthropicConfig models.AnthropicCountTokens
	if err := json.Unmarshal([]byte(config.Value), &anthropicConfig); err != nil {
		common.InternalServerError(c, "Failed to parse Anthropic count tokens config: "+err.Error())
		return
	}

	anthropic := providers.Anthropic{
		BaseURL: anthropicConfig.BaseURL,
		APIKey:  anthropicConfig.APIKey,
		Version: anthropicConfig.Version,
	}

	req, err := anthropic.BuildCountTokensReq(ctx, c.Request.Header, c.Request.Body)
	if err != nil {
		common.InternalServerError(c, "Failed to create request: "+err.Error())
		return
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		common.InternalServerError(c, "Failed to send request: "+err.Error())
		return
	}
	defer res.Body.Close()

	c.Status(res.StatusCode)

	for k, values := range res.Header {
		for _, value := range values {
			c.Writer.Header().Add(k, value)
		}
	}
	c.Writer.Flush()

	if _, err := io.Copy(c.Writer, res.Body); err != nil {
		common.InternalServerError(c, "Failed to read response: "+err.Error())
		return
	}
}
