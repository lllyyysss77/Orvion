package tools

func ScheduledTaskDefinitions() []Definition {
	return []Definition{
		{
			Name:        NameListScheduledTasks,
			Description: "查看 Orvion TG Agent 定时任务列表，可按名称、内容关键词和状态筛选。",
			Properties: map[string]any{
				"query":  stringProperty("任务名称或任务内容关键词，可为空。"),
				"status": enumStringProperty("状态筛选。all 表示不过滤状态。", "all", "enabled", "disabled", "running"),
				"limit":  integerProperty("最多返回条数，默认 20，最大 50。"),
			},
			Category: CategoryScheduledTask,
		},
		{
			Name:        NameCreateScheduledTask,
			Description: "新增 Orvion TG Agent 定时任务。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"name":                 stringProperty("任务名称。"),
				"prompt":               stringProperty("任务内容，也就是到时间后让 Agent 执行的自然语言指令。"),
				"enabled":              booleanProperty("是否启用，默认 true。"),
				"schedule_type":        enumStringProperty("定时类型。interval 表示每隔多少分钟；daily 表示每天固定时间。默认 interval。", "interval", "daily"),
				"interval_minutes":     integerProperty("间隔分钟数，schedule_type=interval 时使用，默认 60。"),
				"time_of_day":          stringProperty("每天执行时间，schedule_type=daily 时必填，格式 HH:mm，例如 09:30。"),
				"timezone":             stringProperty("时区，默认 Local；可传 Asia/Shanghai。"),
				"push_to_conversation": booleanProperty("是否把执行结果推送并写入当前 Agent 对话上下文，默认 false。"),
				"chat_id":              integerProperty("推送目标 Telegram Chat ID。0 或不传表示使用默认配置。"),
			},
			Required: []string{"name", "prompt"},
			Category: CategoryScheduledTask,
		},
		{
			Name:        NameUpdateScheduledTask,
			Description: "修改 Orvion TG Agent 定时任务的名称、内容、计划、启用状态、推送设置或 Chat ID。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"target":               stringProperty("任务名称或 ID。"),
				"name":                 stringProperty("新的任务名称，可选。"),
				"prompt":               stringProperty("新的任务内容，可选。"),
				"enabled":              booleanProperty("是否启用，可选。"),
				"schedule_type":        enumStringProperty("定时类型：interval 或 daily。", "interval", "daily"),
				"interval_minutes":     integerProperty("间隔分钟数，schedule_type=interval 时使用。"),
				"time_of_day":          stringProperty("每天执行时间，格式 HH:mm。"),
				"timezone":             stringProperty("时区，例如 Local 或 Asia/Shanghai。"),
				"push_to_conversation": booleanProperty("是否把执行结果推送并写入 Agent 对话上下文。"),
				"chat_id":              integerProperty("推送目标 Telegram Chat ID。0 表示使用默认配置。"),
				"clear_chat_id":        booleanProperty("是否清空自定义 Chat ID，改用默认配置。"),
			},
			Required: []string{"target"},
			Category: CategoryScheduledTask,
		},
		{
			Name:        NameSetScheduledTaskStatus,
			Description: "启用或禁用 Orvion TG Agent 定时任务。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"target":  stringProperty("任务名称或 ID。"),
				"enabled": booleanProperty("true 表示启用，false 表示禁用。"),
			},
			Required: []string{"target", "enabled"},
			Category: CategoryScheduledTask,
		},
		{
			Name:        NameRunScheduledTask,
			Description: "立即执行一个已有的 Orvion TG Agent 定时任务，并把执行结果返回给当前对话。该工具不会等待任务原定时间，也不会额外推送一条独立 TG 消息。",
			Properties: map[string]any{
				"target": stringProperty("任务名称或 ID。"),
			},
			Required: []string{"target"},
			Category: CategoryScheduledTask,
		},
	}
}
