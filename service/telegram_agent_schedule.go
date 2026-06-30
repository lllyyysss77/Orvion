package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/racio/orvion/agent"
	"github.com/racio/orvion/pkg"
)

const (
	telegramAgentScheduledTaskCheckInterval = 30 * time.Second
	telegramAgentScheduledTaskBatchSize     = 5
)

func StartTelegramAgentScheduledTasks(ctx context.Context) {
	registerTelegramAgentSystemToolHooks()
	pkg.GoSafe("service.telegram_agent_scheduled_tasks", func() { telegramAgentScheduledTaskLoop(ctx) })
}

func telegramAgentScheduledTaskLoop(ctx context.Context) {
	runOnce := func() {
		tasks, err := agent.ClaimDueTelegramAgentScheduledTasks(ctx, time.Now(), telegramAgentScheduledTaskBatchSize)
		if err != nil {
			slog.Warn("读取 TG Agent 定时任务失败", "error", err)
			return
		}
		if len(tasks) == 0 {
			return
		}

		var (
			notifier       *telegramNotifier
			defaultChatID  int64
			notifierReady  bool
			notifierErr    error
			notifierLoaded bool
		)
		loadNotifier := func() (*telegramNotifier, int64, bool, error) {
			if notifierLoaded {
				return notifier, defaultChatID, notifierReady, notifierErr
			}
			notifierLoaded = true
			notifier, defaultChatID, notifierReady, notifierErr = resolveTelegramAgentScheduledTaskNotifier(ctx)
			return notifier, defaultChatID, notifierReady, notifierErr
		}

		for _, task := range tasks {
			var (
				client agent.TelegramClient
				chatID int64
				err    error
			)
			if task.PushToConversation == 1 {
				var ready bool
				notifier, chatID, ready, err = loadNotifier()
				if err == nil && !ready {
					err = errors.New("Telegram 推送配置未启用或缺少 Bot Token/Chat ID")
				}
				if err == nil {
					client = telegramAgentClient{notifier: notifier}
				}
			} else {
				_, chatID, _, _ = loadNotifier()
			}

			var result agent.TelegramAgentScheduledTaskRunResult
			if err == nil {
				result, err = agent.ExecuteTelegramAgentScheduledTask(ctx, task, client, chatID)
			}
			if finishErr := agent.FinishTelegramAgentScheduledTask(ctx, task, result, err, time.Now()); finishErr != nil {
				slog.Warn("更新 TG Agent 定时任务执行结果失败", "task_id", task.ID, "error", finishErr)
			}
			if err != nil {
				slog.Warn("执行 TG Agent 定时任务失败", "task_id", task.ID, "name", task.Name, "error", err)
			} else {
				slog.Info("已执行 TG Agent 定时任务", "task_id", task.ID, "name", task.Name)
			}
		}
	}

	runOnce()
	ticker := time.NewTicker(telegramAgentScheduledTaskCheckInterval)
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

func resolveTelegramAgentScheduledTaskNotifier(ctx context.Context) (*telegramNotifier, int64, bool, error) {
	botToken, rawChatID, apiBase, proxyURL, enabled, err := resolveTelegramRuntimeConfig(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	defaultChatID, parseErr := parseTelegramAgentScheduledTaskChatID(rawChatID)
	if parseErr != nil {
		return nil, 0, false, parseErr
	}
	notifier, ready, err := buildTelegramNotifier(botToken, rawChatID, apiBase, proxyURL, enabled)
	if err != nil {
		return nil, defaultChatID, false, err
	}
	return notifier, defaultChatID, ready, nil
}

func parseTelegramAgentScheduledTaskChatID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	chatID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || chatID == 0 {
		return 0, errors.New("Telegram Chat ID 无效")
	}
	return chatID, nil
}
