package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultPriceSyncIntervalMinutes = 1440
	defaultPriceSyncURL             = "https://models.dev/api.json"
)

var modelAliases = map[string][]string{}

type priceAPICost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

type priceAPIModel struct {
	ID   string       `json:"id"`
	Cost priceAPICost `json:"cost"`
}

type priceAPIProvider struct {
	Models map[string]priceAPIModel `json:"models"`
}

type priceAPIResponse map[string]priceAPIProvider

func StartPriceSync(ctx context.Context) {
	pkg.GoSafe("service.price_sync", func() { priceSyncLoop(ctx) })
}

func TriggerModelPriceSync(ctx context.Context) error {
	cfg, err := loadPriceSyncConfig(ctx)
	if err != nil {
		return err
	}
	return syncModelPrices(ctx, cfg.SourceURL)
}

func priceSyncLoop(ctx context.Context) {
	for {
		cfg, err := loadPriceSyncConfig(ctx)
		if err != nil {
			slog.Error("读取模型价格同步配置失败", "error", err)
		}

		if cfg.Enabled {
			if err := syncModelPrices(ctx, cfg.SourceURL); err != nil {
				slog.Error("同步模型价格失败", "error", err)
			}
		}

		interval := time.Duration(cfg.IntervalMinutes) * time.Minute
		if interval <= 0 {
			interval = time.Duration(defaultPriceSyncIntervalMinutes) * time.Minute
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func loadPriceSyncConfig(ctx context.Context) (models.ModelPriceSyncConfig, error) {
	cfg := models.ModelPriceSyncConfig{
		Enabled:         true,
		IntervalMinutes: defaultPriceSyncIntervalMinutes,
		SourceURL:       defaultPriceSyncURL,
	}

	config, err := gorm.G[models.Config](models.DB).
		Where(models.ColumnEquals("key"), models.KeyModelPriceSync).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := json.Unmarshal([]byte(config.Value), &cfg); err != nil {
		return cfg, err
	}

	cfg.SourceURL = strings.TrimSpace(cfg.SourceURL)
	if cfg.SourceURL == "" {
		cfg.SourceURL = defaultPriceSyncURL
	}
	if cfg.IntervalMinutes <= 0 {
		cfg.IntervalMinutes = defaultPriceSyncIntervalMinutes
	}
	return cfg, nil
}

func syncModelPrices(ctx context.Context, sourceURL string) error {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("price api status: %d", res.StatusCode)
	}

	var raw priceAPIResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return err
	}

	providerNames := make([]string, 0, len(raw))
	for provider := range raw {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" {
			continue
		}
		providerNames = append(providerNames, provider)
	}
	sort.Strings(providerNames)

	prices := make([]models.ModelPrice, 0)
	seen := make(map[string]struct{})

	for _, provider := range providerNames {
		providerData, ok := raw[provider]
		if !ok {
			continue
		}

		modelKeys := make([]string, 0, len(providerData.Models))
		for modelKey := range providerData.Models {
			modelKeys = append(modelKeys, modelKey)
		}
		sort.Strings(modelKeys)

		for _, modelKey := range modelKeys {
			modelData := providerData.Models[modelKey]
			modelID := strings.ToLower(strings.TrimSpace(modelData.ID))
			if modelID == "" {
				continue
			}
			appendPriceEntry(&prices, seen, provider, modelID, modelData.Cost)

			aliases := make([]string, 0)
			aliases = append(aliases, generateClaudeAliases(modelID)...)
			if mapped, ok := modelAliases[modelID]; ok {
				aliases = append(aliases, mapped...)
			}

			for _, alias := range aliases {
				alias = strings.ToLower(strings.TrimSpace(alias))
				if alias == "" {
					continue
				}
				appendPriceEntry(&prices, seen, provider, alias, modelData.Cost)
			}
		}
	}

	if len(prices) == 0 {
		return nil
	}

	return models.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "model_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"provider", "input", "output", "cache_read", "cache_write", "updated_at"}),
	}).Create(&prices).Error
}

func appendPriceEntry(target *[]models.ModelPrice, seen map[string]struct{}, provider, modelID string, cost priceAPICost) {
	if _, ok := seen[modelID]; ok {
		return
	}
	seen[modelID] = struct{}{}
	*target = append(*target, models.ModelPrice{
		ModelID:    modelID,
		Provider:   provider,
		Input:      normalizePriceValue(cost.Input),
		Output:     normalizePriceValue(cost.Output),
		CacheRead:  normalizePriceValue(cost.CacheRead),
		CacheWrite: normalizePriceValue(cost.CacheWrite),
	})
}

func normalizePriceValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func generateClaudeAliases(modelID string) []string {
	if !strings.HasPrefix(modelID, "claude-") {
		return nil
	}

	aliases := make([]string, 0, 3)

	pattern1 := regexp.MustCompile(`^claude-(opus|sonnet|haiku)-(\d)-(\d)(-.*)?$`)
	if match := pattern1.FindStringSubmatch(modelID); match != nil {
		modelType := match[1]
		major := match[2]
		minor := match[3]
		suffix := match[4]

		aliases = append(aliases,
			fmt.Sprintf("claude-%s-%s.%s%s", modelType, major, minor, suffix),
			fmt.Sprintf("claude-%s.%s-%s%s", major, minor, modelType, suffix),
			fmt.Sprintf("claude-%s-%s-%s%s", major, minor, modelType, suffix),
		)
		return aliases
	}

	pattern2 := regexp.MustCompile(`^claude-(\d)-(\d)-(opus|sonnet|haiku)(-.*)?$`)
	if match := pattern2.FindStringSubmatch(modelID); match != nil {
		major := match[1]
		minor := match[2]
		modelType := match[3]
		suffix := match[4]

		aliases = append(aliases,
			fmt.Sprintf("claude-%s.%s-%s%s", major, minor, modelType, suffix),
			fmt.Sprintf("claude-%s-%s.%s%s", modelType, major, minor, suffix),
		)
	}

	return aliases
}
