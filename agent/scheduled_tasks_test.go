package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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

func TestExecuteTelegramAgentScheduledTaskPushDoesNotLoadHistory(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_scheduled_task_no_history")
	ctx := context.Background()
	chatID := int64(6801293687)
	telegramSessions.Delete(chatID)
	t.Cleanup(func() {
		telegramSessions.Delete(chatID)
	})

	config := models.TelegramAgentConfig{
		Enabled:            boolPtr(true),
		BaseURL:            "https://api.example.com/v1",
		APIKey:             "sk-test",
		Model:              "gpt-direct",
		MaxHistoryMessages: 6,
	}
	configValue, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("序列化 TG Agent 配置失败: %v", err)
	}
	if err := db.Create(&models.Config{
		Key:   models.KeyTelegramAgent,
		Value: string(configValue),
	}).Error; err != nil {
		t.Fatalf("保存 TG Agent 配置失败: %v", err)
	}

	oldConversationID, err := startNewTelegramConversation(ctx, chatID)
	if err != nil {
		t.Fatalf("创建测试会话失败: %v", err)
	}
	oldHistory := []chatMessage{
		{Role: "user", Content: "旧问题"},
		{Role: "assistant", Content: "旧回答"},
	}
	if err := saveTelegramSessionMessages(ctx, chatID, oldHistory); err != nil {
		t.Fatalf("保存旧上下文失败: %v", err)
	}
	telegramSessions.Delete(chatID)

	previousExecutor := telegramAgentProviderRequestExecutor
	defer func() {
		telegramAgentProviderRequestExecutor = previousExecutor
	}()

	var capturedMessages []map[string]any
	telegramAgentProviderRequestExecutor = func(ctx context.Context, selected selectedModelProvider, body []byte, stream bool, startedAt time.Time) (*http.Response, int, error) {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("解析 TG Agent 请求体失败: %v", err)
		}
		rawMessages, ok := payload["messages"].([]any)
		if !ok {
			t.Fatalf("请求体 messages 字段类型不正确: %#v", payload["messages"])
		}
		capturedMessages = make([]map[string]any, 0, len(rawMessages))
		for _, item := range rawMessages {
			message, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("消息项类型不正确: %#v", item)
			}
			capturedMessages = append(capturedMessages, message)
		}

		streamBody := strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"定时任务执行完成"}}]}`,
			"",
			`data: [DONE]`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(streamBody)),
		}, 3, nil
	}

	task := models.TelegramAgentScheduledTask{
		Name:               "天气播报",
		Prompt:             "查询今天广州天气",
		Enabled:            1,
		ScheduleType:       TelegramAgentScheduleTypeInterval,
		IntervalMinutes:    15,
		Timezone:           "Local",
		PushToConversation: 1,
		ChatID:             chatID,
	}
	result, err := ExecuteTelegramAgentScheduledTask(ctx, task, &telegramToolTestClient{}, chatID)
	if err != nil {
		t.Fatalf("执行推送型定时任务失败: %v", err)
	}
	if result.Text != "已推送到 Agent 对话" {
		t.Fatalf("返回结果不符合预期: %+v", result)
	}
	taskMessageCount := 0
	for _, message := range capturedMessages {
		content, _ := message["content"].(string)
		if strings.Contains(content, "旧问题") || strings.Contains(content, "旧回答") {
			t.Fatalf("推送型定时任务不应加载旧上下文，实际内容=%q", content)
		}
		if message["role"] == "user" && strings.Contains(content, "任务名称") && strings.Contains(content, "天气播报") {
			taskMessageCount++
		}
	}
	if taskMessageCount != 1 {
		t.Fatalf("推送型定时任务应只携带本轮任务消息，实际任务消息数=%d，内容=%#v", taskMessageCount, capturedMessages)
	}

	telegramSessions.Delete(chatID)
	loaded, err := loadTelegramSessionMessages(ctx, chatID, config)
	if err != nil {
		t.Fatalf("读取执行后的上下文失败: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("执行后应仅保存本轮上下文，实际为 %+v", loaded)
	}
	if loaded[0].Content == "旧问题" || loaded[0].Content == "旧回答" {
		t.Fatalf("执行后上下文不应混入旧会话内容: %+v", loaded)
	}

	currentConversationID := getTelegramSession(chatID).conversationID
	if strings.TrimSpace(currentConversationID) == "" {
		t.Fatalf("执行后应保留当前会话 ID")
	}
	if currentConversationID != oldConversationID {
		t.Fatalf("不应切换到新会话，旧=%q 新=%q", oldConversationID, currentConversationID)
	}
}
