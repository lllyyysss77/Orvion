package tools

import (
	"context"
	"errors"

	"github.com/racio/orvion/models"
)

const (
	CategoryModelProvider = "model_provider"
	CategoryLog           = "log"
	CategorySystem        = "system"
	CategoryAuthKey       = "auth_key"
	CategoryScheduledTask = "scheduled_task"
	CategorySkill         = "skill"
)

const MutationDescriptionSuffix = "该工具会立即执行修改并返回结果。"

const (
	NameListModels             = "list_models"
	NameListProviders          = "list_providers"
	NameSetModelStatus         = "set_model_status"
	NameSetModelsStatusBatch   = "set_models_status_batch"
	NameSetProviderStatus      = "set_provider_status"
	NameGetProviderConfig      = "get_provider_config"
	NameUpdateProviderConfig   = "update_provider_config"
	NameReadSystemLogs         = "read_system_logs"
	NameReadRequestLogs        = "read_request_logs"
	NameGetSystemStatus        = "get_system_status"
	NameGetPerformanceStats    = "get_performance_stats"
	NameListImageCache         = "list_image_cache"
	NameDeleteImageCache       = "delete_image_cache"
	NameRefreshImageCache      = "refresh_image_cache"
	NameGetBackgroundTasks     = "get_background_tasks"
	NameTriggerBackgroundTask  = "trigger_background_task"
	NameListAuthKeys           = "list_auth_keys"
	NameCreateAuthKey          = "create_auth_key"
	NameUpdateAuthKey          = "update_auth_key"
	NameListScheduledTasks     = "list_telegram_agent_scheduled_tasks"
	NameCreateScheduledTask    = "create_telegram_agent_scheduled_task"
	NameUpdateScheduledTask    = "update_telegram_agent_scheduled_task"
	NameSetScheduledTaskStatus = "set_telegram_agent_scheduled_task_status"
	NameRunScheduledTask       = "run_telegram_agent_scheduled_task"
	NameListSkills             = "list_skills"
	NameReadSkill              = "read_skill"
	NameRunTerminalCommand     = "run_terminal_command"
)

type Definition struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	Category    string
}

type Runtime struct {
	ResolveConversationID func(context.Context, int64) string
	RunScheduledTask      func(context.Context, int64, models.TelegramAgentScheduledTask) (string, error)
}

func (runtime Runtime) conversationID(ctx context.Context, chatID int64) string {
	if runtime.ResolveConversationID == nil || chatID == 0 {
		return ""
	}
	return runtime.ResolveConversationID(ctx, chatID)
}

func (runtime Runtime) runScheduledTask(ctx context.Context, chatID int64, task models.TelegramAgentScheduledTask) (string, error) {
	if runtime.RunScheduledTask == nil {
		return "", errors.New("Agent 定时任务执行器未注入")
	}
	return runtime.RunScheduledTask(ctx, chatID, task)
}

type FunctionCall struct {
	ID   string
	Name string
}

type ResultPayload struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text"`
	Final bool   `json:"final,omitempty"`
}

type CallArgs struct {
	Query                      string                 `json:"query"`
	Limit                      int                    `json:"limit"`
	Level                      string                 `json:"level"`
	Status                     string                 `json:"status"`
	ProviderName               string                 `json:"provider_name"`
	Model                      string                 `json:"model"`
	RecentMinutes              int                    `json:"recent_minutes"`
	StartAt                    string                 `json:"start_at"`
	EndAt                      string                 `json:"end_at"`
	KeySuffix                  *string                `json:"key_suffix"`
	AllowAll                   *bool                  `json:"allow_all"`
	AuthModels                 []string               `json:"models"`
	ModelKeywords              []string               `json:"model_keywords"`
	ExpiresAt                  *string                `json:"expires_at"`
	ClearExpiresAt             bool                   `json:"clear_expires_at"`
	RPMLimit                   *int                   `json:"rpm_limit"`
	TaskPrompt                 *string                `json:"prompt"`
	ScheduleType               *string                `json:"schedule_type"`
	IntervalMinutes            *int                   `json:"interval_minutes"`
	TimeOfDay                  *string                `json:"time_of_day"`
	Timezone                   *string                `json:"timezone"`
	PushToConversation         *bool                  `json:"push_to_conversation"`
	ChatID                     *int64                 `json:"chat_id"`
	ClearChatID                bool                   `json:"clear_chat_id"`
	Target                     string                 `json:"target"`
	Enabled                    *bool                  `json:"enabled"`
	Bulk                       bool                   `json:"bulk"`
	Items                      []ModelStatusBatchItem `json:"items"`
	Name                       *string                `json:"name"`
	Config                     *string                `json:"config"`
	ConfigUpdates              map[string]any         `json:"config_updates"`
	RemoveConfigKeys           []string               `json:"remove_config_keys"`
	Console                    *string                `json:"console"`
	ProxyURL                   *string                `json:"proxy_url"`
	ModelsFetchMode            *string                `json:"models_fetch_mode"`
	Capabilities               *[]string              `json:"capabilities"`
	InterfaceConversionEnabled *bool                  `json:"interface_conversion_enabled"`
	InterfaceConversionTarget  *string                `json:"interface_conversion_target"`
	Skill                      string                 `json:"skill"`
	Command                    string                 `json:"command"`
	CommandArgs                []string               `json:"command_args"`
	WorkingDir                 string                 `json:"working_dir"`
	Stdin                      *string                `json:"stdin"`
	TimeoutMs                  int                    `json:"timeout_ms"`
	CacheID                    uint64                 `json:"cache_id"`
	All                        bool                   `json:"all"`
	ClearExisting              *bool                  `json:"clear_existing"`
	Task                       string                 `json:"task"`
}

type ModelStatusBatchItem struct {
	Target  string `json:"target"`
	Enabled *bool  `json:"enabled"`
	Bulk    bool   `json:"bulk"`
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integerProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func booleanProperty(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func stringArrayProperty(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func enumStringProperty(description string, values ...string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        values,
	}
}
