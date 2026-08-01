package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestBackfillChatLogOutputSizesUpdatesMissingSize(t *testing.T) {
	oldDB := models.DB
	modelsDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "backfill-output-size.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	models.DB = modelsDB
	models.ClearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		models.DB = oldDB
		models.ClearChatLogMonthlyTableCacheForTest()
	})

	if err := modelsDB.AutoMigrate(&models.ChatIO{}); err != nil {
		t.Fatalf("migrate chat_io: %v", err)
	}

	createdAt := time.Date(2026, 6, 17, 10, 0, 0, 0, time.Local)
	tableName, err := models.EnsureChatLogMonthlyTable(createdAt)
	if err != nil {
		t.Fatalf("ensure monthly table: %v", err)
	}

	logRow := models.ChatLog{
		UUID:      "log-success-1",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Status:    "success",
		ChatIO:    1,
		Size:      0,
	}
	if err := modelsDB.Table(tableName).Create(&logRow).Error; err != nil {
		t.Fatalf("create log row: %v", err)
	}

	if err := modelsDB.Create(&models.ChatIO{
		LogId:        logRow.ID,
		LogUUID:      logRow.UUID,
		Input:        `{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`,
		OutputString: "hello world",
	}).Error; err != nil {
		t.Fatalf("create chat_io row: %v", err)
	}

	updated, err := BackfillChatLogOutputSizes(context.Background())
	if err != nil {
		t.Fatalf("backfill output sizes: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated rows mismatch: got=%d want=1", updated)
	}

	var refreshed models.ChatLog
	if err := modelsDB.Table(tableName).Where("id = ?", logRow.ID).Take(&refreshed).Error; err != nil {
		t.Fatalf("reload log row: %v", err)
	}
	if refreshed.Size != len("hello world") {
		t.Fatalf("size mismatch: got=%d want=%d", refreshed.Size, len("hello world"))
	}
}

func TestBackfillChatLogOutputSizesSkipsRowsWithoutOutput(t *testing.T) {
	oldDB := models.DB
	modelsDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "backfill-output-skip.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	models.DB = modelsDB
	models.ClearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		models.DB = oldDB
		models.ClearChatLogMonthlyTableCacheForTest()
	})

	if err := modelsDB.AutoMigrate(&models.ChatIO{}); err != nil {
		t.Fatalf("migrate chat_io: %v", err)
	}

	createdAt := time.Date(2026, 6, 17, 10, 5, 0, 0, time.Local)
	tableName, err := models.EnsureChatLogMonthlyTable(createdAt)
	if err != nil {
		t.Fatalf("ensure monthly table: %v", err)
	}

	successWithoutOutput := models.ChatLog{
		UUID:      "log-success-empty",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Status:    "success",
		ChatIO:    1,
		Size:      0,
	}
	if err := modelsDB.Table(tableName).Create(&successWithoutOutput).Error; err != nil {
		t.Fatalf("create success log row: %v", err)
	}
	if err := modelsDB.Create(&models.ChatIO{
		LogId:   successWithoutOutput.ID,
		LogUUID: successWithoutOutput.UUID,
		Input:   `{"messages":[{"role":"user","content":"hi"}]}`,
	}).Error; err != nil {
		t.Fatalf("create empty chat_io row: %v", err)
	}

	errorLog := models.ChatLog{
		UUID:      "log-error-with-output",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Status:    "error",
		ChatIO:    1,
		Size:      0,
	}
	if err := modelsDB.Table(tableName).Create(&errorLog).Error; err != nil {
		t.Fatalf("create error log row: %v", err)
	}
	if err := modelsDB.Create(&models.ChatIO{
		LogId:        errorLog.ID,
		LogUUID:      errorLog.UUID,
		Input:        `{"messages":[{"role":"user","content":"hi"}]}`,
		OutputString: "should not backfill",
	}).Error; err != nil {
		t.Fatalf("create error chat_io row: %v", err)
	}

	updated, err := BackfillChatLogOutputSizes(context.Background())
	if err != nil {
		t.Fatalf("backfill output sizes: %v", err)
	}
	if updated != 0 {
		t.Fatalf("updated rows mismatch: got=%d want=0", updated)
	}

	var refreshedSuccess models.ChatLog
	if err := modelsDB.Table(tableName).Where("id = ?", successWithoutOutput.ID).Take(&refreshedSuccess).Error; err != nil {
		t.Fatalf("reload success log row: %v", err)
	}
	if refreshedSuccess.Size != 0 {
		t.Fatalf("success log without output should stay 0, got=%d", refreshedSuccess.Size)
	}

	var refreshedError models.ChatLog
	if err := modelsDB.Table(tableName).Where("id = ?", errorLog.ID).Take(&refreshedError).Error; err != nil {
		t.Fatalf("reload error log row: %v", err)
	}
	if refreshedError.Size != 0 {
		t.Fatalf("error log should stay 0, got=%d", refreshedError.Size)
	}
}

func TestBackfillChatLogOutputSizesFillsCompletionTokensFromChatIO(t *testing.T) {
	oldDB := models.DB
	modelsDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "backfill-completion-tokens.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	models.DB = modelsDB
	models.ClearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		models.DB = oldDB
		models.ClearChatLogMonthlyTableCacheForTest()
	})

	if err := modelsDB.AutoMigrate(&models.ChatIO{}); err != nil {
		t.Fatalf("migrate chat_io: %v", err)
	}

	createdAt := time.Date(2026, 6, 17, 18, 0, 0, 0, time.Local)
	tableName, err := models.EnsureChatLogMonthlyTable(createdAt)
	if err != nil {
		t.Fatalf("ensure monthly table: %v", err)
	}

	logRow := models.ChatLog{
		UUID:      "raw-output-log-1",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Status:    "success",
		Name:      "custom-batch-v1",
		Style:     "openai",
		ChatIO:    1,
		Size:      493,
		Usage: models.Usage{
			PromptTokens:     17,
			CompletionTokens: 0,
			TotalTokens:      17,
		},
	}
	if err := modelsDB.Table(tableName).Create(&logRow).Error; err != nil {
		t.Fatalf("create log row: %v", err)
	}

	if err := modelsDB.Create(&models.ChatIO{
		LogId:        logRow.ID,
		LogUUID:      logRow.UUID,
		Input:        `{"messages":[{"role":"user","content":"处理这段内容"}]}`,
		OutputString: `{"completed_at":1781689211,"status":"completed","result_id":"job_123"}`,
	}).Error; err != nil {
		t.Fatalf("create chat_io row: %v", err)
	}

	updated, err := BackfillChatLogOutputSizes(context.Background())
	if err != nil {
		t.Fatalf("backfill output stats: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated rows mismatch: got=%d want=1", updated)
	}

	var refreshed models.ChatLog
	if err := modelsDB.Table(tableName).Where("id = ?", logRow.ID).Take(&refreshed).Error; err != nil {
		t.Fatalf("reload log row: %v", err)
	}
	if refreshed.CompletionTokens <= 0 {
		t.Fatalf("completion tokens should be backfilled, got=%d", refreshed.CompletionTokens)
	}
	if refreshed.TotalTokens <= refreshed.PromptTokens {
		t.Fatalf("total tokens should be increased, got prompt=%d total=%d", refreshed.PromptTokens, refreshed.TotalTokens)
	}
}
