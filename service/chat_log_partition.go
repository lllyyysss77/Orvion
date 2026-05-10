package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
)

const chatLogPartitionEnsureInterval = 12 * time.Hour

var chatLogMonthlyBackfillOnce sync.Once

// StartChatLogMonthlyPartitionWorker 启动日志按月分表维护任务（预建当月和下月分表）。
func StartChatLogMonthlyPartitionWorker(ctx context.Context) {
	pkg.GoSafe("service.chat_log_partition_worker", func() { chatLogPartitionLoop(ctx) })
}

func chatLogPartitionLoop(ctx context.Context) {
	chatLogMonthlyBackfillOnce.Do(func() {
		affected, err := models.BackfillChatLogsToMonthly(ctx)
		if err != nil {
			slog.Error("历史日志回填到月表失败", "error", err)
			return
		}
		if affected > 0 {
			slog.Info("历史日志回填到月表完成", "rows", affected)
		}
	})

	for {
		if err := ensureCurrentAndNextMonthChatLogTables(); err != nil {
			slog.Error("预创建日志分表失败", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(chatLogPartitionEnsureInterval):
		}
	}
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
