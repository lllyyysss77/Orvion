package models

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestQueryChatLogModelUsageFiltersTimeRange(t *testing.T) {
	oldDB := DB
	clearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		DB = oldDB
		clearChatLogMonthlyTableCacheForTest()
	})

	dialector, err := buildDialector(filepath.Join(t.TempDir(), "model-usage.db"))
	if err != nil {
		t.Fatalf("build dialector: %v", err)
	}
	DB, err = gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	location := time.FixedZone("CST", 8*60*60)
	startAt := time.Date(2026, time.August, 1, 0, 0, 0, 0, location)
	endAt := startAt.AddDate(0, 1, 0)
	insideAt := startAt.Add(12 * time.Hour)
	outsideAt := startAt.AddDate(0, -1, 0)
	for _, log := range []ChatLog{
		{UUID: "inside", CreatedAt: insideAt, UpdatedAt: insideAt, Name: "model-a", Usage: Usage{TotalTokens: 120, TotalCost: 0.12}},
		{UUID: "outside", CreatedAt: outsideAt, UpdatedAt: outsideAt, Name: "model-b", Usage: Usage{TotalTokens: 900, TotalCost: 0.9}},
	} {
		tableName, ensureErr := EnsureChatLogMonthlyTable(log.CreatedAt)
		if ensureErr != nil {
			t.Fatalf("ensure table: %v", ensureErr)
		}
		if createErr := DB.Table(tableName).Create(&log).Error; createErr != nil {
			t.Fatalf("create log: %v", createErr)
		}
	}

	rows, err := QueryChatLogModelUsage(context.Background(), startAt, endAt)
	if err != nil {
		t.Fatalf("query usage: %v", err)
	}
	if len(rows) != 1 || rows[0].Model != "model-a" || rows[0].TotalTokens != 120 {
		t.Fatalf("unexpected usage rows: %+v", rows)
	}
}
