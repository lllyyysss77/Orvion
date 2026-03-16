package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service"
	"github.com/racio/orvion/service/subscription"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

// ProviderRequest represents the request body for creating/updating a provider
type ProviderRequest struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Config          string `json:"config"`
	Console         string `json:"console"`
	ModelsFetchMode string `json:"models_fetch_mode"`
}

const (
	modelsFetchModeV1Models = "v1_models"
	modelsFetchModePricing  = "api_pricing"
)

// ModelRequest represents the request body for creating/updating a model
type ModelRequest struct {
	Name         string   `json:"name"`
	Remark       string   `json:"remark"`
	MaxRetry     int      `json:"max_retry"`
	TimeOut      int      `json:"time_out"`
	IOLog        bool     `json:"io_log"`
	Strategy     string   `json:"strategy"`
	Breaker      bool     `json:"breaker"`
	Capabilities []string `json:"capabilities"`
	InputPrice   *float64 `json:"input_price"`
	OutputPrice  *float64 `json:"output_price"`
}

type ModelWithPrice struct {
	models.Model
	InputPrice  *float64 `json:"InputPrice"`
	OutputPrice *float64 `json:"OutputPrice"`
}

// ModelWithProviderRequest represents the request body for creating/updating a model-provider association
type ModelWithProviderRequest struct {
	ModelID         uint              `json:"model_id"`
	ProviderModel   string            `json:"provider_name"`
	ProviderID      uint              `json:"provider_id"`
	WithHeader      bool              `json:"with_header"`
	CustomerHeaders map[string]string `json:"customer_headers"`
	Weight          int               `json:"weight"`
}

// ModelProviderStatusRequest represents the request body for updating provider status
type ModelProviderStatusRequest struct {
	Status bool `json:"status"`
}

// ModelStatusRequest represents the request body for updating model status
type ModelStatusRequest struct {
	Status bool `json:"status"`
}

// SystemConfigRequest represents the request body for updating system configuration
type SystemConfigRequest struct {
	EnableSmartRouting  bool    `json:"enable_smart_routing"`
	SuccessRateWeight   float64 `json:"success_rate_weight"`
	ResponseTimeWeight  float64 `json:"response_time_weight"`
	DecayThresholdHours int     `json:"decay_threshold_hours"`
	MinWeight           int     `json:"min_weight"`
}

// ConfigValueRequest represents the request body for updating config value
type ConfigValueRequest struct {
	Value string `json:"value" binding:"required"`
}

// GetProviders 获取所有提供商列表（支持名称搜索和类型筛选）
func GetProviders(c *gin.Context) {
	// 筛选参数
	name := c.Query("name")
	providerType := c.Query("type")

	// 构建查询条件
	query := models.DB.Model(&models.Provider{}).WithContext(c.Request.Context())

	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}

	if providerType != "" {
		query = query.Where("type = ?", providerType)
	}

	// 默认按创建时间升序排列（新创建的在后），保证“提供商管理”列表的排行稳定且可预期
	query = query.Order("created_at ASC").Order("id DESC")
	var providers []models.Provider
	if err := query.Find(&providers).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	common.Success(c, providers)
}

func GetProviderModels(c *gin.Context) {
	id := c.Param("id")
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	switch provider.Type {
	case consts.StyleIFlow, consts.StyleIFlowAuths:
		modelList, modelErr := listIFlowProviderModels(c.Request.Context())
		if modelErr != nil {
			common.NotFound(c, "Failed to get models: "+modelErr.Error())
			return
		}
		common.Success(c, modelList)
		return
	case consts.StyleCodexAuths:
		modelList, modelErr := listCodexProviderModels(c.Request.Context())
		if modelErr != nil {
			common.NotFound(c, "Failed to get models: "+modelErr.Error())
			return
		}
		common.Success(c, modelList)
		return
	default:
		modelList, modelErr := listProviderModelsWithMode(c.Request.Context(), provider)
		if modelErr != nil {
			common.NotFound(c, "Failed to get models: "+modelErr.Error())
			return
		}
		common.Success(c, modelList)
	}
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

func listProviderModelsWithMode(ctx context.Context, provider models.Provider) ([]providers.Model, error) {
	mode := normalizeModelsFetchMode(provider.ModelsFetchMode)
	if mode == modelsFetchModePricing {
		return listProviderModelsFromPricing(ctx, provider.Config)
	}
	chatModel, err := providers.New(provider.Type, provider.Config)
	if err != nil {
		return nil, err
	}
	return chatModel.Models(ctx)
}

func listProviderModelsFromPricing(ctx context.Context, configRaw string) ([]providers.Model, error) {
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
	res, err := http.DefaultClient.Do(req)
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

func listIFlowProviderModels(ctx context.Context) ([]providers.Model, error) {
	subscriptions, err := subscription.GetIFlowSubscriptionManager().ListSubscriptions()
	if err != nil {
		return nil, err
	}
	if len(subscriptions) == 0 {
		return nil, subscription.ErrIFlowSubscriptionNotFound
	}
	modelList, err := subscription.GetIFlowSubscriptionManager().ListAvailableModels(ctx, subscriptions[0].ID)
	if err != nil {
		return nil, err
	}
	result := make([]providers.Model, 0, len(modelList))
	for _, model := range modelList {
		result = append(result, providers.Model{
			ID:      model.ID,
			Object:  "model",
			Created: model.Created,
			OwnedBy: model.OwnedBy,
		})
	}
	return result, nil
}

func listCodexProviderModels(ctx context.Context) ([]providers.Model, error) {
	subscriptions, err := subscription.GetCodexSubscriptionManager().ListSubscriptions()
	if err != nil {
		return nil, err
	}
	if len(subscriptions) == 0 {
		return nil, subscription.ErrCodexSubscriptionNotFound
	}
	modelList, err := subscription.GetCodexSubscriptionManager().ListAvailableModels(ctx, subscriptions[0].ID)
	if err != nil {
		return nil, err
	}
	result := make([]providers.Model, 0, len(modelList))
	for _, model := range modelList {
		result = append(result, providers.Model{
			ID:      model.ID,
			Object:  model.Object,
			Created: model.Created,
			OwnedBy: model.OwnedBy,
		})
	}
	return result, nil
}

// CreateProvider 创建提供商
func CreateProvider(c *gin.Context) {
	var req ProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
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

	provider := models.Provider{
		Name:            req.Name,
		Type:            req.Type,
		Config:          req.Config,
		Console:         req.Console,
		ModelsFetchMode: normalizeModelsFetchMode(req.ModelsFetchMode),
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

	// Check if provider exists
	if _, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context()); err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Provider not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	// Update fields
	updates := models.Provider{
		Name:            req.Name,
		Type:            req.Type,
		Config:          req.Config,
		Console:         req.Console,
		ModelsFetchMode: normalizeModelsFetchMode(req.ModelsFetchMode),
	}

	if _, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).Updates(c.Request.Context(), updates); err != nil {
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
func GetModels(c *gin.Context) {
	params, err := common.ParsePaginationWithDefaults(c, 1, 10)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	query := models.DB.Model(&models.Model{})

	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ?", like)
	}

	if strategy := strings.TrimSpace(c.Query("strategy")); strategy != "" {
		switch strategy {
		case consts.BalancerLottery, consts.BalancerRotor:
			query = query.Where("strategy = ?", strategy)
		default:
			common.BadRequest(c, "invalid strategy filter")
			return
		}
	}

	if ioLog := strings.TrimSpace(c.Query("io_log")); ioLog != "" {
		switch ioLog {
		case "true", "false":
			query = query.Where("io_log = ?", ioLog == "true")
		default:
			common.BadRequest(c, "invalid io_log filter")
			return
		}
	}

	if capability := strings.TrimSpace(c.Query("capability")); capability != "" {
		capability = strings.ToLower(capability)
		switch capability {
		case "chat", "vision", "video", "embedding", "rerank":
			query = query.Where("capabilities LIKE ?", fmt.Sprintf("%%\"%s\"%%", capability))
		default:
			common.BadRequest(c, "invalid capability filter")
			return
		}
	}

	list := make([]models.Model, 0)
	total, err := common.PaginateQuery(query.Order("id DESC"), params, &list)
	if err != nil {
		common.InternalServerError(c, "Failed to query models: "+err.Error())
		return
	}

	names := make([]string, 0, len(list))
	for _, model := range list {
		name := strings.ToLower(strings.TrimSpace(model.Name))
		if name != "" {
			names = append(names, name)
		}
	}

	priceMap := make(map[string]models.ModelPrice)
	if len(names) > 0 {
		prices := make([]models.ModelPrice, 0, len(names))
		if err := models.DB.WithContext(c.Request.Context()).
			Where("model_id IN ?", names).
			Find(&prices).Error; err != nil {
			common.InternalServerError(c, "Failed to query model prices: "+err.Error())
			return
		}
		for _, price := range prices {
			priceMap[price.ModelID] = price
		}
	}

	withPrices := make([]ModelWithPrice, 0, len(list))
	for _, model := range list {
		key := strings.ToLower(strings.TrimSpace(model.Name))
		var inputPrice *float64
		var outputPrice *float64
		if price, ok := priceMap[key]; ok {
			input := price.Input
			output := price.Output
			inputPrice = &input
			outputPrice = &output
		}
		withPrices = append(withPrices, ModelWithPrice{
			Model:       model,
			InputPrice:  inputPrice,
			OutputPrice: outputPrice,
		})
	}

	response := common.NewPaginationResponse(withPrices, total, params)
	common.Success(c, response)
}

// GetModelList 返回所有模型列表用于下拉选择等场景
func GetModelList(c *gin.Context) {
	modelsList, err := gorm.G[models.Model](models.DB).Order("id DESC").Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to get models: "+err.Error())
		return
	}

	common.Success(c, modelsList)
}

// CreateModel 创建模型
func CreateModel(c *gin.Context) {
	var req ModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if model exists
	count, err := gorm.G[models.Model](models.DB).Where("name = ?", req.Name).Count(c.Request.Context(), "id")
	if err != nil {
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}
	if count > 0 {
		common.BadRequest(c, fmt.Sprintf("Model: %s already exists", req.Name))
		return
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = consts.BalancerDefault
	}

	ioLog := 0
	if req.IOLog {
		ioLog = 1
	}
	breaker := 0
	if req.Breaker {
		breaker = 1
	}
	capabilities := normalizeModelCapabilities(req.Capabilities)
	if len(capabilities) == 0 {
		capabilities = []string{"chat"}
	}

	model := models.Model{
		Name:         req.Name,
		Remark:       req.Remark,
		MaxRetry:     req.MaxRetry,
		TimeOut:      req.TimeOut,
		IOLog:        ioLog,
		Strategy:     strategy,
		Breaker:      breaker,
		Status:       1,
		Capabilities: models.ModelCapabilities(capabilities),
	}

	if err := gorm.G[models.Model](models.DB).Create(c.Request.Context(), &model); err != nil {
		common.InternalServerError(c, "Failed to create model: "+err.Error())
		return
	}

	if err := upsertModelPrice(c.Request.Context(), model.Name, req.InputPrice, req.OutputPrice); err != nil {
		common.InternalServerError(c, "Failed to save model price: "+err.Error())
		return
	}

	common.Success(c, model)
}

// UpdateModel 更新模型
func UpdateModel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if model exists
	_, err = gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = consts.BalancerDefault
	}

	// Update fields
	ioLog := 0
	if req.IOLog {
		ioLog = 1
	}
	breaker := 0
	if req.Breaker {
		breaker = 1
	}
	capabilities := normalizeModelCapabilities(req.Capabilities)
	if len(capabilities) == 0 {
		capabilities = []string{"chat"}
	}
	updates := models.Model{
		Name:         req.Name,
		Remark:       req.Remark,
		MaxRetry:     req.MaxRetry,
		TimeOut:      req.TimeOut,
		IOLog:        ioLog,
		Strategy:     strategy,
		Breaker:      breaker,
		Capabilities: models.ModelCapabilities(capabilities),
	}

	// 使用 map 更新，避免 GORM 忽略 0 值（例如 IOLog 关闭）
	updateMap := map[string]any{
		"name":         updates.Name,
		"remark":       updates.Remark,
		"max_retry":    updates.MaxRetry,
		"time_out":     updates.TimeOut,
		"io_log":       updates.IOLog,
		"strategy":     updates.Strategy,
		"breaker":      updates.Breaker,
		"capabilities": updates.Capabilities,
	}
	if err := models.DB.WithContext(c.Request.Context()).Model(&models.Model{}).Where("id = ?", id).Updates(updateMap).Error; err != nil {
		common.InternalServerError(c, "Failed to update model: "+err.Error())
		return
	}

	if err := upsertModelPrice(c.Request.Context(), updates.Name, req.InputPrice, req.OutputPrice); err != nil {
		common.InternalServerError(c, "Failed to save model price: "+err.Error())
		return
	}

	// Get updated model
	updatedModel, err := gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model: "+err.Error())
		return
	}

	common.Success(c, updatedModel)
}

// UpdateModelStatus 更新模型启用状态
func UpdateModelStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	status := 0
	if req.Status {
		status = 1
	}

	if _, err := gorm.G[models.Model](models.DB).Where("id = ?", id).Update(c.Request.Context(), "status", status); err != nil {
		common.InternalServerError(c, "Failed to update model status: "+err.Error())
		return
	}

	updatedModel, err := gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model: "+err.Error())
		return
	}

	common.Success(c, updatedModel)
}

// DeleteModel 删除模型
func DeleteModel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.Model](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete model: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Model not found")
		return
	}

	common.Success(c, nil)
}

type ProviderTemplate struct {
	Type        string `json:"type"`
	DisplayName string `json:"display_name,omitempty"`
	Category    string `json:"category,omitempty"` // apikey | auth
	AuthMode    bool   `json:"auth_mode,omitempty"`
	HideConfig  bool   `json:"hide_config,omitempty"`
	Template    string `json:"template"`
}

var template = []ProviderTemplate{
	{
		Type:        "openai",
		DisplayName: "openai",
		Category:    "apikey",
		Template: `{
			"base_url": "https://api.openai.com/v1",
			"api_key": "YOUR_API_KEY"
		}`,
	},
	{
		Type:        "anthropic",
		DisplayName: "anthropic",
		Category:    "apikey",
		Template: `{
			"base_url": "https://api.anthropic.com/v1",
			"api_key": "YOUR_API_KEY",
			"version": "2023-06-01"
		}`,
	},
	{
		Type:        "codex",
		DisplayName: "codex",
		Category:    "apikey",
		Template: `{
			"base_url": "https://api.openai.com/v1",
			"api_key": "YOUR_API_KEY"
		}`,
	},
	{
		Type:        "gemini",
		DisplayName: "gemini",
		Category:    "apikey",
		Template: `{
			"base_url": "https://generativelanguage.googleapis.com/v1beta",
			"api_key": "YOUR_GEMINI_API_KEY"
		}`,
	},
	{
		Type:        "iflow",
		DisplayName: "iflow",
		Category:    "auth",
		AuthMode:    true,
		HideConfig:  true,
		Template: `{
			"auth_mode": "iflow"
		}`,
	},
	{
		Type:        "codex-auths",
		DisplayName: "codex",
		Category:    "auth",
		AuthMode:    true,
		HideConfig:  true,
		Template: `{
			"auth_mode": "codex"
		}`,
	},
}

func GetProviderTemplates(c *gin.Context) {
	common.Success(c, template)
}

// GetModelProviders 获取模型的提供商关联列表
func GetModelProviders(c *gin.Context) {
	modelIDStr := c.Query("model_id")
	if modelIDStr == "" {
		common.BadRequest(c, "model_id query parameter is required")
		return
	}

	modelID, err := strconv.ParseUint(modelIDStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid model_id format")
		return
	}

	modelProviders, err := gorm.G[models.ModelWithProvider](models.DB).Where("model_id = ?", modelID).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	common.Success(c, modelProviders)
}

// GetModelProviderStatus 获取提供商状态信息
func GetModelProviderStatus(c *gin.Context) {
	providerIDStr := c.Query("provider_id")
	modelName := c.Query("model_name")
	providerModel := c.Query("provider_model")

	if providerIDStr == "" || modelName == "" || providerModel == "" {
		common.BadRequest(c, "provider_id, model_name and provider_model query parameters are required")
		return
	}

	providerID, err := strconv.ParseUint(providerIDStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid provider_id format")
		return
	}

	// 获取提供商信息
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", providerID).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve provider: "+err.Error())
		return
	}

	// 获取最近10次请求状态
	logs, err := gorm.G[models.ChatLog](models.DB).
		Where("provider_name = ?", provider.Name).
		Where("provider_model = ?", providerModel).
		Where("name = ?", modelName).
		Limit(10).
		Order("created_at DESC").
		Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve chat log: "+err.Error())
		return
	}

	status := make([]bool, 0)
	for _, log := range logs {
		status = append(status, log.Status == "success")
	}
	slices.Reverse(status)
	common.Success(c, status)
}

// CreateModelProvider 创建模型提供商关联
func CreateModelProvider(c *gin.Context) {
	var req ModelWithProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// 将 CustomerHeaders 转换为 JSON 字符串
	customerHeadersJSON := ""
	if req.CustomerHeaders != nil && len(req.CustomerHeaders) > 0 {
		if jsonBytes, err := json.Marshal(req.CustomerHeaders); err == nil {
			customerHeadersJSON = string(jsonBytes)
		}
	}

	// 将 bool 转换为 int (0/1)
	withHeader := 0
	if req.WithHeader {
		withHeader = 1
	}

	modelProvider := models.ModelWithProvider{
		ModelID:         req.ModelID,
		ProviderModel:   req.ProviderModel,
		ProviderID:      req.ProviderID,
		WithHeader:      withHeader,
		CustomerHeaders: customerHeadersJSON,
		Weight:          req.Weight,
		Status:          1, // 默认启用
	}

	err := gorm.G[models.ModelWithProvider](models.DB).Create(c.Request.Context(), &modelProvider)
	if err != nil {
		common.InternalServerError(c, "Failed to create model-provider association: "+err.Error())
		return
	}

	common.Success(c, modelProvider)
}

// UpdateModelProvider 更新模型提供商关联
func UpdateModelProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelWithProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	slog.Info("UpdateModelProvider", "req", req)

	// 将 CustomerHeaders 转换为 JSON 字符串
	customerHeadersJSON := ""
	if req.CustomerHeaders != nil && len(req.CustomerHeaders) > 0 {
		if jsonBytes, err := json.Marshal(req.CustomerHeaders); err == nil {
			customerHeadersJSON = string(jsonBytes)
		}
	}

	// 将 bool 转换为 int (0/1)
	withHeader := 0
	if req.WithHeader {
		withHeader = 1
	}

	// Check if model-provider association exists
	_, err = gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model-provider association not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	// Update fields
	// 注意：使用 struct Updates 时 GORM 会忽略 0 值，导致 false/0 无法写入（例如勾选框取消不生效）。
	// 由于当前泛型封装的 Updates 仅接受 struct，这里改为逐列 Update，确保 0/空字符串也能落库。
	updatePairs := []struct {
		col string
		val any
	}{
		{"model_id", req.ModelID},
		{"provider_id", req.ProviderID},
		{"provider_model", req.ProviderModel},
		{"with_header", withHeader},
		{"customer_headers", customerHeadersJSON},
		{"weight", req.Weight},
	}
	for _, pair := range updatePairs {
		if _, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).Update(c.Request.Context(), pair.col, pair.val); err != nil {
			common.InternalServerError(c, "Failed to update model-provider association: "+err.Error())
			return
		}
	}

	// Get updated model-provider association
	updatedModelProvider, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model-provider association: "+err.Error())
		return
	}

	common.Success(c, updatedModelProvider)
}

// UpdateModelProviderStatus 切换模型提供商关联启用状态
func UpdateModelProviderStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelProviderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	existing, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model-provider association not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve model-provider association: "+err.Error())
		return
	}

	var status int
	if req.Status {
		status = 1
	} else {
		status = 0
	}
	// 使用 struct 更新会忽略 0 值，导致“禁用（0）”无法落库，这里改为单列 Update。
	if _, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).Update(c.Request.Context(), "status", status); err != nil {
		common.InternalServerError(c, "Failed to update status: "+err.Error())
		return
	}

	existing.Status = status
	common.Success(c, existing)
}

// DeleteModelProvider 删除模型提供商关联
func DeleteModelProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete model-provider association: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Model-provider association not found")
		return
	}

	common.Success(c, nil)
}

type WrapLog struct {
	models.ChatLog
	KeyName string `json:"key_name"`
}

// GetRequestLogs 获取最近的请求日志（支持分页和筛选）
func GetRequestLogs(c *gin.Context) {
	// 解析分页参数
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	// 获取筛选参数
	providerName := c.Query("provider_name")
	name := c.Query("name")
	status := c.Query("status")
	style := c.Query("style")
	authKeyID := c.Query("auth_key_id")

	// 构建查询条件
	query := models.DB.Model(&models.ChatLog{})

	if providerName != "" {
		query = query.Where("provider_name = ?", providerName)
	}

	if name != "" {
		query = query.Where("name = ?", name)
	}

	if status != "" {
		query = query.Where("status = ?", status)
	}

	if style != "" {
		query = query.Where("style = ?", style)
	}

	if authKeyID != "" {
		query = query.Where("auth_key_id = ?", authKeyID)
	}

	// 执行分页查询
	var logs []models.ChatLog
	total, err := common.PaginateQuery(
		query.Order("id DESC"),
		params,
		&logs,
	)
	if err != nil {
		common.InternalServerError(c, "Failed to query logs: "+err.Error())
		return
	}

	keys, err := gorm.G[models.AuthKey](models.DB).Where("id IN ?", lo.Map(logs, func(log models.ChatLog, _ int) uint { return log.AuthKeyID })).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	keyMap := lo.KeyBy(keys, func(key models.AuthKey) uint { return key.ID })

	wrapLogs := make([]WrapLog, 0)
	for _, log := range logs {
		var keyName string
		if key, ok := keyMap[log.AuthKeyID]; ok {
			keyName = key.Name
		}
		if log.AuthKeyID == 0 {
			keyName = "admin"
		}
		wrapLogs = append(wrapLogs, WrapLog{
			ChatLog: log,
			KeyName: keyName,
		})
	}

	// 返回分页响应
	response := common.NewPaginationResponse(wrapLogs, total, params)
	common.Success(c, response)
}

// GetChatIO 查询指定日志的输入输出记录
func GetChatIO(c *gin.Context) {
	id := c.Param("id")

	chatIO, err := gorm.G[models.ChatIO](models.DB).Where("log_id = ?", id).First(c.Request.Context())
	if err != nil {
		common.NotFound(c, "ChatIO not found")
		return
	}

	mode := strings.ToLower(strings.TrimSpace(c.Query("mode")))
	if mode == "" || mode == "full" {
		common.Success(c, chatIO)
		return
	}

	inputLimit := parsePositiveInt(c.Query("input_limit"), 20000)
	outputLimit := parsePositiveInt(c.Query("output_limit"), 20000)
	outputItemsLimit := parsePositiveInt(c.Query("output_items_limit"), 50)

	summary := buildChatIOSummary(chatIO, inputLimit, outputLimit, outputItemsLimit)
	common.Success(c, summary)
}

type chatIOSummary struct {
	ID                   uint     `json:"ID"`
	LogId                uint     `json:"LogId"`
	Input                string   `json:"Input"`
	OutputString         string   `json:"OutputString,omitempty"`
	OutputStringArray    []string `json:"OutputStringArray,omitempty"`
	InputBytes           int      `json:"input_bytes"`
	OutputBytes          int      `json:"output_bytes"`
	OutputItems          int      `json:"output_items"`
	Summary              bool     `json:"summary"`
	TruncatedInput       bool     `json:"truncated_input"`
	TruncatedOutput      bool     `json:"truncated_output"`
	TruncatedOutputItems bool     `json:"truncated_output_items"`
}

func buildChatIOSummary(chatIO models.ChatIO, inputLimit, outputLimit, outputItemsLimit int) chatIOSummary {
	input, inputTruncated := truncateString(chatIO.Input, inputLimit)
	outputBytes := len(chatIO.OutputString)
	outputItems := 0
	outputTruncated := false
	outputItemsTruncated := false
	outputString := ""
	var outputArray []string

	parsed, totalItems, itemsTruncated := parseChatIOStringArray(chatIO.OutputStringArray, outputItemsLimit)
	if len(parsed) > 0 {
		outputItems = totalItems
		outputItemsTruncated = itemsTruncated
		outputArray = make([]string, 0, len(parsed))
		for _, entry := range parsed {
			truncatedEntry, truncated := truncateString(entry, outputLimit)
			if truncated {
				outputTruncated = true
			}
			outputArray = append(outputArray, truncatedEntry)
		}
		outputBytes = len(chatIO.OutputStringArray)
	} else if chatIO.OutputString != "" {
		outputItems = 1
		outputString, outputTruncated = truncateString(chatIO.OutputString, outputLimit)
	}

	return chatIOSummary{
		ID:                   chatIO.ID,
		LogId:                chatIO.LogId,
		Input:                input,
		OutputString:         outputString,
		OutputStringArray:    outputArray,
		InputBytes:           len(chatIO.Input),
		OutputBytes:          outputBytes,
		OutputItems:          outputItems,
		Summary:              true,
		TruncatedInput:       inputTruncated,
		TruncatedOutput:      outputTruncated,
		TruncatedOutputItems: outputItemsTruncated,
	}
}

func parseChatIOStringArray(raw string, limit int) ([]string, int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, 0, false
	}
	if limit <= 0 {
		limit = 1
	}
	const maxParseBytes = 2 * 1024 * 1024
	if len(trimmed) > maxParseBytes {
		return []string{trimmed}, 1, len(trimmed) > maxParseBytes
	}
	var parsed []string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return []string{trimmed}, 1, false
	}
	if len(parsed) <= limit {
		return parsed, len(parsed), false
	}
	return parsed[:limit], len(parsed), true
}

func truncateString(raw string, limit int) (string, bool) {
	if limit <= 0 {
		return raw, false
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw, false
	}
	return string(runes[:limit]) + "\n...(已截断)", true
}

func parsePositiveInt(raw string, defaultValue int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return defaultValue
	}
	return value
}

// GetUserAgents 获取所有不重复的用户代理种类
func GetUserAgents(c *gin.Context) {
	var userAgents []string

	// 查询所有不重复的非空用户代理
	if err := models.DB.Model(&models.ChatLog{}).
		Where("user_agent IS NOT NULL AND user_agent != ''").
		Distinct("user_agent").
		Pluck("user_agent", &userAgents).
		Error; err != nil {
		common.InternalServerError(c, "Failed to query user agents: "+err.Error())
		return
	}

	common.Success(c, userAgents)
}

// GetConfigByKey 获取特定配置
func GetConfigByKey(c *gin.Context) {
	key := c.Param("key")
	config, err := gorm.G[models.Config](models.DB).Where("key = ?", key).First(c.Request.Context())

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 配置不存在，返回空响应
			common.Success(c, map[string]string{
				"key":   key,
				"value": "",
			})
			return
		}
		common.InternalServerError(c, "Failed to get config: "+err.Error())
		return
	}

	common.Success(c, map[string]string{
		"key":   config.Key,
		"value": config.Value,
	})
}

// UpdateConfigByKey 更新配置
func UpdateConfigByKey(c *gin.Context) {
	key := c.Param("key")

	var req ConfigValueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 获取或创建配置记录
	config, err := gorm.G[models.Config](models.DB).Where("key = ?", key).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 创建新配置
			config = models.Config{
				Key:   key,
				Value: req.Value,
			}
			if err := gorm.G[models.Config](models.DB).Create(c.Request.Context(), &config); err != nil {
				common.InternalServerError(c, "Failed to create config: "+err.Error())
				return
			}
		} else {
			common.InternalServerError(c, "Failed to get config: "+err.Error())
			return
		}
	} else {
		// 更新配置值
		config.Value = req.Value
		if _, err := gorm.G[models.Config](models.DB).Where("key = ?", key).Updates(c.Request.Context(), config); err != nil {
			common.InternalServerError(c, "Failed to update config: "+err.Error())
			return
		}
	}

	common.Success(c, map[string]string{
		"key":   config.Key,
		"value": config.Value,
	})
}

// RunModelPriceSync 立刻拉取模型价格表
func RunModelPriceSync(c *gin.Context) {
	if err := service.TriggerModelPriceSync(c.Request.Context()); err != nil {
		common.InternalServerError(c, "Failed to sync model prices: "+err.Error())
		return
	}
	common.Success(c, map[string]any{
		"status": "ok",
	})
}

// CleanLogsRequest 清理日志请求
type CleanLogsRequest struct {
	Type  string `json:"type"`  // "count" 或 "days"
	Value int    `json:"value"` // 数量或天数
}

// CleanLogs 清理日志
func CleanLogs(c *gin.Context) {
	var req CleanLogsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.Value <= 0 {
		common.BadRequest(c, "Value must be greater than 0")
		return
	}

	var deletedCount int64

	switch req.Type {
	case "count":
		// 获取要保留的最小 ID
		var minID uint
		if err := models.DB.Model(&models.ChatLog{}).
			Order("id DESC").
			Limit(req.Value).
			Pluck("id", &[]uint{}).
			Scan(&minID).Error; err != nil {
			common.InternalServerError(c, "Failed to query min ID: "+err.Error())
			return
		}

		if minID == 0 {
			common.Success(c, map[string]any{"deleted_count": 0})
			return
		}

		// 先删除关联的 ChatIO
		if err := models.DB.Unscoped().Where("log_id IN (SELECT id FROM chat_logs WHERE id < ?)", minID).Delete(&models.ChatIO{}).Error; err != nil {
			common.InternalServerError(c, "Failed to delete chat IO: "+err.Error())
			return
		}

		// 删除日志
		result := models.DB.Unscoped().Where("id < ?", minID).Delete(&models.ChatLog{})
		if result.Error != nil {
			common.InternalServerError(c, "Failed to delete logs: "+result.Error.Error())
			return
		}
		deletedCount = result.RowsAffected

	case "days":
		// 计算 N 天前的时间
		cutoffTime := time.Now().AddDate(0, 0, -req.Value)

		// 先删除关联的 ChatIO
		if err := models.DB.Unscoped().Where("log_id IN (SELECT id FROM chat_logs WHERE created_at < ?)", cutoffTime).Delete(&models.ChatIO{}).Error; err != nil {
			common.InternalServerError(c, "Failed to delete chat IO: "+err.Error())
			return
		}

		// 删除日志
		result := models.DB.Unscoped().Where("created_at < ?", cutoffTime).Delete(&models.ChatLog{})
		if result.Error != nil {
			common.InternalServerError(c, "Failed to delete logs: "+result.Error.Error())
			return
		}
		deletedCount = result.RowsAffected

	default:
		common.BadRequest(c, "Invalid type: must be 'count' or 'days'")
		return
	}

	common.Success(c, map[string]any{"deleted_count": deletedCount})
}

func normalizeModelCapabilities(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			continue
		}
		switch value {
		case "embeddings", "embed":
			value = "embedding"
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func upsertModelPrice(ctx context.Context, modelName string, input, output *float64) error {
	if modelName == "" {
		return nil
	}
	if input != nil && *input == 0 {
		input = nil
	}
	if output != nil && *output == 0 {
		output = nil
	}
	if input == nil && output == nil {
		return nil
	}

	key := strings.ToLower(strings.TrimSpace(modelName))
	if key == "" {
		return nil
	}

	var price models.ModelPrice
	exists := true
	if err := models.DB.WithContext(ctx).Where("model_id = ?", key).First(&price).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			exists = false
		} else {
			return err
		}
	}

	updates := models.ModelPrice{
		ModelID:  key,
		Provider: price.Provider,
		Input:    price.Input,
		Output:   price.Output,
	}
	if input != nil {
		updates.Input = *input
	}
	if output != nil {
		updates.Output = *output
	}

	if !exists {
		return models.DB.WithContext(ctx).Create(&updates).Error
	}
	return models.DB.WithContext(ctx).Model(&models.ModelPrice{}).Where("model_id = ?", key).Updates(map[string]any{
		"input":  updates.Input,
		"output": updates.Output,
	}).Error
}
