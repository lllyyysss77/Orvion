package models

import "gorm.io/gorm"

type Config struct {
	gorm.Model
	Key   string `gorm:"uniqueIndex"` // 配置类型
	Value string // 配置内容
}

const (
	KeyAnthropicCountTokens = "anthropic_count_tokens"
	// KeyAnthropicProxyIP 全局代理 IP 配置（用于覆盖 X-Forwarded-For/X-Real-IP）
	KeyAnthropicProxyIP = "anthropic_proxy_ip"
	// KeyTelegramBreakerAlert 熔断 Telegram 告警配置
	KeyTelegramBreakerAlert = "breaker_alert_tg"
	// KeyModelPriceSync 模型价格同步配置
	KeyModelPriceSync = "model_price_sync"
	// KeySystemLogCleanup 系统日志自动清理配置
	KeySystemLogCleanup = "system_log_cleanup"
	// KeyFirstDeployTime 首次部署时间（用于跨重启统计系统总运行时间），值为 RFC3339 时间字符串（UTC）。
	KeyFirstDeployTime = "first_deploy_time"
	// KeyTotalConsumedAmount 全局累计消费金额（持久化累加，不受日志删除影响）。
	KeyTotalConsumedAmount = "total_consumed_amount"
)

type AnthropicCountTokens struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Version string `json:"version"`
}

type AnthropicProxyIPConfig struct {
	Enabled bool   `json:"enabled"`
	ProxyIP string `json:"proxy_ip"`
}

type TelegramBreakerAlertConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	APIBase  string `json:"api_base"`
	ProxyURL string `json:"proxy_url"`
}

type ModelPriceSyncConfig struct {
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes"`
	SourceURL       string `json:"source_url"`
}

type SystemLogCleanupConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}
