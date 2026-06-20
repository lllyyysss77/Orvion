package models

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestDropEmptyChatLogMonthlyTablesExcept(t *testing.T) {
	oldDB := DB
	clearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		DB = oldDB
		clearChatLogMonthlyTableCacheForTest()
	})

	dialector, err := buildDialector(filepath.Join(t.TempDir(), "chat-log-partition.db"))
	if err != nil {
		t.Fatalf("build dialector: %v", err)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	DB = db

	now := time.Now()
	currentTable, err := EnsureChatLogMonthlyTable(now)
	if err != nil {
		t.Fatalf("ensure current table: %v", err)
	}
	assertChatLogMonthlyIndexes(t, currentTable)
	nextTable, err := EnsureChatLogMonthlyTable(now.AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("ensure next table: %v", err)
	}
	assertChatLogMonthlyIndexes(t, nextTable)
	emptyOldTable, err := EnsureChatLogMonthlyTable(now.AddDate(0, -2, 0))
	if err != nil {
		t.Fatalf("ensure empty old table: %v", err)
	}
	nonEmptyOldAt := now.AddDate(0, -3, 0)
	nonEmptyOldTable, err := EnsureChatLogMonthlyTable(nonEmptyOldAt)
	if err != nil {
		t.Fatalf("ensure non-empty old table: %v", err)
	}
	if err := DB.Table(nonEmptyOldTable).Create(&ChatLog{
		UUID:      "test-log-uuid",
		CreatedAt: nonEmptyOldAt,
		UpdatedAt: nonEmptyOldAt,
		Status:    "success",
	}).Error; err != nil {
		t.Fatalf("insert old log: %v", err)
	}

	dropped, err := DropEmptyChatLogMonthlyTablesExcept(context.Background(), currentTable, nextTable)
	if err != nil {
		t.Fatalf("drop empty tables: %v", err)
	}
	if !sameStringSet(dropped, []string{emptyOldTable}) {
		t.Fatalf("dropped tables mismatch: got=%v want=[%s]", dropped, emptyOldTable)
	}

	for _, tableName := range []string{currentTable, nextTable, nonEmptyOldTable} {
		if !DB.Migrator().HasTable(tableName) {
			t.Fatalf("table should be kept: %s", tableName)
		}
	}
	if DB.Migrator().HasTable(emptyOldTable) {
		t.Fatalf("empty old table should be dropped: %s", emptyOldTable)
	}
}

func TestQueryChatLogDailyUsageSummaryTopStatsUseSuccessfulRequests(t *testing.T) {
	oldDB := DB
	clearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		DB = oldDB
		clearChatLogMonthlyTableCacheForTest()
	})

	dialector, err := buildDialector(filepath.Join(t.TempDir(), "daily-summary.db"))
	if err != nil {
		t.Fatalf("build dialector: %v", err)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	DB = db

	loc := time.FixedZone("UTC+8", 8*3600)
	start := time.Date(2026, 6, 19, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	tableName, err := EnsureChatLogMonthlyTable(start)
	if err != nil {
		t.Fatalf("ensure daily table: %v", err)
	}

	rows := []ChatLog{
		{CreatedAt: start.Add(1 * time.Hour), Name: "gpt-success", AuthKeyID: 1, Status: "success", Usage: Usage{TotalCost: 0.1}, ProxyTimeMs: 100},
		{CreatedAt: start.Add(2 * time.Hour), Name: "gpt-success", AuthKeyID: 1, Status: "success", Usage: Usage{TotalCost: 0.2}, ProxyTimeMs: 120},
		{CreatedAt: start.Add(3 * time.Hour), Name: "gpt-error", AuthKeyID: 2, Status: "error", Usage: Usage{TotalCost: 0.3}, ProxyTimeMs: 900},
		{CreatedAt: start.Add(4 * time.Hour), Name: "gpt-error", AuthKeyID: 2, Status: "error", Usage: Usage{TotalCost: 0.4}, ProxyTimeMs: 950},
		{CreatedAt: start.Add(5 * time.Hour), Name: "gpt-error", AuthKeyID: 2, Status: "error", Usage: Usage{TotalCost: 0.5}, ProxyTimeMs: 1000},
	}
	if err := DB.Table(tableName).Create(&rows).Error; err != nil {
		t.Fatalf("insert daily logs: %v", err)
	}

	summary, err := QueryChatLogDailyUsageSummary(context.Background(), start, end)
	if err != nil {
		t.Fatalf("query daily summary: %v", err)
	}
	if summary.TotalRequests != 5 || summary.SuccessRequests != 2 {
		t.Fatalf("请求统计不符合预期: total=%d success=%d", summary.TotalRequests, summary.SuccessRequests)
	}
	if summary.TopModelName != "gpt-success" || summary.TopModelReqs != 2 {
		t.Fatalf("Top 模型应只统计成功请求，实际 name=%q reqs=%d", summary.TopModelName, summary.TopModelReqs)
	}
	if summary.TopAuthKeyID != 1 || summary.TopAuthKeyReqs != 2 {
		t.Fatalf("Top API Key 应只统计成功请求，实际 id=%d reqs=%d", summary.TopAuthKeyID, summary.TopAuthKeyReqs)
	}
	if summary.SlowestRequest == nil || summary.SlowestRequest.Name != "gpt-error" || summary.SlowestRequest.Status != "error" {
		t.Fatalf("最慢请求应保留全部请求口径，实际=%+v", summary.SlowestRequest)
	}
}

func TestQueryChatLogCountSupportsModelWithProviderIDFilter(t *testing.T) {
	oldDB := DB
	clearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		DB = oldDB
		clearChatLogMonthlyTableCacheForTest()
	})

	dialector, err := buildDialector(filepath.Join(t.TempDir(), "count-model-provider.db"))
	if err != nil {
		t.Fatalf("build dialector: %v", err)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	DB = db

	now := time.Now()
	windowStart := now.Add(-30 * time.Second)
	tableName, err := EnsureChatLogMonthlyTable(now)
	if err != nil {
		t.Fatalf("ensure monthly table: %v", err)
	}

	rows := []ChatLog{
		{CreatedAt: now.Add(-20 * time.Second), ModelWithProviderID: 7, Status: "error"},
		{CreatedAt: now.Add(-10 * time.Second), ModelWithProviderID: 7, Status: "error"},
		{CreatedAt: now.Add(-5 * time.Second), ModelWithProviderID: 8, Status: "error"},
		{CreatedAt: now.Add(-3 * time.Second), ModelWithProviderID: 7, Status: "success"},
	}
	if err := DB.Table(tableName).Create(&rows).Error; err != nil {
		t.Fatalf("insert logs: %v", err)
	}

	count, err := QueryChatLogCount(
		context.Background(),
		ChatLogQueryScope{StartAt: &windowStart},
		"model_with_provider_id = ? AND created_at >= ? AND status = ?",
		uint(7),
		windowStart,
		"error",
	)
	if err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func assertChatLogMonthlyIndexes(t *testing.T, tableName string) {
	t.Helper()
	for _, spec := range chatLogMonthlyIndexSpecs {
		indexName := chatLogMonthlyIndexName(tableName, spec.Name)
		if !DB.Migrator().HasIndex(tableName, indexName) {
			t.Fatalf("missing chat log monthly index %s on %s", indexName, tableName)
		}
	}
}

func clearChatLogMonthlyTableCacheForTest() {
	chatLogMonthlyTableCache.Range(func(key, _ any) bool {
		chatLogMonthlyTableCache.Delete(key)
		return true
	})
}

func sameStringSet(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(want))
	for _, item := range want {
		counts[item]++
	}
	for _, item := range got {
		if counts[item] == 0 {
			return false
		}
		counts[item]--
	}
	return true
}
