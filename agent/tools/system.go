package tools

func SystemDefinitions() []Definition {
	return []Definition{
		{
			Name:        NameGetSystemStatus,
			Description: "查看 Orvion 当前系统状态摘要，包括资源使用、模型数量、今日请求成功率、消耗和运行时间。",
			Properties:  map[string]any{},
			Category:    CategorySystem,
		},
		{
			Name:        NameGetPerformanceStats,
			Description: "查看慢 SQL 统计和请求成功率，适合判断数据库查询健康度、接口成功率和平均延迟。",
			Properties: map[string]any{
				"recent_minutes": integerProperty("统计最近多少分钟的请求成功率，默认 60，最大 10080。"),
			},
			Category: CategorySystem,
		},
		{
			Name:        NameListImageCache,
			Description: "查看当前 TG 状态图片缓存列表，包括缓存数量、容量、大小、文件名和来源。",
			Properties:  map[string]any{},
			Category:    CategorySystem,
		},
		{
			Name:        NameDeleteImageCache,
			Description: "删除 TG 状态图片缓存。传 cache_id 删除单张；传 all=true 清空全部缓存。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"cache_id": integerProperty("图片缓存 ID。删除单张时必填。"),
				"all":      booleanProperty("是否清空全部图片缓存。"),
			},
			Category: CategorySystem,
		},
		{
			Name:        NameRefreshImageCache,
			Description: "刷新或补充 TG 状态图片缓存。默认清空旧缓存后后台重新下载。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"clear_existing": booleanProperty("是否先清空已有缓存，默认 true。false 表示只补齐缺失缓存。"),
			},
			Category: CategorySystem,
		},
		{
			Name:        NameGetBackgroundTasks,
			Description: "查看 Orvion 后台任务状态，包括价格同步、系统日志清理、日志 token 回填、图片缓存补充和 Agent 定时任务扫描。",
			Properties:  map[string]any{},
			Category:    CategorySystem,
		},
		{
			Name:        NameTriggerBackgroundTask,
			Description: "手动触发后台任务：模型价格同步、系统日志清理、日志输出/token 回填或图片缓存补充。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"task": enumStringProperty("要触发的任务。", "model_price_sync", "system_log_cleanup", "token_backfill", "image_cache_refill"),
			},
			Required: []string{"task"},
			Category: CategorySystem,
		},
	}
}
