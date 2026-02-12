package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func MergeUsage(current models.Usage, fallback models.Usage) models.Usage {
	if current.PromptTokens == 0 {
		current.PromptTokens = fallback.PromptTokens
	}
	if current.CompletionTokens == 0 {
		current.CompletionTokens = fallback.CompletionTokens
	}
	if current.TotalTokens == 0 {
		current.TotalTokens = fallback.TotalTokens
	}
	if current.CachedTokens == 0 {
		current.CachedTokens = fallback.CachedTokens
	}
	if current.PromptTokensDetails == "" && fallback.PromptTokensDetails != "" {
		current.PromptTokensDetails = fallback.PromptTokensDetails
	}
	return current
}

func EstimateOutputSize(output *models.OutputUnion) int {
	if output == nil {
		return 0
	}
	if output.OfString != "" {
		return len(output.OfString)
	}
	if len(output.OfStringArray) == 0 {
		return 0
	}

	size := 0
	for _, item := range output.OfStringArray {
		size += len(item)
	}
	return size
}

func CalculateTotalCost(ctx context.Context, modelName string, usage models.Usage) float64 {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if modelName == "" {
		return 0
	}

	price, err := loadModelPrice(ctx, modelName)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Warn("获取模型价格失败", "model", modelName, "error", err)
		}
		return 0
	}

	details := parsePromptTokensDetails(usage.PromptTokensDetails)
	cachedTokens := usage.CachedTokens
	if cachedTokens <= 0 {
		cachedTokens = details.CachedTokens
	}
	if cachedTokens < 0 {
		cachedTokens = 0
	}

	cacheWriteTokens := details.CacheWriteTokens
	if cacheWriteTokens < 0 {
		cacheWriteTokens = 0
	}

	billableInput := usage.PromptTokens
	if !details.PromptExcludesCachedToken {
		if cachedTokens > usage.PromptTokens {
			cachedTokens = usage.PromptTokens
		}
		billableInput = usage.PromptTokens - cachedTokens
	}

	const perMillion = 1_000_000.0
	total := float64(billableInput)/perMillion*price.Input +
		float64(usage.CompletionTokens)/perMillion*price.Output +
		float64(cachedTokens)/perMillion*price.CacheRead +
		float64(cacheWriteTokens)/perMillion*price.CacheWrite
	if total < 0 {
		return 0
	}
	return total
}

func loadModelPrice(ctx context.Context, modelName string) (models.ModelPrice, error) {
	price, err := gorm.G[models.ModelPrice](models.DB).Where("model_id = ?", modelName).First(ctx)
	return price, err
}

func parsePromptTokensDetails(details string) models.PromptTokensDetails {
	raw := strings.TrimSpace(details)
	if raw == "" {
		return models.PromptTokensDetails{}
	}
	var parsed models.PromptTokensDetails
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return models.PromptTokensDetails{}
	}
	if parsed.CachedTokens < 0 {
		parsed.CachedTokens = 0
	}
	if parsed.CacheWriteTokens < 0 {
		parsed.CacheWriteTokens = 0
	}
	return parsed
}
