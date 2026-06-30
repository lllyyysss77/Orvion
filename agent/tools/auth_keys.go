package tools

func AuthKeyDefinitions() []Definition {
	return []Definition{
		{
			Name:        NameListAuthKeys,
			Description: "查看 Orvion API Key 列表，只返回项目名称、掩码 Key、状态、权限、RPM、用量和最后使用时间，不返回完整 Key。",
			Properties: map[string]any{
				"query":  stringProperty("API Key 项目名称筛选关键词，可为空。"),
				"status": enumStringProperty("状态筛选。all 表示不过滤状态。", "all", "enabled", "disabled"),
				"limit":  integerProperty("最多返回条数，默认 20，最大 50。"),
			},
			Category: CategoryAuthKey,
		},
		{
			Name:        NameCreateAuthKey,
			Description: "新增 Orvion API Key。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"name":       stringProperty("API Key 项目名称。"),
				"key_suffix": stringProperty("自定义 Key 后缀，可选。传 abc 会生成 sk-abc；不传则自动生成。"),
				"enabled":    booleanProperty("是否启用，默认 true。"),
				"allow_all":  booleanProperty("是否允许全部模型，默认 true。false 表示限制模型。"),
				"models":     stringArrayProperty("允许的精确模型名列表，allow_all=false 时可用。"),
				"model_keywords": stringArrayProperty(
					"模型名称关键词列表，会按包含匹配多个模型，例如 claude、deepseek。",
				),
				"expires_at": stringProperty("有效期，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。可选。"),
				"rpm_limit":  integerProperty("每分钟请求数限制，0 表示无限制，默认 0。"),
			},
			Required: []string{"name"},
			Category: CategoryAuthKey,
		},
		{
			Name:        NameUpdateAuthKey,
			Description: "修改 Orvion API Key 的名称、启用状态、模型权限、有效期、RPM 或 Key 后缀。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"target":     stringProperty("API Key 项目名称或 ID。"),
				"name":       stringProperty("新的项目名称，可选。"),
				"key_suffix": stringProperty("新的 Key 后缀，可选。传 abc 会改为 sk-abc；不传则不修改 Key。"),
				"enabled":    booleanProperty("是否启用，可选。"),
				"allow_all":  booleanProperty("是否允许全部模型。false 表示限制模型。"),
				"models":     stringArrayProperty("允许的精确模型名列表，allow_all=false 时可用。"),
				"model_keywords": stringArrayProperty(
					"模型名称关键词列表，会按包含匹配多个模型，例如 claude、deepseek。",
				),
				"expires_at":       stringProperty("新的有效期，可用 RFC3339、YYYY-MM-DD HH:mm:ss 或 YYYY-MM-DD。"),
				"clear_expires_at": booleanProperty("是否清空有效期。"),
				"rpm_limit":        integerProperty("每分钟请求数限制，0 表示无限制。"),
			},
			Required: []string{"target"},
			Category: CategoryAuthKey,
		},
	}
}
