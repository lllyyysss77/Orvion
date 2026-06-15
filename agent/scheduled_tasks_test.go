package agent

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestTelegramAgentScheduledTaskDailyNextRunAt(t *testing.T) {
	task := models.TelegramAgentScheduledTask{
		ScheduleType: TelegramAgentScheduleTypeDaily,
		TimeOfDay:    "09:30",
		Timezone:     "Local",
	}
	from := time.Date(2026, 6, 7, 8, 0, 0, 0, time.Local)
	next, err := CalculateTelegramAgentScheduledTaskNextRunAt(task, from)
	if err != nil {
		t.Fatalf("计算下次执行时间失败: %v", err)
	}
	if next.Hour() != 9 || next.Minute() != 30 || next.Day() != 7 {
		t.Fatalf("下次执行时间不符合预期: %s", next.Format(time.RFC3339))
	}

	from = time.Date(2026, 6, 7, 10, 0, 0, 0, time.Local)
	next, err = CalculateTelegramAgentScheduledTaskNextRunAt(task, from)
	if err != nil {
		t.Fatalf("计算跨天下次执行时间失败: %v", err)
	}
	if next.Hour() != 9 || next.Minute() != 30 || next.Day() != 8 {
		t.Fatalf("跨天下次执行时间不符合预期: %s", next.Format(time.RFC3339))
	}
}

func TestTelegramAgentScheduledTaskClaimAndFinish(t *testing.T) {
	previousDB := models.DB
	t.Cleanup(func() {
		models.DB = previousDB
	})

	db, err := gorm.Open(sqlite.Open("file:tg_agent_scheduled_task_claim?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	models.DB = db
	if err := db.AutoMigrate(&models.TelegramAgentScheduledTask{}); err != nil {
		t.Fatalf("初始化测试表失败: %v", err)
	}

	now := time.Now()
	dueAt := now.Add(-time.Minute)
	task := models.TelegramAgentScheduledTask{
		Name:            "测试任务",
		Prompt:          "总结系统状态",
		Enabled:         1,
		ScheduleType:    TelegramAgentScheduleTypeInterval,
		IntervalMinutes: 15,
		Timezone:        "Local",
		NextRunAt:       &dueAt,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("创建测试任务失败: %v", err)
	}

	claimed, err := ClaimDueTelegramAgentScheduledTasks(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("抢占任务失败: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != task.ID {
		t.Fatalf("抢占任务不符合预期: %#v", claimed)
	}

	var running models.TelegramAgentScheduledTask
	if err := db.First(&running, task.ID).Error; err != nil {
		t.Fatalf("读取抢占后任务失败: %v", err)
	}
	if running.Running != 1 || running.LastStatus != "running" {
		t.Fatalf("抢占状态不符合预期: running=%d status=%s", running.Running, running.LastStatus)
	}

	err = FinishTelegramAgentScheduledTask(context.Background(), claimed[0], TelegramAgentScheduledTaskRunResult{Text: "执行完成"}, nil, now)
	if err != nil {
		t.Fatalf("完成任务失败: %v", err)
	}

	var finished models.TelegramAgentScheduledTask
	if err := db.First(&finished, task.ID).Error; err != nil {
		t.Fatalf("读取完成后任务失败: %v", err)
	}
	if finished.Running != 0 || finished.LastStatus != "success" || finished.RunCount != 1 {
		t.Fatalf("完成状态不符合预期: running=%d status=%s run_count=%d", finished.Running, finished.LastStatus, finished.RunCount)
	}
	if finished.NextRunAt == nil || !finished.NextRunAt.After(now) {
		t.Fatalf("下次执行时间未刷新: %#v", finished.NextRunAt)
	}
}
