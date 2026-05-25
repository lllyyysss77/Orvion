package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
)

const chatLogPartitionEnsureInterval = 12 * time.Hour

// StartChatLogMonthlyPartitionWorker 启动日志按月分表维护任务。
func StartChatLogMonthlyPartitionWorker(ctx context.Context) {
	pkg.GoSafe("service.chat_log_partition_worker", func() { chatLogPartitionLoop(ctx) })
}

func chatLogPartitionLoop(ctx context.Context) {
	for {
		if err := maintainChatLogMonthlyTables(ctx); err != nil {
			slog.Error("维护日志分表失败", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(chatLogPartitionEnsureInterval):
		}
	}
}

func maintainChatLogMonthlyTables(ctx context.Context) error {
	if err := ensureCurrentAndNextMonthChatLogTables(); err != nil {
		return err
	}

	now := time.Now()
	currentTable := models.ChatLogMonthlyTableName(now)
	nextTable := models.ChatLogMonthlyTableName(now.AddDate(0, 1, 0))
	dropped, err := models.DropEmptyChatLogMonthlyTablesExcept(ctx, currentTable, nextTable)
	if err != nil {
		return err
	}
	if len(dropped) > 0 {
		slog.Info("已清理空日志分表", "tables", dropped)
	}
	return nil
}

func ensureCurrentAndNextMonthChatLogTables() error {
	now := time.Now()
	if _, err := models.EnsureChatLogMonthlyTable(now); err != nil {
		return err
	}
	if _, err := models.EnsureChatLogMonthlyTable(now.AddDate(0, 1, 0)); err != nil {
		return err
	}
	return nil
}
