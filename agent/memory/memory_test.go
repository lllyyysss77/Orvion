package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

type fakeMemoryLLM struct {
	responses []string
	calls     []CompleteRequest
}

func (llm *fakeMemoryLLM) Complete(_ context.Context, req CompleteRequest) (string, error) {
	llm.calls = append(llm.calls, req)
	if len(llm.responses) == 0 {
		return `{"worth_remembering":false,"title":"","summary":"","importance":0}`, nil
	}
	response := llm.responses[0]
	llm.responses = llm.responses[1:]
	return response, nil
}

func TestProcessTurnCreatesAndMergesDailyMemory(t *testing.T) {
	useMemoryTestDB(t)

	cfg := models.TelegramAgentConfig{Model: "memory-model"}
	day := time.Date(2026, 6, 29, 10, 0, 0, 0, time.Local)
	llm := &fakeMemoryLLM{responses: []string{
		`{"worth_remembering":true,"title":"回复偏好","summary":"用户希望所有回复使用简体中文。","importance":90}`,
		`{"worth_remembering":true,"title":"回复偏好","summary":"用户希望所有回复使用简体中文，并且核心代码注释也使用中文。","importance":92}`,
	}}

	err := ProcessTurn(context.Background(), cfg, llm, Turn{
		User:       "以后都用中文",
		Assistant:  "好的",
		OccurredAt: day,
	})
	if err != nil {
		t.Fatalf("写入日记忆失败: %v", err)
	}

	err = ProcessTurn(context.Background(), cfg, llm, Turn{
		User:       "代码注释也用中文",
		Assistant:  "明白",
		OccurredAt: day.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("合并日记忆失败: %v", err)
	}

	var row models.AgentMemory
	if err := models.DB.Where("period_type = ? AND period_key = ?", PeriodDay, "2026-06-29").First(&row).Error; err != nil {
		t.Fatalf("读取日记忆失败: %v", err)
	}
	if row.SourceCount != 2 {
		t.Fatalf("期望 source_count=2，实际为 %d", row.SourceCount)
	}
	if !strings.Contains(row.Content, "核心代码注释") {
		t.Fatalf("日记忆未保存合并后的摘要: %s", row.Content)
	}
}

func TestBuildContextPromptIncludesGlobalMemories(t *testing.T) {
	useMemoryTestDB(t)

	rows := []models.AgentMemory{
		memoryRow(PeriodMonth, "2026-06", "月摘要", "用户长期维护 Orvion。", time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)),
		memoryRow(PeriodWeek, "2026-W26", "周摘要", "本周重点是 TG Agent。", time.Date(2026, 6, 22, 0, 0, 0, 0, time.Local)),
		memoryRow(PeriodDay, "2026-06-29", "日摘要", "今天要求长期记忆全局生效。", time.Date(2026, 6, 29, 0, 0, 0, 0, time.Local)),
	}
	if err := models.DB.Create(&rows).Error; err != nil {
		t.Fatalf("写入测试记忆失败: %v", err)
	}

	prompt, err := BuildContextPrompt(context.Background(), models.TelegramAgentConfig{})
	if err != nil {
		t.Fatalf("构建记忆提示失败: %v", err)
	}
	for _, expected := range []string{"## 长期记忆", "月记忆", "周记忆", "日记忆", "全局生效"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("长期记忆提示缺少 %q: %s", expected, prompt)
		}
	}
}

func TestRollupCompletedCreatesWeekAndDeletesDays(t *testing.T) {
	useMemoryTestDB(t)

	previousMonday := time.Date(2026, 6, 8, 0, 0, 0, 0, time.Local)
	rows := []models.AgentMemory{
		memoryRow(PeriodDay, "2026-06-08", "偏好", "用户要求中文回复。", previousMonday),
		memoryRow(PeriodDay, "2026-06-09", "项目", "项目长期维护 TG Agent。", previousMonday.AddDate(0, 0, 1)),
	}
	if err := models.DB.Create(&rows).Error; err != nil {
		t.Fatalf("写入日记忆失败: %v", err)
	}
	llm := &fakeMemoryLLM{responses: []string{
		`{"title":"周摘要","summary":"用户要求中文回复，项目长期维护 TG Agent。","importance":88}`,
	}}

	result, err := RollupCompleted(context.Background(), models.TelegramAgentConfig{Model: "memory-model"}, llm, time.Date(2026, 6, 16, 9, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("滚动周记忆失败: %v", err)
	}
	if result.WeeksCreated != 1 || result.DaysDeleted != 2 {
		t.Fatalf("周滚动统计不正确: %+v", result)
	}
	assertMemoryCount(t, PeriodDay, 0)
	assertMemoryCount(t, PeriodWeek, 1)
}

func TestRollupCompletedCreatesMonthAndDeletesWeeks(t *testing.T) {
	useMemoryTestDB(t)

	rows := []models.AgentMemory{
		memoryRow(PeriodWeek, "2026-W23", "周摘要1", "用户偏好中文。", time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)),
		memoryRow(PeriodWeek, "2026-W24", "周摘要2", "项目重点是 Agent。", time.Date(2026, 6, 8, 0, 0, 0, 0, time.Local)),
	}
	if err := models.DB.Create(&rows).Error; err != nil {
		t.Fatalf("写入周记忆失败: %v", err)
	}
	llm := &fakeMemoryLLM{responses: []string{
		`{"title":"月摘要","summary":"用户偏好中文，项目重点是 Agent。","importance":90}`,
	}}

	result, err := RollupCompleted(context.Background(), models.TelegramAgentConfig{Model: "memory-model"}, llm, time.Date(2026, 7, 2, 9, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("滚动月记忆失败: %v", err)
	}
	if result.MonthsCreated != 1 || result.WeeksDeleted != 2 {
		t.Fatalf("月滚动统计不正确: %+v", result)
	}
	assertMemoryCount(t, PeriodWeek, 0)
	assertMemoryCount(t, PeriodMonth, 1)
}

func useMemoryTestDB(t *testing.T) {
	t.Helper()
	previousDB := models.DB
	t.Cleanup(func() {
		models.DB = previousDB
	})

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentMemory{}); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	models.DB = db
}

func memoryRow(periodType string, periodKey string, title string, content string, startedAt time.Time) models.AgentMemory {
	endedAt := startedAt.AddDate(0, 0, 1)
	switch periodType {
	case PeriodWeek:
		endedAt = startedAt.AddDate(0, 0, 7)
	case PeriodMonth:
		endedAt = startedAt.AddDate(0, 1, 0)
	}
	return models.AgentMemory{
		PeriodType:  periodType,
		PeriodKey:   periodKey,
		StartedAt:   startedAt,
		EndedAt:     endedAt,
		Title:       title,
		Content:     content,
		Importance:  80,
		SourceCount: 1,
		Model:       "memory-model",
	}
}

func assertMemoryCount(t *testing.T, periodType string, expected int64) {
	t.Helper()
	var count int64
	if err := models.DB.Model(&models.AgentMemory{}).Where("period_type = ?", periodType).Count(&count).Error; err != nil {
		t.Fatalf("统计 %s 记忆失败: %v", periodType, err)
	}
	if count != expected {
		t.Fatalf("期望 %s 记忆数量为 %d，实际为 %d", periodType, expected, count)
	}
}
