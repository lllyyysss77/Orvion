package tools

func SkillDefinitions(skillNames []string) []Definition {
	definitions := []Definition{
		{
			Name:        NameListSkills,
			Description: "查看本地 Agent Skills 列表。Skills 来自本地目录中的 skills.md 或 SKILL.md，支持按名称、描述、触发词和说明关键词过滤。",
			Properties: map[string]any{
				"query": stringProperty("Skill 名称、描述、触发词或自然语言需求，可为空。"),
				"limit": integerProperty("最多返回条数，默认 20，最大 50。"),
			},
			Category: CategorySkill,
		},
	}
	if len(skillNames) == 0 {
		return definitions
	}

	skillProperty := stringProperty("Skill 名称。")
	skillProperty["enum"] = skillNames
	definitions = append(definitions,
		Definition{
			Name:        NameReadSkill,
			Description: "按需加载指定 Skill 的 SKILL.md Body、Skill 目录、脚本相对路径、绝对路径和推荐命令模板。需要执行脚本或读取 references/scripts 资源时，必须先读取 Skill。",
			Properties: map[string]any{
				"skill": skillProperty,
			},
			Required: []string{"skill"},
			Category: CategorySkill,
		},
		Definition{
			Name:        NameRunTerminalCommand,
			Description: "执行本地命令行工具，用于按 Skill 说明和推荐命令模板运行脚本、读取 references/scripts 资源或进行文件操作。请先 read_skill 获取 Skill 目录和脚本绝对路径，再用 command + command_args + working_dir 结构化执行；不要使用 bash -c/sh -c/zsh -c。执行会直接运行并写入审计日志。",
			Properties: map[string]any{
				"command":      stringProperty("可执行命令或绝对路径，例如 bash、python3、node 或 /path/to/script。必须是单个命令，参数放 command_args。"),
				"command_args": stringArrayProperty("命令参数列表，例如 [\"/abs/skill/scripts/search.sh\", \"--query\", \"广州天气\"]。"),
				"working_dir":  stringProperty("工作目录。执行 Skill 脚本时通常传 read_skill 返回的 Skill 目录。"),
				"stdin":        stringProperty("可选 stdin 文本。脚本要求 JSON stdin 时再传。"),
				"timeout_ms":   integerProperty("超时时间，默认 30000，最大 240000。"),
			},
			Required: []string{"command"},
			Category: CategorySkill,
		},
	)
	return definitions
}
