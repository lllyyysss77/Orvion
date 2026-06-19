package handler

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

type AuthKeySummaryRes struct {
	Name             string     `json:"name"`
	KeyMasked        string     `json:"keyMasked"`
	ExpiresAt        *time.Time `json:"expiresAt"`
	ExpireInDays     *int       `json:"expireInDays"`
	TotalCost        float64    `json:"totalCost"`
	TotalRequests    int64      `json:"totalRequests"`
	SuccessRequests  int64      `json:"successRequests"`
	FailureRequests  int64      `json:"failureRequests"`
	TotalTimeMs      int64      `json:"totalTimeMs"`
	PromptTokens     int64      `json:"promptTokens"`
	CompletionTokens int64      `json:"completionTokens"`
	TotalTokens      int64      `json:"totalTokens"`
	InputCost        float64    `json:"inputCost"`
	OutputCost       float64    `json:"outputCost"`
	AllowAll         bool       `json:"allowAll"`
	Models           []string   `json:"models"`
}

// AuthKeySummary 返回 API Key 视角的概览数据
func AuthKeySummary(c *gin.Context) {
	ctx := c.Request.Context()
	authKeyID, ok := ctx.Value(consts.ContextKeyAuthKeyID).(uint)
	if !ok || authKeyID == 0 {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden, "auth key required")
		return
	}

	authKey, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", authKeyID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "auth key not found")
			return
		}
		common.InternalServerError(c, "Failed to load auth key: "+err.Error())
		return
	}

	allowAll, _ := ctx.Value(consts.ContextKeyAllowAllModel).(bool)
	allowedModels := make([]string, 0)
	if !allowAll {
		unique := make(map[string]struct{})
		if raw := ctx.Value(consts.ContextKeyAllowModels); raw != nil {
			if list, ok := raw.([]string); ok {
				for _, name := range list {
					name = strings.TrimSpace(name)
					if name != "" {
						if _, exists := unique[name]; exists {
							continue
						}
						unique[name] = struct{}{}
						allowedModels = append(allowedModels, name)
					}
				}
			}
		}
		sort.Strings(allowedModels)
	}

	agg, err := models.QueryChatLogAuthKeySummary(ctx, authKeyID)
	if err != nil {
		common.InternalServerError(c, "Failed to summarize auth key logs: "+err.Error())
		return
	}
	failureRequests := agg.TotalRequests - agg.SuccessRequests

	modelAgg, err := models.QueryChatLogAuthKeyCompletionByModel(ctx, authKeyID)
	if err != nil {
		common.InternalServerError(c, "Failed to aggregate tokens: "+err.Error())
		return
	}

	outputCost := 0.0
	if len(modelAgg) > 0 {
		modelIDs := make([]string, 0, len(modelAgg))
		for _, item := range modelAgg {
			if item.Model == "" {
				continue
			}
			modelIDs = append(modelIDs, item.Model)
		}

		if len(modelIDs) > 0 {
			prices := make([]models.ModelPrice, 0, len(modelIDs))
			if err := models.DB.WithContext(ctx).
				Where("model_id IN ?", modelIDs).
				Find(&prices).Error; err != nil {
				common.InternalServerError(c, "Failed to query model prices: "+err.Error())
				return
			}

			priceMap := make(map[string]models.ModelPrice, len(prices))
			for _, price := range prices {
				priceMap[strings.ToLower(strings.TrimSpace(price.ModelID))] = price
			}

			const perMillion = 1_000_000.0
			for _, item := range modelAgg {
				modelName := strings.ToLower(strings.TrimSpace(item.Model))
				price, ok := priceMap[modelName]
				if !ok {
					continue
				}
				outputCost += float64(item.Completion) / perMillion * price.Output
			}
		}
	}
	inputCost := agg.TotalCost - outputCost
	if inputCost < 0 {
		inputCost = 0
	}

	var expireInDays *int
	if authKey.ExpiresAt != nil {
		days := int(math.Ceil(authKey.ExpiresAt.Sub(time.Now()).Hours() / 24))
		if days < 0 {
			days = 0
		}
		expireInDays = &days
	}

	common.Success(c, AuthKeySummaryRes{
		Name:             authKey.Name,
		KeyMasked:        maskAuthKey(authKey.Key),
		ExpiresAt:        authKey.ExpiresAt,
		ExpireInDays:     expireInDays,
		TotalCost:        agg.TotalCost,
		TotalRequests:    agg.TotalRequests,
		SuccessRequests:  agg.SuccessRequests,
		FailureRequests:  failureRequests,
		TotalTimeMs:      agg.TotalTime,
		PromptTokens:     agg.Prompt,
		CompletionTokens: agg.Completion,
		TotalTokens:      agg.Total,
		InputCost:        inputCost,
		OutputCost:       outputCost,
		AllowAll:         allowAll,
		Models:           allowedModels,
	})
}

func maskAuthKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "--"
	}
	if len(trimmed) <= 12 {
		return trimmed
	}
	prefix := trimmed[:8]
	suffix := trimmed[len(trimmed)-4:]
	return fmt.Sprintf("%s****%s", prefix, suffix)
}
