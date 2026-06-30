package tools

func LogDefinitions() []Definition {
	return []Definition{
		{
			Name:        NameReadSystemLogs,
			Description: "读取 Orvion 系统日志尾部内容，适合排查 panic、SQL、启动、熔断、Telegram、更新检查等运行时问题。",
			Properties: map[string]any{
				"level": enumStringProperty("日志级别筛选。all 表示不过滤级别。", "all", "debug", "info", "warn", "error"),
				"query": stringProperty("日志文本关键词，可为空。"),
				"limit": integerProperty("最多返回行数，默认 20，最大 80。"),
			},
			Category: CategoryLog,
		},
		{
			Name:        NameReadRequestLogs,
			Description: "读取 Orvion 请求日志摘要，适合排查模型请求失败、供应商错误、超时、成本和 token 消耗。",
			Properties: map[string]any{
				"status":         enumStringProperty("请求状态筛选。all 表示不过滤状态。", "all", "success", "error"),
				"provider_name":  stringProperty("提供商名称关键词，可为空。"),
				"model":          stringProperty("模型名称或上游模型名称关键词，可为空。"),
				"query":          stringProperty("任意关键词，会匹配模型、提供商、请求路径、错误、User-Agent、IP 等字段。"),
				"recent_minutes": integerProperty("只看最近多少分钟，可为空或 0。"),
				"start_at":       stringProperty("开始时间，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。"),
				"end_at":         stringProperty("结束时间，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。"),
				"limit":          integerProperty("最多返回条数，默认 10，最大 200。"),
			},
			Category: CategoryLog,
		},
	}
}
