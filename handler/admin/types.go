package admin

import (
	"time"

	"github.com/racio/orvion/models"
)

// ProviderRequest represents the request body for creating/updating a provider
type ProviderRequest struct {
	Name            string   `json:"name"`
	Config          string   `json:"config"`
	Console         string   `json:"console"`
	ProxyID         uint     `json:"proxy_id"`
	ProxyURL        string   `json:"proxy_url"`
	ModelsFetchMode string   `json:"models_fetch_mode"`
	Capabilities    []string `json:"capabilities"`
	// 接口转换：启用后，不支持的接口会转为 target 指定接口能力。
	InterfaceConversionEnabled bool   `json:"interface_conversion_enabled"`
	InterfaceConversionTarget  string `json:"interface_conversion_target"`
}

type ProviderStatusRequest struct {
	Status bool `json:"status"`
}

type ProviderListItem struct {
	models.Provider
	ProxyName                 string `json:"ProxyName"`
	ProviderEnabled           bool   `json:"ProviderEnabled"`
	ProviderModelCount        int    `json:"ProviderModelCount"`
	EnabledProviderModelCount int    `json:"EnabledProviderModelCount"`
}

type ProxyRequest struct {
	Name     string `json:"name"`
	ProxyURL string `json:"proxy_url"`
}

type ProxyListItem struct {
	models.Proxy
	UsageCount int64 `json:"UsageCount"`
}

const (
	modelsFetchModeV1Models = "v1_models"
	modelsFetchModePricing  = "api_pricing"
	pricingFetchTimeout     = 20 * time.Second
)

// ModelRequest represents the request body for creating/updating a model
type ModelRequest struct {
	Name            string   `json:"name"`
	Remark          string   `json:"remark"`
	MaxRetry        int      `json:"max_retry"`
	TimeOut         int      `json:"time_out"`
	IOLog           bool     `json:"io_log"`
	Strategy        string   `json:"strategy"`
	Breaker         bool     `json:"breaker"`
	FallbackModelID uint     `json:"fallback_model_id"`
	Capabilities    []string `json:"capabilities"`
	InputPrice      *float64 `json:"input_price"`
	OutputPrice     *float64 `json:"output_price"`
	CacheRead       *float64 `json:"cache_read_price"`
	CacheWrite      *float64 `json:"cache_write_price"`
}

type ModelWithPrice struct {
	models.Model
	InputPrice      *float64 `json:"InputPrice"`
	OutputPrice     *float64 `json:"OutputPrice"`
	CacheReadPrice  *float64 `json:"CacheReadPrice"`
	CacheWritePrice *float64 `json:"CacheWritePrice"`
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

type TelegramAgentModelsRequest struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}
