package memory

import (
	"fmt"
	"strings"

	"github.com/racio/orvion/models"
)

func dailyMemorySystemPrompt() string {
	return strings.Join([]string{
		"你是 Orvion 的长期记忆整理器。",
		"你的任务是判断一轮 Telegram Agent 对话是否值得写入长期记忆，并把值得记录的信息合并成当天摘要。",
		"只记录长期稳定的信息，例如用户偏好、长期任务背景、项目约定、架构决策、持续问题、明确要求以后遵守的规则。",
		"不要记录一次性闲聊、临时查询结果、普通天气新闻、无长期价值的执行过程、敏感密钥或完整 token。",
		"如果已有当天记忆，请输出合并后的完整当天摘要，而不是只输出新增片段。",
		"只能返回 JSON，不要返回 Markdown 或解释。",
		`JSON 格式：{"worth_remembering":true,"title":"简短标题","summary":"完整当天长期记忆摘要"}`,
		`如果不值得记录，返回：{"worth_remembering":false,"title":"","summary":""}`,
	}, "\n")
}

func dailyMemoryUserPrompt(dayKey string, existing models.AgentMemory, user string, assistant string) string {
	lines := []string{
		"日期：" + dayKey,
	}
	if strings.TrimSpace(existing.Content) != "" {
		lines = append(lines,
			"",
			"已有当天记忆：",
			"标题："+existing.Title,
			"内容：",
			limitText(existing.Content, maxMemoryContentRunes),
		)
	}
	lines = append(lines,
		"",
		"本轮用户消息：",
		limitText(user, maxTurnTextRunes),
		"",
		"本轮助手回复：",
		limitText(assistant, maxTurnTextRunes),
	)
	return strings.Join(lines, "\n")
}

func rollupMemorySystemPrompt(periodType string) string {
	label := memoryPeriodLabel(periodType)
	return strings.Join([]string{
		"你是 Orvion 的长期记忆压缩器。",
		"你的任务是把较细粒度的长期记忆合并成" + label + "摘要。",
		"保留长期稳定的信息：用户偏好、长期任务背景、项目约定、架构决策、持续问题、固定规则。",
		"删除重复、临时查询结果、一次性执行过程和无长期价值细节。",
		"如果已有该周期摘要，请输出合并后的完整摘要。",
		"只能返回 JSON，不要返回 Markdown 或解释。",
		`JSON 格式：{"title":"简短标题","summary":"完整周期长期记忆摘要"}`,
	}, "\n")
}

func rollupMemoryUserPrompt(periodType string, periodKey string, existing models.AgentMemory, rows []models.AgentMemory) string {
	lines := []string{
		fmt.Sprintf("目标周期：%s %s", memoryPeriodLabel(periodType), periodKey),
	}
	if strings.TrimSpace(existing.Content) != "" {
		lines = append(lines,
			"",
			"已有目标周期摘要：",
			"标题："+existing.Title,
			"内容：",
			limitText(existing.Content, maxMemoryContentRunes),
		)
	}
	lines = append(lines, "", "待合并记忆：")
	for _, row := range rows {
		lines = append(lines,
			"",
			fmt.Sprintf("[%s %s] %s", memoryPeriodLabel(row.PeriodType), row.PeriodKey, emptyFallback(row.Title, "记忆摘要")),
			limitText(row.Content, maxMemoryContentRunes),
		)
	}
	return strings.Join(lines, "\n")
}

func memoryPeriodLabel(periodType string) string {
	switch periodType {
	case PeriodDay:
		return "日"
	case PeriodWeek:
		return "周"
	case PeriodMonth:
		return "月"
	default:
		return "周期"
	}
}
