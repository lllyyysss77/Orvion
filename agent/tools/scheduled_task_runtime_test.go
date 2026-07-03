package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestRunScheduledTaskToolUsesRuntimeExecutor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:tg_agent_run_scheduled_task_tool?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&models.TelegramAgentScheduledTask{}); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}
	previousDB := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = previousDB })

	task := models.TelegramAgentScheduledTask{
		Name:            "每日天气",
		Prompt:          "查询佛山天气",
		Enabled:         1,
		ScheduleType:    TelegramAgentScheduleTypeInterval,
		IntervalMinutes: 60,
		Timezone:        "Local",
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("创建测试定时任务失败: %v", err)
	}

	called := false
	result := ExecuteFunctionTool(context.Background(), Runtime{
		RunScheduledTask: func(_ context.Context, chatID int64, got models.TelegramAgentScheduledTask) (string, error) {
			called = true
			if chatID != 6801293722 {
				t.Fatalf("chatID 不正确: %d", chatID)
			}
			if got.Name != task.Name || got.Prompt != task.Prompt {
				t.Fatalf("传入的定时任务不正确: %+v", got)
			}
			return "已执行 Agent 定时任务\n名称：每日天气\n结果：晴天", nil
		},
	}, 6801293722, models.TelegramAgentConfig{}, NameRunScheduledTask, CallArgs{Target: "每日天气"})

	payload := ParseResultPayload(result)
	if !called {
		t.Fatalf("期望调用 Runtime.RunScheduledTask")
	}
	if !payload.OK || !payload.Final {
		t.Fatalf("期望立即执行工具返回最终成功结果，实际为: %+v", payload)
	}
	if !strings.Contains(payload.Text, "已执行 Agent 定时任务") || !strings.Contains(payload.Text, "晴天") {
		t.Fatalf("工具返回内容不正确: %s", payload.Text)
	}
}
