package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/racio/orvion/models"
)

func TestTelegramAgentScheduledTaskToolsCreateAndUpdateDirectly(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_scheduled_task_tools_direct")
	ctx := context.Background()
	chatID := int64(6801293720)
	cfg := models.TelegramAgentConfig{}

	createResult := executeTelegramAgentFunctionToolCall(ctx, chatID, cfg, telegramAgentOpenAIToolCall{
		ID:   "call_create_schedule",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name: telegramAgentToolCreateScheduledTask,
			Arguments: strings.Join([]string{
				`{`,
				`"name":"每日日志检查",`,
				`"prompt":"检查最近错误日志并总结",`,
				`"schedule_type":"daily",`,
				`"time_of_day":"09:30",`,
				`"timezone":"Asia/Shanghai",`,
				`"push_to_conversation":true`,
				`}`,
			}, ""),
		},
	})
	createPayload := parseTelegramAgentToolResultPayload(createResult)
	if !createPayload.OK || !createPayload.Final || !strings.Contains(createPayload.Text, "已创建 Agent 定时任务") {
		t.Fatalf("创建定时任务工具返回不正确: %+v", createPayload)
	}

	var task models.TelegramAgentScheduledTask
	if err := db.Where("name = ?", "每日日志检查").First(&task).Error; err != nil {
		t.Fatalf("定时任务应已创建: %v", err)
	}
	if task.ScheduleType != TelegramAgentScheduleTypeDaily || task.TimeOfDay != "09:30" || task.PushToConversation != 1 || task.NextRunAt == nil {
		t.Fatalf("创建后的定时任务字段不正确: %+v", task)
	}

	updateResult := executeTelegramAgentFunctionToolCall(ctx, chatID, cfg, telegramAgentOpenAIToolCall{
		ID:   "call_update_schedule",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolUpdateScheduledTask,
			Arguments: `{"target":"每日日志检查","interval_minutes":30,"push_to_conversation":false}`,
		},
	})
	updatePayload := parseTelegramAgentToolResultPayload(updateResult)
	if !updatePayload.OK || !strings.Contains(updatePayload.Text, "已更新 Agent 定时任务") {
		t.Fatalf("修改定时任务工具返回不正确: %+v", updatePayload)
	}

	if err := db.Where("name = ?", "每日日志检查").First(&task).Error; err != nil {
		t.Fatalf("读取修改后的定时任务失败: %v", err)
	}
	if task.ScheduleType != TelegramAgentScheduleTypeInterval || task.IntervalMinutes != 30 || task.PushToConversation != 0 {
		t.Fatalf("修改后的定时任务字段不正确: %+v", task)
	}
}

func TestTelegramAgentScheduledTaskToolCreatesDirectlyByDefault(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_scheduled_task_tools_default_direct")
	ctx := context.Background()
	chatID := int64(6801293721)

	result := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_create_schedule_default_direct",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolCreateScheduledTask,
			Arguments: `{"name":"默认直接任务","prompt":"检查系统状态","interval_minutes":15}`,
		},
	})
	payload := parseTelegramAgentToolResultPayload(result)
	if !payload.OK || !payload.Final || !strings.Contains(payload.Text, "已创建 Agent 定时任务") {
		t.Fatalf("默认应直接创建定时任务，实际为: %+v", payload)
	}

	var count int64
	if err := db.Model(&models.TelegramAgentScheduledTask{}).Where("name = ?", "默认直接任务").Count(&count).Error; err != nil {
		t.Fatalf("统计定时任务失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("默认应创建定时任务，实际数量: %d", count)
	}
}
