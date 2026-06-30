package tools

import (
	"context"
	"strings"

	"github.com/racio/orvion/models"
)

func FunctionDefinitions(ctx context.Context, cfg models.TelegramAgentConfig) []Definition {
	definitions := BuiltInDefinitions()
	return append(definitions, skillFunctionDefinitions(ctx, cfg)...)
}

func skillFunctionDefinitions(ctx context.Context, cfg models.TelegramAgentConfig) []Definition {
	if !telegramAgentSkillsEnabled(cfg) {
		return nil
	}

	skills, err := loadTelegramAgentEnabledSkills(ctx, cfg)
	skillNames := []string{}
	if err == nil {
		for _, skill := range skills {
			if skill.Enabled {
				skillNames = append(skillNames, skill.Name)
			}
		}
	}
	return SkillDefinitions(skillNames)
}

func SkillMetadataPrompt(ctx context.Context, cfg models.TelegramAgentConfig) string {
	if !telegramAgentSkillsEnabled(cfg) {
		return ""
	}
	skills, err := loadTelegramAgentEnabledSkills(ctx, cfg)
	if err != nil {
		return "## Skills 元数据\n当前 Skill 目录扫描失败：" + err.Error()
	}
	enabled := make([]telegramAgentSkill, 0, len(skills))
	for _, skill := range skills {
		if skill.Enabled {
			enabled = append(enabled, skill)
		}
	}
	if len(enabled) == 0 {
		return "## Skills 元数据\n当前没有启用的 Skill。"
	}
	lines := []string{
		"## Skills 元数据",
		"以下仅是启用 Skill 的 name/description 元数据，用作触发器；不要据此编造脚本参数或执行结果。需要使用某个 Skill 时，必须先调用 read_skill 加载 SKILL.md Body。",
	}
	for _, skill := range enabled {
		line := "- name: " + skill.Name
		description := strings.Join(strings.Fields(strings.TrimSpace(skill.Description)), " ")
		if description != "" {
			line += " | description: " + description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func ExecuteFunctionTool(ctx context.Context, runtime Runtime, chatID int64, cfg models.TelegramAgentConfig, name string, args CallArgs) string {
	switch strings.TrimSpace(name) {
	case NameListModels:
		text, err := listTelegramAgentModels(ctx, args.Query)
		if err != nil {
			return ToolResult(false, "查看模型失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameListProviders:
		text, err := listTelegramAgentProviders(ctx, args.Query)
		if err != nil {
			return ToolResult(false, "查看提供商失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameReadSystemLogs:
		text, err := readTelegramAgentSystemLogs(ctx, args)
		if err != nil {
			return ToolResult(false, "读取系统日志失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameReadRequestLogs:
		text, err := readTelegramAgentRequestLogs(ctx, args)
		if err != nil {
			return ToolResult(false, "读取请求日志失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameGetSystemStatus:
		return executeSystemTool(ctx, args, "查看系统状态失败：", false, func(hooks TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error) {
			return hooks.GetSystemStatus
		})
	case NameGetPerformanceStats:
		return executeSystemTool(ctx, args, "查看性能统计失败：", false, func(hooks TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error) {
			return hooks.GetPerformanceStats
		})
	case NameListImageCache:
		return executeSystemTool(ctx, args, "查看图片缓存失败：", false, func(hooks TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error) {
			return hooks.ListImageCache
		})
	case NameDeleteImageCache:
		return executeSystemTool(ctx, args, "删除图片缓存失败：", true, func(hooks TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error) {
			return hooks.DeleteImageCache
		})
	case NameRefreshImageCache:
		return executeSystemTool(ctx, args, "刷新图片缓存失败：", true, func(hooks TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error) {
			return hooks.RefreshImageCache
		})
	case NameGetBackgroundTasks:
		return executeSystemTool(ctx, args, "查看后台任务状态失败：", false, func(hooks TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error) {
			return hooks.GetBackgroundTasks
		})
	case NameTriggerBackgroundTask:
		return executeSystemTool(ctx, args, "触发后台任务失败：", true, func(hooks TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error) {
			return hooks.TriggerBackgroundTask
		})
	case NameListAuthKeys:
		text, err := listTelegramAgentAuthKeys(ctx, args)
		if err != nil {
			return ToolResult(false, "查看 API Key 失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameListSkills:
		text, err := listTelegramAgentSkills(ctx, cfg, args)
		if err != nil {
			return ToolResult(false, "查看 Skills 失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameReadSkill:
		text, err := readTelegramAgentSkill(ctx, cfg, args)
		if err != nil {
			return ToolResult(false, "读取 Skill 失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameRunTerminalCommand:
		return executePreparedAction(ctx, runtime, "准备执行命令失败：", "执行命令失败：", func() (telegramToolAction, error) {
			return buildTelegramRunCommandAction(ctx, chatID, cfg, args)
		})
	case NameCreateAuthKey:
		return executePreparedAction(ctx, runtime, "准备新增 API Key 失败：", "新增 API Key 失败：", func() (telegramToolAction, error) {
			return buildTelegramCreateAuthKeyAction(ctx, chatID, args)
		})
	case NameUpdateAuthKey:
		return executePreparedAction(ctx, runtime, "准备修改 API Key 失败：", "修改 API Key 失败：", func() (telegramToolAction, error) {
			return buildTelegramUpdateAuthKeyAction(ctx, chatID, args)
		})
	case NameListScheduledTasks:
		text, err := listTelegramAgentScheduledTasks(ctx, args)
		if err != nil {
			return ToolResult(false, "查看 Agent 定时任务失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameCreateScheduledTask:
		return executePreparedAction(ctx, runtime, "准备新增 Agent 定时任务失败：", "新增 Agent 定时任务失败：", func() (telegramToolAction, error) {
			return buildTelegramCreateScheduledTaskAction(ctx, chatID, args)
		})
	case NameUpdateScheduledTask:
		return executePreparedAction(ctx, runtime, "准备修改 Agent 定时任务失败：", "修改 Agent 定时任务失败：", func() (telegramToolAction, error) {
			return buildTelegramUpdateScheduledTaskAction(ctx, chatID, args)
		})
	case NameSetScheduledTaskStatus:
		return executePreparedAction(ctx, runtime, "准备 Agent 定时任务状态操作失败：", "执行 Agent 定时任务状态操作失败：", func() (telegramToolAction, error) {
			return buildTelegramSetScheduledTaskStatusAction(ctx, chatID, args)
		})
	case NameSetModelStatus:
		if args.Enabled == nil {
			return ToolResult(false, "缺少 enabled 参数")
		}
		target := cleanupTelegramToolTarget(args.Target)
		return executePreparedAction(ctx, runtime, "准备模型操作失败：", "执行模型操作失败：", func() (telegramToolAction, error) {
			return buildTelegramSetModelStatusActionWithMode(ctx, chatID, target, *args.Enabled, args.Bulk)
		})
	case NameSetModelsStatusBatch:
		return executePreparedAction(ctx, runtime, "准备批量模型操作失败：", "执行批量模型操作失败：", func() (telegramToolAction, error) {
			return buildTelegramSetModelsStatusBatchAction(ctx, chatID, args.Items)
		})
	case NameSetProviderStatus:
		if args.Enabled == nil {
			return ToolResult(false, "缺少 enabled 参数")
		}
		target := cleanupTelegramToolTarget(args.Target)
		return executePreparedAction(ctx, runtime, "准备提供商操作失败：", "执行提供商操作失败：", func() (telegramToolAction, error) {
			return buildTelegramSetProviderStatusAction(ctx, chatID, target, *args.Enabled)
		})
	case NameGetProviderConfig:
		text, err := getTelegramAgentProviderConfigText(ctx, args.Target)
		if err != nil {
			return ToolResult(false, "查看提供商配置失败："+err.Error())
		}
		return ToolResult(true, text)
	case NameUpdateProviderConfig:
		return executePreparedAction(ctx, runtime, "准备提供商配置更新失败：", "执行提供商配置更新失败：", func() (telegramToolAction, error) {
			return buildTelegramUpdateProviderConfigAction(ctx, chatID, args)
		})
	default:
		return ToolResult(false, "未知工具："+name)
	}
}

func executeSystemTool(ctx context.Context, args CallArgs, errorPrefix string, final bool, pick func(TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error)) string {
	text, err := callTelegramAgentSystemTool(ctx, args, pick)
	if err != nil {
		if final {
			return ToolFinalResult(false, errorPrefix+err.Error())
		}
		return ToolResult(false, errorPrefix+err.Error())
	}
	if final {
		return ToolFinalResult(true, text)
	}
	return ToolResult(true, text)
}

func executePreparedAction(ctx context.Context, runtime Runtime, prepareErrorPrefix string, executeErrorPrefix string, build func() (telegramToolAction, error)) string {
	action, err := build()
	if err != nil {
		return ToolResult(false, prepareErrorPrefix+err.Error())
	}
	text, err := prepareOrExecuteTelegramToolAction(ctx, runtime, action)
	if err != nil {
		return ToolFinalResult(false, executeErrorPrefix+err.Error())
	}
	return ToolFinalResult(true, text)
}
