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
	nextTable, err := EnsureChatLogMonthlyTable(now.AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("ensure next table: %v", err)
	}
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
