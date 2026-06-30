package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/racio/orvion/agent"
	"github.com/racio/orvion/pkg"
)

const (
	telegramAgentMemoryRollupInterval   = time.Hour
	telegramAgentMemoryRollupRunTimeout = 2 * time.Minute
)

// StartTelegramAgentMemoryRollup 启动 TG Agent 长期记忆周/月压缩任务。
func StartTelegramAgentMemoryRollup(ctx context.Context) {
	slog.Info("TG Agent 长期记忆压缩任务已启动", "interval", telegramAgentMemoryRollupInterval.String())
	pkg.GoSafe("service.telegram_agent_memory_rollup", func() { telegramAgentMemoryRollupLoop(ctx) })
}

func telegramAgentMemoryRollupLoop(ctx context.Context) {
	runOnce := func() {
		runCtx, cancel := context.WithTimeout(ctx, telegramAgentMemoryRollupRunTimeout)
		defer cancel()

		result, err := agent.RollupTelegramAgentMemories(runCtx, time.Now())
		if err != nil {
			slog.Warn("TG Agent 长期记忆压缩失败", "error", err)
			return
		}
		if result.WeeksCreated == 0 && result.MonthsCreated == 0 && result.DaysDeleted == 0 && result.WeeksDeleted == 0 {
			return
		}
		slog.Info("TG Agent 长期记忆压缩完成",
			"weeks_created", result.WeeksCreated,
			"months_created", result.MonthsCreated,
			"days_deleted", result.DaysDeleted,
			"weeks_deleted", result.WeeksDeleted,
		)
	}

	runOnce()
	ticker := time.NewTicker(telegramAgentMemoryRollupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}
