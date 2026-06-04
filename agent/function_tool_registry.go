package agent

import (
	"context"
	"strings"

	"github.com/racio/orvion/models"
)

type telegramAgentFunctionToolHandler func(context.Context, int64, models.TelegramAgentConfig, telegramAgentToolCallArgs) string

type telegramAgentFunctionToolDefinition struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	Handler     telegramAgentFunctionToolHandler
}

func telegramAgentFunctionToolDefinitions(cfg models.TelegramAgentConfig) []telegramAgentFunctionToolDefinition {
	mutationDescriptionSuffix := "该工具不会立即修改，而是创建一个需要用户确认的待执行操作。"
	if !telegramAgentRequiresToolConfirmation(cfg) {
		mutationDescriptionSuffix = "该工具会直接执行修改，不需要用户再次确认。"
	}

	return []telegramAgentFunctionToolDefinition{
		{
			Name:        telegramAgentToolListModels,
			Description: "查看 Orvion 模型列表，可按关键词筛选。",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "模型名称筛选关键词，可为空。"},
			},
			Handler: telegramAgentFunctionListModels,
		},
		{
			Name:        telegramAgentToolListProviders,
			Description: "查看 Orvion 提供商列表，可按关键词筛选。",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "提供商名称筛选关键词，可为空。"},
			},
			Handler: telegramAgentFunctionListProviders,
		},
		{
			Name:        telegramAgentToolReadSystemLogs,
			Description: "读取 Orvion 系统日志尾部内容，适合排查 panic、SQL、启动、熔断、Telegram、更新检查等运行时问题。",
			Properties: map[string]any{
				"level": map[string]any{
					"type":        "string",
					"description": "日志级别筛选。all 表示不过滤级别。",
					"enum":        []string{"all", "debug", "info", "warn", "error"},
				},
				"query": map[string]any{"type": "string", "description": "日志文本关键词，可为空。"},
				"limit": map[string]any{"type": "integer", "description": "最多返回行数，默认 20，最大 80。"},
			},
			Handler: telegramAgentFunctionReadSystemLogs,
		},
		{
			Name:        telegramAgentToolReadRequestLogs,
			Description: "读取 Orvion 请求日志摘要，适合排查模型请求失败、供应商错误、超时、成本和 token 消耗。",
			Properties: map[string]any{
				"status": map[string]any{
					"type":        "string",
					"description": "请求状态筛选。all 表示不过滤状态。",
					"enum":        []string{"all", "success", "error"},
				},
				"provider_name":  map[string]any{"type": "string", "description": "提供商名称关键词，可为空。"},
				"model":          map[string]any{"type": "string", "description": "模型名称或上游模型名称关键词，可为空。"},
				"query":          map[string]any{"type": "string", "description": "任意关键词，会匹配模型、提供商、请求路径、错误、User-Agent、IP 等字段。"},
				"recent_minutes": map[string]any{"type": "integer", "description": "只看最近多少分钟，可为空或 0。"},
				"start_at":       map[string]any{"type": "string", "description": "开始时间，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。"},
				"end_at":         map[string]any{"type": "string", "description": "结束时间，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。"},
				"limit":          map[string]any{"type": "integer", "description": "最多返回条数，默认 10，最大 50。"},
			},
			Handler: telegramAgentFunctionReadRequestLogs,
		},
		{
			Name:        telegramAgentToolCreateAuthKey,
			Description: "新增 Orvion API Key。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"name":       map[string]any{"type": "string", "description": "API Key 项目名称。"},
				"key_suffix": map[string]any{"type": "string", "description": "自定义 Key 后缀，可选。传 abc 会生成 sk-abc；不传则自动生成。"},
				"enabled":    map[string]any{"type": "boolean", "description": "是否启用，默认 true。"},
				"allow_all":  map[string]any{"type": "boolean", "description": "是否允许全部模型，默认 true。false 表示限制模型。"},
				"models":     map[string]any{"type": "array", "description": "允许的精确模型名列表，allow_all=false 时可用。", "items": map[string]any{"type": "string"}},
				"model_keywords": map[string]any{
					"type":        "array",
					"description": "模型名称关键词列表，会按包含匹配多个模型，例如 claude、deepseek。",
					"items":       map[string]any{"type": "string"},
				},
				"expires_at": map[string]any{"type": "string", "description": "有效期，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。可选。"},
				"rpm_limit":  map[string]any{"type": "integer", "description": "每分钟请求数限制，0 表示无限制，默认 0。"},
			},
			Required: []string{"name"},
			Handler:  telegramAgentFunctionCreateAuthKey,
		},
		{
			Name:        telegramAgentToolUpdateAuthKey,
			Description: "修改 Orvion API Key 的名称、启用状态、模型权限、有效期、RPM 或 Key 后缀。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"target":     map[string]any{"type": "string", "description": "API Key 项目名称或 ID。"},
				"name":       map[string]any{"type": "string", "description": "新的项目名称，可选。"},
				"key_suffix": map[string]any{"type": "string", "description": "新的 Key 后缀，可选。传 abc 会改为 sk-abc；不传则不修改 Key。"},
				"enabled":    map[string]any{"type": "boolean", "description": "是否启用，可选。"},
				"allow_all":  map[string]any{"type": "boolean", "description": "是否允许全部模型。false 表示限制模型。"},
				"models":     map[string]any{"type": "array", "description": "允许的精确模型名列表，allow_all=false 时可用。", "items": map[string]any{"type": "string"}},
				"model_keywords": map[string]any{
					"type":        "array",
					"description": "模型名称关键词列表，会按包含匹配多个模型，例如 claude、deepseek。",
					"items":       map[string]any{"type": "string"},
				},
				"expires_at":       map[string]any{"type": "string", "description": "新的有效期，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。"},
				"clear_expires_at": map[string]any{"type": "boolean", "description": "是否清空有效期。"},
				"rpm_limit":        map[string]any{"type": "integer", "description": "每分钟请求数限制，0 表示无限制。"},
			},
			Required: []string{"target"},
			Handler:  telegramAgentFunctionUpdateAuthKey,
		},
		{
			Name:        telegramAgentToolSetModelStatus,
			Description: "启用或禁用模型。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"target":  map[string]any{"type": "string", "description": "模型精确名称、ID 或批量关键词。去掉所有、全部、相关、的等修饰词。"},
				"enabled": map[string]any{"type": "boolean", "description": "true 表示启用，false 表示禁用。"},
				"bulk":    map[string]any{"type": "boolean", "description": "是否按 target 作为关键词批量匹配多个模型。"},
			},
			Required: []string{"target", "enabled"},
			Handler:  telegramAgentFunctionSetModelStatus,
		},
		{
			Name:        telegramAgentToolSetModelsStatusBatch,
			Description: "批量启用或禁用多组模型，适合一句话里同时包含多个模型启停动作。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"items": map[string]any{
					"type":        "array",
					"description": "多个模型启停动作。每个动作必须独立表达 target、enabled 和 bulk。",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"target":  map[string]any{"type": "string", "description": "模型精确名称或批量关键词。去掉所有、全部、相关、的等修饰词。"},
							"enabled": map[string]any{"type": "boolean", "description": "true 表示启用，false 表示禁用。"},
							"bulk":    map[string]any{"type": "boolean", "description": "是否按 target 作为关键词批量匹配多个模型。"},
						},
						"required":             []string{"target", "enabled"},
						"additionalProperties": false,
					},
				},
			},
			Required: []string{"items"},
			Handler:  telegramAgentFunctionSetModelsStatusBatch,
		},
		{
			Name:        telegramAgentToolSetProviderStatus,
			Description: "启用或禁用提供商。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"target":  map[string]any{"type": "string", "description": "提供商名称或 ID。"},
				"enabled": map[string]any{"type": "boolean", "description": "true 表示启用，false 表示禁用。"},
			},
			Required: []string{"target", "enabled"},
			Handler:  telegramAgentFunctionSetProviderStatus,
		},
		{
			Name:        telegramAgentToolGetProviderConfig,
			Description: "查看提供商配置摘要。敏感字段会被隐藏，用于修改前确认当前配置。",
			Properties: map[string]any{
				"target": map[string]any{"type": "string", "description": "提供商名称或 ID。"},
			},
			Required: []string{"target"},
			Handler:  telegramAgentFunctionGetProviderConfig,
		},
		{
			Name:        telegramAgentToolUpdateProviderConfig,
			Description: "修改提供商配置。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"target": map[string]any{"type": "string", "description": "提供商名称或 ID。"},
				"name":   map[string]any{"type": "string", "description": "新的提供商名称，可选。"},
				"config": map[string]any{"type": "string", "description": "完整 provider config JSON，可选。通常优先使用 config_updates 做局部修改。"},
				"config_updates": map[string]any{
					"type":                 "object",
					"description":          "要合并进 provider config JSON 的字段，例如 {\"base_url\":\"https://...\",\"api_key\":\"sk-a,sk-b\"}。",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"remove_config_keys": map[string]any{
					"type":        "array",
					"description": "要从 provider config JSON 中删除的字段名。",
					"items":       map[string]any{"type": "string"},
				},
				"console":   map[string]any{"type": "string", "description": "控制台地址；传空字符串表示清空。"},
				"proxy_url": map[string]any{"type": "string", "description": "代理地址，支持 http/https/socks5；传空字符串表示清空。"},
				"models_fetch_mode": map[string]any{
					"type":        "string",
					"description": "模型获取方式：v1_models 表示通用，api_pricing 表示 NewAPI。",
					"enum":        []string{"v1_models", "api_pricing"},
				},
				"capabilities": map[string]any{
					"type":        "array",
					"description": "提供商支持的接口能力。",
					"items":       map[string]any{"type": "string", "enum": []string{"chat", "openai", "claude"}},
				},
				"interface_conversion_enabled": map[string]any{"type": "boolean", "description": "是否启用接口转换。"},
				"interface_conversion_target": map[string]any{
					"type":        "string",
					"description": "接口转换目标：chat 对应 /v1/chat/completions，responses 对应 /v1/responses，messages 对应 /v1/messages；关闭转换请设置 interface_conversion_enabled=false。",
					"enum":        []string{"chat", "responses", "messages"},
				},
			},
			Required: []string{"target"},
			Handler:  telegramAgentFunctionUpdateProviderConfig,
		},
	}
}

func findTelegramAgentFunctionToolDefinition(cfg models.TelegramAgentConfig, name string) (telegramAgentFunctionToolDefinition, bool) {
	name = strings.TrimSpace(name)
	for _, tool := range telegramAgentFunctionToolDefinitions(cfg) {
		if tool.Name == name {
			return tool, true
		}
	}
	return telegramAgentFunctionToolDefinition{}, false
}

func telegramAgentFunctionListModels(ctx context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := listTelegramAgentModels(ctx, args.Query)
	if err != nil {
		return telegramAgentToolResult(false, "查看模型失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionListProviders(ctx context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := listTelegramAgentProviders(ctx, args.Query)
	if err != nil {
		return telegramAgentToolResult(false, "查看提供商失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionReadSystemLogs(ctx context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := readTelegramAgentSystemLogs(ctx, args)
	if err != nil {
		return telegramAgentToolResult(false, "读取系统日志失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionReadRequestLogs(ctx context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := readTelegramAgentRequestLogs(ctx, args)
	if err != nil {
		return telegramAgentToolResult(false, "读取请求日志失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionCreateAuthKey(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramCreateAuthKeyAction(ctx, chatID, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备新增 API Key 失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action, telegramAgentRequiresToolConfirmation(cfg))
	if err != nil {
		return telegramAgentToolFinalResult(false, "新增 API Key 失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionUpdateAuthKey(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramUpdateAuthKeyAction(ctx, chatID, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备修改 API Key 失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action, telegramAgentRequiresToolConfirmation(cfg))
	if err != nil {
		return telegramAgentToolFinalResult(false, "修改 API Key 失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionSetModelStatus(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	if args.Enabled == nil {
		return telegramAgentToolResult(false, "缺少 enabled 参数")
	}
	target := cleanupTelegramToolTarget(args.Target)
	action, err := buildTelegramSetModelStatusActionWithMode(ctx, chatID, target, *args.Enabled, args.Bulk)
	if err != nil {
		return telegramAgentToolResult(false, "准备模型操作失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action, telegramAgentRequiresToolConfirmation(cfg))
	if err != nil {
		return telegramAgentToolFinalResult(false, "执行模型操作失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionSetModelsStatusBatch(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramSetModelsStatusBatchAction(ctx, chatID, args.Items)
	if err != nil {
		return telegramAgentToolResult(false, "准备批量模型操作失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action, telegramAgentRequiresToolConfirmation(cfg))
	if err != nil {
		return telegramAgentToolFinalResult(false, "执行批量模型操作失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionSetProviderStatus(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	if args.Enabled == nil {
		return telegramAgentToolResult(false, "缺少 enabled 参数")
	}
	target := cleanupTelegramToolTarget(args.Target)
	action, err := buildTelegramSetProviderStatusAction(ctx, chatID, target, *args.Enabled)
	if err != nil {
		return telegramAgentToolResult(false, "准备提供商操作失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action, telegramAgentRequiresToolConfirmation(cfg))
	if err != nil {
		return telegramAgentToolFinalResult(false, "执行提供商操作失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionGetProviderConfig(ctx context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := getTelegramAgentProviderConfigText(ctx, args.Target)
	if err != nil {
		return telegramAgentToolResult(false, "查看提供商配置失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionUpdateProviderConfig(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramUpdateProviderConfigAction(ctx, chatID, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备提供商配置更新失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action, telegramAgentRequiresToolConfirmation(cfg))
	if err != nil {
		return telegramAgentToolFinalResult(false, "执行提供商配置更新失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}
