package handler

import (
	"database/sql"
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

type authKeyTokenAgg struct {
	Model      string `gorm:"column:model"`
	Completion int64  `gorm:"column:completion"`
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

	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{}, "auth_key_id, status, prompt_tokens, completion_tokens, total_tokens, total_cost, proxy_time_ms, name")
	if err != nil {
		common.InternalServerError(c, "Failed to build log query: "+err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, AuthKeySummaryRes{Name: authKey.Name, KeyMasked: maskAuthKey(authKey.Key), AllowAll: allowAll, Models: allowedModels})
		return
	}

	type authKeySummaryAgg struct {
		TotalRequests   int64           `gorm:"column:total_requests"`
		SuccessRequests int64           `gorm:"column:success_requests"`
		Prompt          sql.NullInt64   `gorm:"column:prompt"`
		Completion      sql.NullInt64   `gorm:"column:completion"`
		Total           sql.NullInt64   `gorm:"column:total"`
		TotalCost       sql.NullFloat64 `gorm:"column:total_cost"`
		TotalTime       sql.NullInt64   `gorm:"column:total_time"`
	}
	var agg authKeySummaryAgg
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT COUNT(1) AS total_requests,
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success_requests,
		        COALESCE(SUM(prompt_tokens),0) AS prompt,
		        COALESCE(SUM(completion_tokens),0) AS completion,
		        COALESCE(SUM(total_tokens),0) AS total,
		        COALESCE(SUM(total_cost),0) AS total_cost,
		        COALESCE(SUM(proxy_time_ms),0) AS total_time
		   FROM (`+union.SQL+`) AS logs
		  WHERE auth_key_id = ?`,
		authKeyID,
	).Scan(&agg).Error; err != nil {
		common.InternalServerError(c, "Failed to summarize auth key logs: "+err.Error())
		return
	}
	failureRequests := agg.TotalRequests - agg.SuccessRequests

	modelAgg := make([]authKeyTokenAgg, 0)
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT LOWER(name) AS model, COALESCE(SUM(completion_tokens),0) AS completion
		   FROM (`+union.SQL+`) AS logs
		  WHERE auth_key_id = ?
		  GROUP BY LOWER(name)`,
		authKeyID,
	).Scan(&modelAgg).Error; err != nil {
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
	inputCost := agg.TotalCost.Float64 - outputCost
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
		TotalCost:        agg.TotalCost.Float64,
		TotalRequests:    agg.TotalRequests,
		SuccessRequests:  agg.SuccessRequests,
		FailureRequests:  failureRequests,
		TotalTimeMs:      agg.TotalTime.Int64,
		PromptTokens:     agg.Prompt.Int64,
		CompletionTokens: agg.Completion.Int64,
		TotalTokens:      agg.Total.Int64,
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
