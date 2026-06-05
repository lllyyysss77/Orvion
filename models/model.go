package models

import (
	"net/http"
	"time"
)

type Provider struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Name            string
	Config          string
	Console         string // 控制台地址
	ProxyURL        string `gorm:"column:proxy_url"`         // 访问上游时使用的代理地址（可选）
	ModelsFetchMode string `gorm:"column:models_fetch_mode"` // 模型获取方式：v1_models/api_pricing
	Capabilities    ProviderCapabilities
	// 接口转换配置：enabled=1 时，客户端不支持的接口会转换到 target 对应接口。
	InterfaceConversionEnabled int    `gorm:"column:interface_conversion_enabled"` // 0/1
	InterfaceConversionTarget  string `gorm:"column:interface_conversion_target"`  // chat/responses/messages
}

type AnthropicConfig struct {
	BaseUrl string `json:"base_url"`
	ApiKey  string `json:"api_key"`
	Version string `json:"version"`
}

type Model struct {
	ID              uint `gorm:"primaryKey"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Name            string
	Remark          string
	MaxRetry        int               // 重试次数限制
	TimeOut         int               // 超时时间 单位秒
	IOLog           int               // 是否记录IO (0/1)
	Strategy        string            // 负载均衡策略 默认 lottery
	Breaker         int               // 是否开启熔断 (0/1)
	Status          int               // 是否启用 (0/1)
	FallbackModelID uint              `gorm:"column:fallback_model_id;index"` // 全部提供商失败后的回退模型ID，0表示不回退
	Capabilities    ModelCapabilities // 模型能力类型（JSON 数组）
}

type ModelWithProvider struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ModelID           uint
	ProviderModel     string
	ProviderID        uint
	WithHeader        int        // 是否透传header (0/1)
	Status            int        // 是否启用 (0/1)
	AutoDisabledUntil *time.Time `gorm:"column:auto_disabled_until"`
	CustomerHeaders   string     // 自定义headers (JSON)
	Weight            int
}

type ChatLog struct {
	ID                  uint `gorm:"primaryKey"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
	UUID                string `gorm:"column:uuid"`
	Name                string `gorm:"index"`
	ProviderModel       string `gorm:"index"`
	ProviderName        string `gorm:"index"`
	ModelWithProviderID uint   `gorm:"column:model_with_provider_id;index"`
	Status              string `gorm:"index"` // error or success
	Style               string // 类型
	RequestPath         string `gorm:"column:request_path"` // 请求路径（如 /v1/chat/completions）
	UserAgent           string `gorm:"index"`               // 用户代理
	RemoteIP            string // 访问ip
	AuthKeyID           uint   `gorm:"index"` // 使用的AuthKey ID
	ChatIO              int    // 是否开启IO记录 (0/1)

	Error            string // if status is error, this field will be set
	Retry            int    // 重试次数
	ProxyTimeMs      int    `gorm:"column:proxy_time_ms"`       // 代理耗时(毫秒)
	FirstChunkTimeMs int    `gorm:"column:first_chunk_time_ms"` // 首个chunk耗时(毫秒)
	ChunkTimeMs      int    `gorm:"column:chunk_time_ms"`       // chunk耗时(毫秒)
	Tps              float64
	Size             int // 响应大小 字节
	Usage
}

// TableName 指定表名
func (ChatLog) TableName() string {
	return "chat_logs"
}

func (l ChatLog) WithError(err error) ChatLog {
	l.Error = err.Error()
	l.Status = "error"
	return l
}

type Usage struct {
	PromptTokens        int64   `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens    int64   `json:"completion_tokens" gorm:"column:completion_tokens"`
	TotalTokens         int64   `json:"total_tokens" gorm:"column:total_tokens"`
	CachedTokens        int64   `json:"cached_tokens" gorm:"column:cached_tokens"`
	CacheHitRate        float64 `json:"cache_hit_rate" gorm:"column:cache_hit_rate"`               // 缓存命中率（0-100）
	PromptTokensDetails string  `json:"prompt_tokens_details" gorm:"column:prompt_tokens_details"` // JSON 字符串
	TotalCost           float64 `json:"total_cost" gorm:"column:total_cost"`                       // 总消费
}

type PromptTokensDetails struct {
	CachedTokens              int64 `json:"cached_tokens"`
	CacheWriteTokens          int64 `json:"cache_write_tokens,omitempty"`
	PromptExcludesCachedToken bool  `json:"prompt_excludes_cached_tokens,omitempty"`
	AudioTokens               int64 `json:"audio_tokens"`
}

type ChatIO struct {
	ID                uint `gorm:"primaryKey"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LogId             uint   `gorm:"column:log_id"`
	LogUUID           string `gorm:"column:log_uuid;index"`
	Input             string
	OutputString      string `gorm:"column:output_string"`
	OutputStringArray string `gorm:"column:output_string_array"` // JSON 数组字符串
}

// TableName 指定表名
func (ChatIO) TableName() string {
	return "chat_io"
}

type TelegramAgentMessage struct {
	ID             uint `gorm:"primaryKey"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ChatID         int64  `gorm:"column:chat_id;index:idx_tg_agent_chat_order;index:idx_tg_agent_conversation_order"`
	ConversationID string `gorm:"column:conversation_id;size:96;index:idx_tg_agent_conversation_order"`
	MessageOrder   int    `gorm:"column:message_order;index:idx_tg_agent_conversation_order"`
	Role           string `gorm:"column:role;size:32"`
	Content        string `gorm:"column:content"`
}

func (TelegramAgentMessage) TableName() string {
	return "telegram_agent_messages"
}

type TelegramAgentSession struct {
	ID             uint `gorm:"primaryKey"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ChatID         int64  `gorm:"column:chat_id;uniqueIndex"`
	ConversationID string `gorm:"column:conversation_id;size:96;index"`
}

func (TelegramAgentSession) TableName() string {
	return "telegram_agent_sessions"
}

type TelegramAgentPendingAction struct {
	ID         uint `gorm:"primaryKey"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ChatID     int64     `gorm:"column:chat_id;uniqueIndex"`
	ActionJSON string    `gorm:"column:action_json"`
	Summary    string    `gorm:"column:summary"`
	ExpiresAt  time.Time `gorm:"column:expires_at;index"`
}

func (TelegramAgentPendingAction) TableName() string {
	return "telegram_agent_pending_actions"
}

type TelegramAgentToolCallLog struct {
	ID                   uint `gorm:"primaryKey"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ChatID               int64      `gorm:"column:chat_id;index"`
	ConversationID       string     `gorm:"column:conversation_id;size:96;index"`
	Source               string     `gorm:"column:source;size:32;index"`
	ToolCallID           string     `gorm:"column:tool_call_id;size:128;index"`
	ToolName             string     `gorm:"column:tool_name;size:128;index"`
	Arguments            string     `gorm:"column:arguments"`
	Result               string     `gorm:"column:result"`
	Status               string     `gorm:"column:status;size:64;index"`
	OK                   int        `gorm:"column:ok"`
	Final                int        `gorm:"column:final"`
	RequiresConfirmation int        `gorm:"column:requires_confirmation"`
	ActionKind           string     `gorm:"column:action_kind;size:64"`
	ActionSummary        string     `gorm:"column:action_summary"`
	Error                string     `gorm:"column:error"`
	ConfirmedAt          *time.Time `gorm:"column:confirmed_at"`
	ExecutedAt           *time.Time `gorm:"column:executed_at"`
	CancelledAt          *time.Time `gorm:"column:cancelled_at"`
}

func (TelegramAgentToolCallLog) TableName() string {
	return "telegram_agent_tool_call_logs"
}

type OutputUnion struct {
	OfString      string
	OfStringArray []string `gorm:"serializer:json"`
}

type ReqMeta struct {
	UserAgent string // 用户代理
	RemoteIP  string // 访问ip
	Header    http.Header
}

type AuthKey struct {
	ID         uint `gorm:"primaryKey"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Name       string // 项目名称
	Key        string
	Status     int        // 是否启用 (0/1)
	AllowAll   int        // 是否允许所有模型 (0/1)
	Models     string     // 允许的模型列表 (JSON 数组字符串)
	ExpiresAt  *time.Time // nil=永不过期，有值=具体过期时间
	RpmLimit   int        `gorm:"column:rpm_limit"` // 每分钟请求数限制，0 表示无限制
	UsageCount int64      // 使用次数统计
	TotalCost  float64    `gorm:"column:total_cost"` // 累计费用
	LastUsedAt *time.Time // 最后使用时间
}

// TableName 指定表名
func (AuthKey) TableName() string {
	return "auth_keys"
}
