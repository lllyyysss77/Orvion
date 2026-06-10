package models

import "time"

type Config struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	Key       string `gorm:"uniqueIndex"` // 配置类型
	Value     string // 配置内容
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
	// KeyGitHubVersionCheck GitHub 版本更新检查配置
	KeyGitHubVersionCheck = "github_version_check"
	// KeyUILoadingStyle 全局加载 UI 样式配置
	KeyUILoadingStyle = "ui_loading_style"
	// KeyFirstDeployTime 首次部署时间（用于跨重启统计系统总运行时间），值为 RFC3339 时间字符串（UTC）。
	KeyFirstDeployTime = "first_deploy_time"
	// KeyTotalConsumedAmount 全局累计消费金额（持久化累加，不受日志删除影响）。
	KeyTotalConsumedAmount = "total_consumed_amount"
	// KeyTelegramDailyUsageReportLastSentDate TG 每日使用日报最近一次发送的自然日（本地时区 yyyy-mm-dd）。
	KeyTelegramDailyUsageReportLastSentDate = "tg_daily_usage_report_last_sent_date"
	// KeyProviderStatusSnapshotPrefix 提供商整体关闭前的启用关联快照前缀。
	KeyProviderStatusSnapshotPrefix = "provider_status_snapshot:"
	// KeyTelegramAgent TG 流式对话 Agent 配置。
	KeyTelegramAgent = "telegram_agent"
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
	Enabled        bool   `json:"enabled"`
	BotToken       string `json:"bot_token"`
	ChatID         string `json:"chat_id"`
	APIBase        string `json:"api_base"`
	ProxyURL       string `json:"proxy_url"`
	StatusImageURL string `json:"status_image_url"`
}

type TelegramAgentConfig struct {
	Enabled                  *bool    `json:"enabled,omitempty"`
	BaseURL                  string   `json:"base_url"`
	APIKey                   string   `json:"api_key"`
	Model                    string   `json:"model"`
	SystemPrompt             string   `json:"system_prompt"`
	MaxHistoryMessages       int      `json:"max_history_messages"`
	MaxTokens                int      `json:"max_tokens"`
	Temperature              *float64 `json:"temperature,omitempty"`
	EditIntervalMs           int      `json:"edit_interval_ms"`
	ToolConfirmationRequired *bool    `json:"tool_confirmation_required,omitempty"`
	SkillsEnabled            *bool    `json:"skills_enabled,omitempty"`
	SkillsDir                string   `json:"-"`
	SkillsEmbeddingModel     string   `json:"skills_embedding_model,omitempty"`
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

type GitHubVersionCheckConfig struct {
	Enabled bool `json:"enabled"`
}

type UILoadingStyleConfig struct {
	Style string `json:"style"`
}
