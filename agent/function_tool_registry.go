package agent

import (
	"context"
	"strconv"
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

func telegramAgentFunctionToolDefinitions(ctx context.Context, cfg models.TelegramAgentConfig, skillQuery string) []telegramAgentFunctionToolDefinition {
	mutationDescriptionSuffix := "该工具会立即执行修改并返回结果。"

	definitions := []telegramAgentFunctionToolDefinition{
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
			Description: "查看 Orvion 提供商列表，可按关键词筛选，返回提供商 URL 和 API Key。",
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
				"limit":          map[string]any{"type": "integer", "description": "最多返回条数，默认 10，最大 200。"},
			},
			Handler: telegramAgentFunctionReadRequestLogs,
		},
		{
			Name:        telegramAgentToolCreateAttachmentFile,
			Description: "创建可发送给 Telegram 用户的本地附件文件。适合生成 SVG、Markdown、JSON、HTML、文本等内容；SVG 应作为 file 附件发送，不要只口头说明已生成。",
			Properties: map[string]any{
				"file_name":       map[string]any{"type": "string", "description": "文件名，只需要文件名本身，例如 cat.svg、report.md。不要传目录路径。"},
				"content":         map[string]any{"type": "string", "description": "完整文件内容。SVG 需要传完整 <svg>...</svg> 文本。"},
				"attachment_kind": map[string]any{"type": "string", "description": "附件类型。SVG 使用 file；普通图片 URL 或真实图片文件才使用 image。默认 file。", "enum": []string{"file", "image"}},
				"caption":         map[string]any{"type": "string", "description": "Telegram 附件说明，可为空。"},
			},
			Required: []string{"file_name", "content"},
			Handler:  telegramAgentFunctionCreateAttachmentFile,
		},
		{
			Name:        telegramAgentToolListAuthKeys,
			Description: "查看 Orvion API Key 列表，只返回项目名称、掩码 Key、状态、权限、RPM、用量和最后使用时间，不返回完整 Key。",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "API Key 项目名称筛选关键词，可为空。"},
				"status": map[string]any{
					"type":        "string",
					"description": "状态筛选。all 表示不过滤状态。",
					"enum":        []string{"all", "enabled", "disabled"},
				},
				"limit": map[string]any{"type": "integer", "description": "最多返回条数，默认 20，最大 50。"},
			},
			Handler: telegramAgentFunctionListAuthKeys,
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
			Name:        telegramAgentToolListScheduledTasks,
			Description: "查看 Orvion TG Agent 定时任务列表，可按名称、内容关键词和状态筛选。",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "任务名称或任务内容关键词，可为空。"},
				"status": map[string]any{
					"type":        "string",
					"description": "状态筛选。all 表示不过滤状态。",
					"enum":        []string{"all", "enabled", "disabled", "running"},
				},
				"limit": map[string]any{"type": "integer", "description": "最多返回条数，默认 20，最大 50。"},
			},
			Handler: telegramAgentFunctionListScheduledTasks,
		},
		{
			Name:        telegramAgentToolCreateScheduledTask,
			Description: "新增 Orvion TG Agent 定时任务。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"name":                 map[string]any{"type": "string", "description": "任务名称。"},
				"prompt":               map[string]any{"type": "string", "description": "任务内容，也就是到时间后让 Agent 执行的自然语言指令。"},
				"enabled":              map[string]any{"type": "boolean", "description": "是否启用，默认 true。"},
				"schedule_type":        map[string]any{"type": "string", "description": "定时类型。interval 表示每隔多少分钟；daily 表示每天固定时间。默认 interval。", "enum": []string{"interval", "daily"}},
				"interval_minutes":     map[string]any{"type": "integer", "description": "间隔分钟数，schedule_type=interval 时使用，默认 60。"},
				"time_of_day":          map[string]any{"type": "string", "description": "每天执行时间，schedule_type=daily 时必填，格式 HH:mm，例如 09:30。"},
				"timezone":             map[string]any{"type": "string", "description": "时区，默认 Local；可传 Asia/Shanghai。"},
				"push_to_conversation": map[string]any{"type": "boolean", "description": "是否把执行结果推送并写入当前 Agent 对话上下文，默认 false。"},
				"chat_id":              map[string]any{"type": "integer", "description": "推送目标 Telegram Chat ID。0 或不传表示使用默认配置。"},
			},
			Required: []string{"name", "prompt"},
			Handler:  telegramAgentFunctionCreateScheduledTask,
		},
		{
			Name:        telegramAgentToolUpdateScheduledTask,
			Description: "修改 Orvion TG Agent 定时任务的名称、内容、计划、启用状态、推送设置或 Chat ID。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"target":               map[string]any{"type": "string", "description": "任务名称或 ID。"},
				"name":                 map[string]any{"type": "string", "description": "新的任务名称，可选。"},
				"prompt":               map[string]any{"type": "string", "description": "新的任务内容，可选。"},
				"enabled":              map[string]any{"type": "boolean", "description": "是否启用，可选。"},
				"schedule_type":        map[string]any{"type": "string", "description": "定时类型：interval 或 daily。", "enum": []string{"interval", "daily"}},
				"interval_minutes":     map[string]any{"type": "integer", "description": "间隔分钟数，schedule_type=interval 时使用。"},
				"time_of_day":          map[string]any{"type": "string", "description": "每天执行时间，格式 HH:mm。"},
				"timezone":             map[string]any{"type": "string", "description": "时区，例如 Local 或 Asia/Shanghai。"},
				"push_to_conversation": map[string]any{"type": "boolean", "description": "是否把执行结果推送并写入 Agent 对话上下文。"},
				"chat_id":              map[string]any{"type": "integer", "description": "推送目标 Telegram Chat ID。0 表示使用默认配置。"},
				"clear_chat_id":        map[string]any{"type": "boolean", "description": "是否清空自定义 Chat ID，改用默认配置。"},
			},
			Required: []string{"target"},
			Handler:  telegramAgentFunctionUpdateScheduledTask,
		},
		{
			Name:        telegramAgentToolSetScheduledTaskStatus,
			Description: "启用或禁用 Orvion TG Agent 定时任务。" + mutationDescriptionSuffix,
			Properties: map[string]any{
				"target":  map[string]any{"type": "string", "description": "任务名称或 ID。"},
				"enabled": map[string]any{"type": "boolean", "description": "true 表示启用，false 表示禁用。"},
			},
			Required: []string{"target", "enabled"},
			Handler:  telegramAgentFunctionSetScheduledTaskStatus,
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
			Description: "查看提供商配置摘要。敏感字段会被隐藏，用于修改前核对当前配置。",
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

	return append(definitions, telegramAgentSkillFunctionToolDefinitions(ctx, cfg, skillQuery)...)
}

func telegramAgentSkillFunctionToolDefinitions(ctx context.Context, cfg models.TelegramAgentConfig, skillQuery string) []telegramAgentFunctionToolDefinition {
	if !telegramAgentSkillsEnabled(cfg) {
		return nil
	}

	skills, err := selectTelegramAgentSkillsForToolContext(ctx, cfg, skillQuery, telegramAgentDynamicSkillContextLimit)
	catalogText := "当前 Skill 目录尚未扫描到可用 Skill。"
	skillNames := []string{}
	if err == nil {
		catalogText = summarizeTelegramAgentSkillToolCatalog(skills)
		for _, skill := range skills {
			if skill.Enabled {
				skillNames = append(skillNames, skill.Name)
			}
		}
	} else {
		catalogText = "当前 Skill 目录扫描失败：" + err.Error()
	}

	listProperties := map[string]any{
		"query": map[string]any{"type": "string", "description": "Skill 名称、描述、触发词或自然语言需求，可为空。"},
		"search_mode": map[string]any{
			"type":        "string",
			"description": "检索方式。keyword 表示关键词匹配；embedding 表示向量相似度检索，配置了远端向量模型时会使用远端向量模型。",
			"enum":        []string{TelegramAgentSkillSearchKeyword, TelegramAgentSkillSearchEmbedding},
		},
		"limit": map[string]any{"type": "integer", "description": "最多返回条数，默认 20，最大 50。"},
	}
	definitions := []telegramAgentFunctionToolDefinition{
		{
			Name:        telegramAgentToolListSkills,
			Description: "查看本地 Agent Skills 列表。Skills 来自本地目录中的 skills.md 或 SKILL.md；支持 keyword 与 embedding 检索。" + catalogText,
			Properties:  listProperties,
			Handler:     telegramAgentFunctionListSkills,
		},
	}
	if len(skillNames) == 0 {
		return definitions
	}

	skillProperty := map[string]any{"type": "string", "description": "Skill 名称。"}
	if len(skillNames) > 0 {
		skillProperty["enum"] = skillNames
	}
	definitions = append(definitions,
		telegramAgentFunctionToolDefinition{
			Name:        telegramAgentToolReadSkill,
			Description: "读取指定 Skill 的说明、Skill 目录、脚本相对路径和绝对路径。需要执行脚本时，先读取 Skill，再使用 run_terminal_command 按绝对路径执行。" + catalogText,
			Properties: map[string]any{
				"skill": skillProperty,
			},
			Required: []string{"skill"},
			Handler:  telegramAgentFunctionReadSkill,
		},
		telegramAgentFunctionToolDefinition{
			Name:        telegramAgentToolRunTerminalCommand,
			Description: "执行本地命令行工具，用于按 Skill 说明运行脚本。请先 read_skill 获取脚本绝对路径，再用 command + command_args + working_dir 结构化执行；不要使用 bash -c/sh -c/zsh -c。执行会直接运行并写入审计日志。" + catalogText,
			Properties: map[string]any{
				"command":      map[string]any{"type": "string", "description": "可执行命令或绝对路径，例如 bash、python3、node 或 /path/to/script。必须是单个命令，参数放 command_args。"},
				"command_args": map[string]any{"type": "array", "description": "命令参数列表，例如 [\"/abs/skill/scripts/search.sh\", \"--query\", \"广州天气\"]。", "items": map[string]any{"type": "string"}},
				"working_dir":  map[string]any{"type": "string", "description": "工作目录。执行 Skill 脚本时通常传 read_skill 返回的 Skill 目录。"},
				"stdin":        map[string]any{"type": "string", "description": "可选 stdin 文本。脚本要求 JSON stdin 时再传。"},
				"timeout_ms":   map[string]any{"type": "integer", "description": "超时时间，默认 30000，最大 120000。"},
			},
			Required: []string{"command"},
			Handler:  telegramAgentFunctionRunTerminalCommand,
		},
	)
	return definitions
}

func summarizeTelegramAgentSkillToolCatalog(skills []telegramAgentSkill) string {
	enabled := make([]telegramAgentSkill, 0, len(skills))
	for _, skill := range skills {
		if skill.Enabled {
			enabled = append(enabled, skill)
		}
	}
	if len(enabled) == 0 {
		return "当前没有与本轮需求明确相关的 Skill；不要用无关 Skill 替代执行。"
	}
	limit := len(enabled)
	if limit > 8 {
		limit = 8
	}
	parts := make([]string, 0, limit)
	for _, skill := range enabled[:limit] {
		scriptNames := make([]string, 0, len(skill.Scripts))
		for _, script := range skill.Scripts {
			scriptNames = append(scriptNames, script.Name)
		}
		if len(scriptNames) == 0 {
			parts = append(parts, skill.Name+"（无脚本）")
			continue
		}
		parts = append(parts, skill.Name+"（脚本："+strings.Join(scriptNames, "、")+"）")
	}
	if len(enabled) > limit {
		parts = append(parts, "还有 "+strconv.Itoa(len(enabled)-limit)+" 个")
	}
	return "当前启用 Skill：" + strings.Join(parts, "；") + "。"
}

func findTelegramAgentFunctionToolDefinition(ctx context.Context, cfg models.TelegramAgentConfig, name string) (telegramAgentFunctionToolDefinition, bool) {
	name = strings.TrimSpace(name)
	for _, tool := range telegramAgentFunctionToolDefinitions(ctx, cfg, "") {
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

func telegramAgentFunctionCreateAttachmentFile(_ context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := createTelegramAgentAttachmentFile(args)
	if err != nil {
		return telegramAgentToolResult(false, "创建附件文件失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionListAuthKeys(ctx context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := listTelegramAgentAuthKeys(ctx, args)
	if err != nil {
		return telegramAgentToolResult(false, "查看 API Key 失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionListSkills(ctx context.Context, _ int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := listTelegramAgentSkills(ctx, cfg, args)
	if err != nil {
		return telegramAgentToolResult(false, "查看 Skills 失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionReadSkill(ctx context.Context, _ int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := readTelegramAgentSkill(ctx, cfg, args)
	if err != nil {
		return telegramAgentToolResult(false, "读取 Skill 失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionRunTerminalCommand(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramRunCommandAction(ctx, chatID, cfg, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备执行命令失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
	if err != nil {
		return telegramAgentToolFinalResult(false, "执行命令失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionCreateAuthKey(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramCreateAuthKeyAction(ctx, chatID, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备新增 API Key 失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
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
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
	if err != nil {
		return telegramAgentToolFinalResult(false, "修改 API Key 失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionListScheduledTasks(ctx context.Context, _ int64, _ models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	text, err := listTelegramAgentScheduledTasks(ctx, args)
	if err != nil {
		return telegramAgentToolResult(false, "查看 Agent 定时任务失败："+err.Error())
	}
	return telegramAgentToolResult(true, text)
}

func telegramAgentFunctionCreateScheduledTask(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramCreateScheduledTaskAction(ctx, chatID, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备新增 Agent 定时任务失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
	if err != nil {
		return telegramAgentToolFinalResult(false, "新增 Agent 定时任务失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionUpdateScheduledTask(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramUpdateScheduledTaskAction(ctx, chatID, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备修改 Agent 定时任务失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
	if err != nil {
		return telegramAgentToolFinalResult(false, "修改 Agent 定时任务失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}

func telegramAgentFunctionSetScheduledTaskStatus(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
	action, err := buildTelegramSetScheduledTaskStatusAction(ctx, chatID, args)
	if err != nil {
		return telegramAgentToolResult(false, "准备 Agent 定时任务状态操作失败："+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
	if err != nil {
		return telegramAgentToolFinalResult(false, "执行 Agent 定时任务状态操作失败："+err.Error())
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
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
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
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
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
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
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
	text, err := prepareOrExecuteTelegramToolAction(ctx, action)
	if err != nil {
		return telegramAgentToolFinalResult(false, "执行提供商配置更新失败："+err.Error())
	}
	return telegramAgentToolFinalResult(true, text)
}
