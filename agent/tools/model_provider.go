package tools

func ModelProviderDefinitions() []Definition {
	return []Definition{
		{
			Name:        NameListModels,
			Description: "查看 Orvion 模型列表，可按关键词筛选。",
			Properties: map[string]any{
				"query": stringProperty("模型名称筛选关键词，可为空。"),
			},
			Category: CategoryModelProvider,
		},
		{
			Name:        NameListProviders,
			Description: "查看 Orvion 提供商列表，可按关键词筛选，返回提供商 URL 和 API Key。",
			Properties: map[string]any{
				"query": stringProperty("提供商名称筛选关键词，可为空。"),
			},
			Category: CategoryModelProvider,
		},
		{
			Name:        NameSetModelStatus,
			Description: "启用或禁用模型。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"target":  stringProperty("模型精确名称、ID 或批量关键词。去掉所有、全部、相关、的等修饰词。"),
				"enabled": booleanProperty("true 表示启用，false 表示禁用。"),
				"bulk":    booleanProperty("是否按 target 作为关键词批量匹配多个模型。"),
			},
			Required: []string{"target", "enabled"},
			Category: CategoryModelProvider,
		},
		{
			Name:        NameSetModelsStatusBatch,
			Description: "批量启用或禁用多组模型，适合一句话里同时包含多个模型启停动作。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"items": map[string]any{
					"type":        "array",
					"description": "多个模型启停动作。每个动作必须独立表达 target、enabled 和 bulk。",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"target":  stringProperty("模型精确名称或批量关键词。去掉所有、全部、相关、的等修饰词。"),
							"enabled": booleanProperty("true 表示启用，false 表示禁用。"),
							"bulk":    booleanProperty("是否按 target 作为关键词批量匹配多个模型。"),
						},
						"required":             []string{"target", "enabled"},
						"additionalProperties": false,
					},
				},
			},
			Required: []string{"items"},
			Category: CategoryModelProvider,
		},
		{
			Name:        NameSetProviderStatus,
			Description: "启用或禁用提供商。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"target":  stringProperty("提供商名称或 ID。"),
				"enabled": booleanProperty("true 表示启用，false 表示禁用。"),
			},
			Required: []string{"target", "enabled"},
			Category: CategoryModelProvider,
		},
		{
			Name:        NameGetProviderConfig,
			Description: "查看提供商配置摘要。敏感字段会被隐藏，用于修改前核对当前配置。",
			Properties: map[string]any{
				"target": stringProperty("提供商名称或 ID。"),
			},
			Required: []string{"target"},
			Category: CategoryModelProvider,
		},
		{
			Name:        NameUpdateProviderConfig,
			Description: "修改提供商配置。" + MutationDescriptionSuffix,
			Properties: map[string]any{
				"target": stringProperty("提供商名称或 ID。"),
				"name":   stringProperty("新的提供商名称，可选。"),
				"config": stringProperty("完整 provider config JSON，可选。通常优先使用 config_updates 做局部修改。"),
				"config_updates": map[string]any{
					"type":                 "object",
					"description":          "要合并进 provider config JSON 的字段，例如 {\"base_url\":\"https://...\",\"api_key\":\"sk-a,sk-b\"}。",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"remove_config_keys": stringArrayProperty("要从 provider config JSON 中删除的字段名。"),
				"console":            stringProperty("控制台地址；传空字符串表示清空。"),
				"proxy_url":          stringProperty("代理地址，支持 http/https/socks5；传空字符串表示清空。"),
				"models_fetch_mode":  enumStringProperty("模型获取方式：v1_models 表示通用，api_pricing 表示 NewAPI。", "v1_models", "api_pricing"),
				"capabilities": map[string]any{
					"type":        "array",
					"description": "提供商支持的接口能力。",
					"items":       map[string]any{"type": "string", "enum": []string{"chat", "openai", "claude"}},
				},
				"interface_conversion_enabled": booleanProperty("是否启用接口转换。"),
				"interface_conversion_target":  enumStringProperty("接口转换目标：chat 对应 /v1/chat/completions，responses 对应 /v1/responses，messages 对应 /v1/messages；关闭转换请设置 interface_conversion_enabled=false。", "chat", "responses", "messages"),
			},
			Required: []string{"target"},
			Category: CategoryModelProvider,
		},
	}
}
