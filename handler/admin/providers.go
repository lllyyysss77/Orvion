package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

// GetProviders 获取所有提供商列表（支持名称搜索）
func GetProviders(c *gin.Context) {
	name := c.Query("name")
	ctx := c.Request.Context()
	now := time.Now()
	year, month, day := now.Date()
	startOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location())

	query := models.DB.Model(&models.Provider{}).WithContext(ctx)

	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}

	var providers []models.Provider
	if err := query.
		Order("providers.created_at ASC").
		Order("providers.id DESC").
		Find(&providers).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	// 按“今天使用量”降序排序，使用量相同时保留数据库稳定排序。
	usageRows, err := models.QueryChatLogProviderUsage(ctx, models.ChatLogQueryScope{StartAt: &startOfDay}, "created_at >= ?", startOfDay)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	usageByName := make(map[string]int64, len(usageRows))
	for _, row := range usageRows {
		usageByName[strings.TrimSpace(row.ProviderName)] = row.UsageCount
	}
	sort.SliceStable(providers, func(i, j int) bool {
		return usageByName[providers[i].Name] > usageByName[providers[j].Name]
	})

	items := buildProviderListItems(ctx, providers)
	common.Success(c, items)
}

func buildProviderListItems(ctx context.Context, providers []models.Provider) []ProviderListItem {
	items := make([]ProviderListItem, 0, len(providers))
	if len(providers) == 0 {
		return items
	}

	providerIDs := lo.Map(providers, func(provider models.Provider, _ int) uint {
		return provider.ID
	})
	type providerStatusAgg struct {
		ProviderID   uint `gorm:"column:provider_id"`
		TotalCount   int  `gorm:"column:total_count"`
		EnabledCount int  `gorm:"column:enabled_count"`
	}
	var rows []providerStatusAgg
	if err := models.DB.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Select("provider_id, COUNT(*) AS total_count, COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END),0) AS enabled_count").
		Where("provider_id IN ?", providerIDs).
		Group("provider_id").
		Scan(&rows).Error; err != nil {
		rows = nil
	}

	statsByProviderID := make(map[uint]providerStatusAgg, len(rows))
	for _, row := range rows {
		statsByProviderID[row.ProviderID] = row
	}
	proxyNames := make(map[uint]string)
	proxyIDs := lo.Uniq(lo.FilterMap(providers, func(provider models.Provider, _ int) (uint, bool) {
		return provider.ProxyID, provider.ProxyID > 0
	}))
	if len(proxyIDs) > 0 {
		var proxies []models.Proxy
		if err := models.DB.WithContext(ctx).Where("id IN ?", proxyIDs).Find(&proxies).Error; err == nil {
			for _, proxy := range proxies {
				proxyNames[proxy.ID] = proxy.Name
			}
		}
	}

	for _, provider := range providers {
		stats := statsByProviderID[provider.ID]
		items = append(items, ProviderListItem{
			Provider:                  provider,
			ProxyName:                 proxyNames[provider.ProxyID],
			ProviderEnabled:           provider.Status != 0,
			ProviderModelCount:        stats.TotalCount,
			EnabledProviderModelCount: stats.EnabledCount,
		})
	}
	return items
}

func GetProviderModels(c *gin.Context) {
	id := c.Param("id")
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if err := models.ResolveProviderProxyURL(c.Request.Context(), &provider); err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	modelList, modelErr := listProviderModelsWithMode(c.Request.Context(), provider)
	if modelErr != nil {
		common.NotFound(c, "Failed to get models: "+modelErr.Error())
		return
	}
	common.Success(c, modelList)
}

func GetTelegramAgentDirectModels(c *gin.Context) {
	var req TelegramAgentModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	baseURL, err := normalizeTelegramAgentDirectModelsBaseURL(req.BaseURL)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		common.BadRequest(c, "API Key 不能为空")
		return
	}

	configRaw, err := json.Marshal(map[string]string{
		"base_url": baseURL,
		"api_key":  apiKey,
	})
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	provider, err := providers.NewForStyle(consts.StyleOpenAI, string(configRaw))
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	modelList, err := provider.Models(c.Request.Context())
	if err != nil {
		common.NotFound(c, "Failed to get models: "+err.Error())
		return
	}
	common.Success(c, modelList)
}

func normalizeTelegramAgentDirectModelsBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("请求 URL 不能为空")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("请求 URL 无效: %w", err)
	}
	if strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", errors.New("请求 URL 必须包含 scheme 和 host")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/chat/completions") {
		path = strings.TrimSuffix(path, "/chat/completions")
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeModelsFetchMode(raw string) string {
	mode := strings.TrimSpace(strings.ToLower(raw))
	switch mode {
	case modelsFetchModePricing:
		return modelsFetchModePricing
	case modelsFetchModeV1Models, "":
		return modelsFetchModeV1Models
	default:
		return modelsFetchModeV1Models
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func normalizeProviderCapabilities(values []string) models.ProviderCapabilities {
	return models.NormalizeProviderCapabilities(values)
}

func normalizeInterfaceConversionTarget(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "chat":
		return "chat"
	case "responses":
		return "responses"
	case "messages":
		return "messages"
	default:
		return ""
	}
}

func listProviderModelsWithMode(ctx context.Context, provider models.Provider) ([]providers.Model, error) {
	mode := normalizeModelsFetchMode(provider.ModelsFetchMode)
	if mode == modelsFetchModePricing {
		return listProviderModelsFromPricing(ctx, provider.Config, provider.ProxyURL)
	}
	chatModel, err := providers.NewWithProxy(provider.Config, provider.ProxyURL)
	if err != nil {
		return nil, err
	}
	return chatModel.Models(ctx)
}

func listProviderModelsFromPricing(ctx context.Context, configRaw string, proxyURL string) ([]providers.Model, error) {
	var cfg struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := json.Unmarshal([]byte(configRaw), &cfg); err != nil {
		return nil, fmt.Errorf("invalid provider config: %w", err)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is empty")
	}
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	url := baseURL + "/api/pricing"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}
	httpClient, err := providers.GetClientWithProxy(pricingFetchTimeout, proxyURL)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("status code: %d body: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	modelSet := make(map[string]struct{})
	collectPricingModelIDs(payload, modelSet)
	if len(modelSet) == 0 {
		return []providers.Model{}, nil
	}
	ids := make([]string, 0, len(modelSet))
	for id := range modelSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	models := make([]providers.Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, providers.Model{
			ID:      id,
			Object:  "model",
			Created: 0,
			OwnedBy: "newapi",
		})
	}
	return models, nil
}

func collectPricingModelIDs(node any, out map[string]struct{}) {
	switch value := node.(type) {
	case map[string]any:
		for _, key := range []string{"model_name", "model", "id", "name"} {
			if raw, ok := value[key]; ok {
				if text, ok := raw.(string); ok {
					text = strings.TrimSpace(text)
					if text != "" {
						out[text] = struct{}{}
					}
				}
			}
		}
		for _, key := range []string{"data", "models", "items", "list", "result", "results"} {
			if child, ok := value[key]; ok {
				collectPricingModelIDs(child, out)
			}
		}
	case []any:
		for _, item := range value {
			collectPricingModelIDs(item, out)
		}
	}
}

func sanitizeProviderProxyURL(raw string) (string, error) {
	proxyURL := strings.TrimSpace(raw)
	if proxyURL == "" {
		return "", nil
	}

	lower := strings.ToLower(proxyURL)
	if strings.HasPrefix(lower, "socket5://") {
		proxyURL = "socks5://" + proxyURL[len("socket5://"):]
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", errors.New("proxy URL must include host")
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https", "socks5":
		return proxyURL, nil
	default:
		return "", fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
	}
}

func resolveProviderProxy(ctx context.Context, proxyID uint, legacyProxyURL string) (string, error) {
	if proxyID == 0 {
		return sanitizeProviderProxyURL(legacyProxyURL)
	}
	proxy, err := gorm.G[models.Proxy](models.DB).Where("id = ?", proxyID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("proxy_id %d does not exist", proxyID)
		}
		return "", err
	}
	return proxy.ProxyURL, nil
}

// CreateProvider 创建提供商
func CreateProvider(c *gin.Context) {
	var req ProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	proxyURL, err := resolveProviderProxy(c.Request.Context(), req.ProxyID, req.ProxyURL)
	if err != nil {
		common.BadRequest(c, "Invalid proxy_url: "+err.Error())
		return
	}
	normalizedConfig, err := models.NormalizeProviderConfig(req.Config)
	if err != nil {
		common.BadRequest(c, "Invalid config: "+err.Error())
		return
	}

	// Check if provider exists
	count, err := gorm.G[models.Provider](models.DB).Where("name = ?", req.Name).Count(c.Request.Context(), "id")
	if err != nil {
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	if count > 0 {
		common.BadRequest(c, "Provider already exists")
		return
	}

	normalizedCapabilities := normalizeProviderCapabilities(req.Capabilities)
	if len(normalizedCapabilities) == 0 {
		common.BadRequest(c, "capabilities must include at least one of chat/openai/claude")
		return
	}
	conversionEnabled := 0
	conversionTarget := ""
	if req.InterfaceConversionEnabled {
		conversionEnabled = 1
		conversionTarget = normalizeInterfaceConversionTarget(req.InterfaceConversionTarget)
		if conversionTarget == "" {
			common.BadRequest(c, "interface_conversion_target must be one of chat/responses/messages")
			return
		}
		if !models.ProviderSupportsEndpoint([]string(normalizedCapabilities), conversionTarget) {
			common.BadRequest(c, "interface_conversion_target must be supported by provider capabilities")
			return
		}
	}

	provider := models.Provider{
		Name:                       req.Name,
		Config:                     normalizedConfig,
		Console:                    req.Console,
		ProxyID:                    req.ProxyID,
		ProxyURL:                   proxyURL,
		ModelsFetchMode:            normalizeModelsFetchMode(req.ModelsFetchMode),
		Capabilities:               normalizedCapabilities,
		Status:                     1,
		InterfaceConversionEnabled: conversionEnabled,
		InterfaceConversionTarget:  conversionTarget,
	}

	if err := gorm.G[models.Provider](models.DB).Create(c.Request.Context(), &provider); err != nil {
		common.InternalServerError(c, "Failed to create provider: "+err.Error())
		return
	}

	common.Success(c, provider)
}

// UpdateProvider 更新提供商
func UpdateProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	proxyURL, err := resolveProviderProxy(c.Request.Context(), req.ProxyID, req.ProxyURL)
	if err != nil {
		common.BadRequest(c, "Invalid proxy_url: "+err.Error())
		return
	}
	normalizedConfig, err := models.NormalizeProviderConfig(req.Config)
	if err != nil {
		common.BadRequest(c, "Invalid config: "+err.Error())
		return
	}

	// Check if provider exists
	if _, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context()); err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Provider not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	normalizedCapabilities := normalizeProviderCapabilities(req.Capabilities)
	if len(normalizedCapabilities) == 0 {
		common.BadRequest(c, "capabilities must include at least one of chat/openai/claude")
		return
	}
	conversionEnabled := 0
	conversionTarget := ""
	if req.InterfaceConversionEnabled {
		conversionEnabled = 1
		conversionTarget = normalizeInterfaceConversionTarget(req.InterfaceConversionTarget)
		if conversionTarget == "" {
			common.BadRequest(c, "interface_conversion_target must be one of chat/responses/messages")
			return
		}
		if !models.ProviderSupportsEndpoint([]string(normalizedCapabilities), conversionTarget) {
			common.BadRequest(c, "interface_conversion_target must be supported by provider capabilities")
			return
		}
	}

	updates := map[string]any{
		"name":                         req.Name,
		"config":                       normalizedConfig,
		"console":                      req.Console,
		"proxy_id":                     req.ProxyID,
		"proxy_url":                    proxyURL,
		"models_fetch_mode":            normalizeModelsFetchMode(req.ModelsFetchMode),
		"capabilities":                 normalizedCapabilities,
		"interface_conversion_enabled": conversionEnabled,
		"interface_conversion_target":  conversionTarget,
	}

	if err := models.DB.WithContext(c.Request.Context()).
		Model(&models.Provider{}).
		Where("id = ?", id).
		Updates(updates).Error; err != nil {
		common.InternalServerError(c, "Failed to update provider: "+err.Error())
		return
	}

	// Get updated provider
	updatedProvider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated provider: "+err.Error())
		return
	}

	common.Success(c, updatedProvider)
}

// UpdateProviderStatus 切换提供商整体启用状态，不修改模型关联自身状态。
func UpdateProviderStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID")
		return
	}

	var req ProviderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	ctx := c.Request.Context()
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Provider not found")
			return
		}
		common.InternalServerError(c, "Failed to load provider: "+err.Error())
		return
	}

	status := boolToInt(req.Status)
	if err := models.DB.WithContext(ctx).
		Model(&models.Provider{}).
		Where("id = ?", id).
		Update("status", status).Error; err != nil {
		common.InternalServerError(c, "Failed to update provider status: "+err.Error())
		return
	}

	provider.Status = status
	items := buildProviderListItems(ctx, []models.Provider{provider})
	if len(items) == 0 {
		common.Success(c, provider)
		return
	}
	common.Success(c, items[0])
}

// DeleteProvider 删除提供商
func DeleteProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete provider: "+err.Error())
		return
	}

	//删除关联
	if _, err := gorm.G[models.ModelWithProvider](models.DB).Where("provider_id = ?", id).Delete(c.Request.Context()); err != nil {
		common.InternalServerError(c, "Failed to delete provider: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Provider not found")
		return
	}

	common.Success(c, nil)
}

// GetModels 获取模型列表（支持分页与筛选）

type ProviderTemplate struct {
	DisplayName string `json:"display_name,omitempty"`
	Category    string `json:"category,omitempty"` // apikey | auth
	AuthMode    bool   `json:"auth_mode,omitempty"`
	HideConfig  bool   `json:"hide_config,omitempty"`
	Template    string `json:"template"`
}

var template = []ProviderTemplate{
	{
		DisplayName: "通用",
		Category:    "apikey",
		Template: `{
			"base_url": "https://api.openai.com/v1",
			"api_key": "YOUR_API_KEY"
		}`,
	},
}

func GetProviderTemplates(c *gin.Context) {
	common.Success(c, template)
}
