package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agenttools "github.com/racio/orvion/agent/tools"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg/logutil"
)

func registerTelegramAgentSystemToolHooks() {
	agenttools.SetTelegramAgentSystemToolHooks(agenttools.TelegramAgentSystemToolHooks{
		GetSystemStatus:       telegramAgentToolGetSystemStatus,
		GetPerformanceStats:   telegramAgentToolGetPerformanceStats,
		ListImageCache:        telegramAgentToolListImageCache,
		DeleteImageCache:      telegramAgentToolDeleteImageCache,
		RefreshImageCache:     telegramAgentToolRefreshImageCache,
		GetBackgroundTasks:    telegramAgentToolGetBackgroundTasks,
		TriggerBackgroundTask: telegramAgentToolTriggerBackgroundTask,
	})
}

func telegramAgentToolGetSystemStatus(ctx context.Context, _ agenttools.TelegramAgentSystemToolRequest) (string, error) {
	return buildTelegramSystemStatusMessage(ctx), nil
}

func telegramAgentToolGetPerformanceStats(ctx context.Context, req agenttools.TelegramAgentSystemToolRequest) (string, error) {
	recentMinutes := req.RecentMinutes
	if recentMinutes <= 0 {
		recentMinutes = 60
	}
	if recentMinutes > 10080 {
		recentMinutes = 10080
	}
	start := time.Now().Add(-time.Duration(recentMinutes) * time.Minute)

	var agg models.ChatLogMetricsAgg
	if models.DB != nil {
		var err error
		agg, err = models.QueryChatLogMetricsAgg(ctx, models.ChatLogQueryScope{StartAt: &start}, "created_at >= ?", start)
		if err != nil {
			return "", err
		}
	}

	failed := agg.Reqs - agg.Success
	if failed < 0 {
		failed = 0
	}
	successRate := 0.0
	if agg.Reqs > 0 {
		successRate = float64(agg.Success) * 100 / float64(agg.Reqs)
	}
	avgLatency := 0.0
	if agg.Reqs > 0 {
		avgLatency = float64(agg.TimeMs) / float64(agg.Reqs)
	}

	slow := models.SnapshotSlowSQLStats()
	lines := []string{
		"性能统计",
		fmt.Sprintf("请求窗口：最近 %d 分钟", recentMinutes),
		fmt.Sprintf("请求总数：%d", agg.Reqs),
		fmt.Sprintf("成功 / 失败：%d / %d", agg.Success, failed),
		fmt.Sprintf("成功率：%.2f%%", successRate),
		fmt.Sprintf("平均耗时：%.2fms", avgLatency),
		"",
		"慢 SQL 统计",
		fmt.Sprintf("统计窗口：最近 %d 条 SQL", slow.WindowSize),
		fmt.Sprintf("慢 SQL 阈值：%dms", slow.ThresholdMs),
		fmt.Sprintf("SQL 总数：%d", slow.TotalQueries),
		fmt.Sprintf("慢 SQL 数：%d", slow.SlowQueries),
		fmt.Sprintf("SQL 正常率：%.2f%%", slow.NormalRate),
		fmt.Sprintf("慢 SQL 占比：%.2f%%", slow.SlowRate),
	}
	return strings.Join(lines, "\n"), nil
}

func telegramAgentToolListImageCache(_ context.Context, _ agenttools.TelegramAgentSystemToolRequest) (string, error) {
	return formatTelegramAgentImageCacheSnapshot(ListTelegramStatusImageCache()), nil
}

func telegramAgentToolDeleteImageCache(_ context.Context, req agenttools.TelegramAgentSystemToolRequest) (string, error) {
	if req.All {
		cleared := clearTelegramStatusImageWindow()
		scheduleTelegramStatusImageRefillWithTrigger(RootContext(), "agent_tool_delete_all")
		return fmt.Sprintf("已清空图片缓存：%d 张。\n%s", cleared, formatTelegramAgentImageCacheSnapshot(ListTelegramStatusImageCache())), nil
	}
	if req.CacheID == 0 {
		return "", errors.New("请提供 cache_id，或传 all=true 清空全部图片缓存")
	}
	if !DeleteTelegramStatusImageCacheItem(req.CacheID) {
		return "", fmt.Errorf("未找到图片缓存：%d", req.CacheID)
	}
	return fmt.Sprintf("已删除图片缓存：%d。\n%s", req.CacheID, formatTelegramAgentImageCacheSnapshot(ListTelegramStatusImageCache())), nil
}

func telegramAgentToolRefreshImageCache(_ context.Context, req agenttools.TelegramAgentSystemToolRequest) (string, error) {
	clearExisting := true
	if req.ClearExisting != nil {
		clearExisting = *req.ClearExisting
	}
	cleared := 0
	if clearExisting {
		cleared = clearTelegramStatusImageWindow()
	}
	scheduleTelegramStatusImageRefillWithTrigger(RootContext(), "agent_tool_refresh")
	if clearExisting {
		return fmt.Sprintf("已触发图片缓存刷新，已清空旧缓存 %d 张，后台会自动补充到容量上限。\n%s", cleared, formatTelegramAgentImageCacheSnapshot(ListTelegramStatusImageCache())), nil
	}
	return "已触发图片缓存补充，后台会补齐缺失缓存。\n" + formatTelegramAgentImageCacheSnapshot(ListTelegramStatusImageCache()), nil
}

func telegramAgentToolGetBackgroundTasks(ctx context.Context, _ agenttools.TelegramAgentSystemToolRequest) (string, error) {
	priceCfg, priceErr := loadPriceSyncConfig(ctx)
	cleanupCfg, cleanupErr := loadSystemLogCleanupConfig(ctx)

	imageCache := ListTelegramStatusImageCache()
	telegramStatusImageWindowMu.Lock()
	imageRefillRunning := telegramStatusImageRefillRunning
	telegramStatusImageWindowMu.Unlock()

	var scheduledTotal int64
	var scheduledEnabled int64
	var scheduledRunning int64
	if models.DB != nil {
		db := models.DB.WithContext(ctx).Model(&models.TelegramAgentScheduledTask{})
		_ = db.Count(&scheduledTotal).Error
		_ = db.Where("enabled = ?", 1).Count(&scheduledEnabled).Error
		_ = db.Where("last_status = ?", "running").Count(&scheduledRunning).Error
	}

	lines := []string{"后台任务状态"}
	if priceErr != nil {
		lines = append(lines, "模型价格同步：读取配置失败："+priceErr.Error())
	} else {
		lines = append(lines, fmt.Sprintf("模型价格同步：%s，间隔 %d 分钟", enabledText(priceCfg.Enabled), priceCfg.IntervalMinutes))
	}
	if cleanupErr != nil {
		lines = append(lines, "系统日志清理：读取配置失败："+cleanupErr.Error())
	} else {
		lines = append(lines, fmt.Sprintf("系统日志清理：%s，间隔 %d 分钟", enabledText(cleanupCfg.Enabled), cleanupCfg.IntervalMinutes))
	}
	lines = append(lines,
		fmt.Sprintf("日志输出/token 回填：启用，间隔 %s，单次最多 %d 条", chatLogOutputSizeBackfillInterval, chatLogOutputSizeBackfillMaxRowsPerRun),
		fmt.Sprintf("图片缓存补充：%s，缓存 %d/%d，大小 %s", runningText(imageRefillRunning), imageCache.Total, imageCache.Capacity, formatBytesBinary(uint64(imageCache.Bytes))),
		fmt.Sprintf("Agent 定时任务扫描：启用，间隔 %s，任务 %d 个，启用 %d 个，运行中 %d 个", telegramAgentScheduledTaskCheckInterval, scheduledTotal, scheduledEnabled, scheduledRunning),
		fmt.Sprintf("日志分表维护：启用，间隔 %s", chatLogPartitionEnsureInterval),
		fmt.Sprintf("模型提供商自动关闭队列：启用，worker %d 个", modelProviderAutoDisableWorkers),
	)
	return strings.Join(lines, "\n"), nil
}

func telegramAgentToolTriggerBackgroundTask(ctx context.Context, req agenttools.TelegramAgentSystemToolRequest) (string, error) {
	switch normalizeBackgroundTaskName(req.Task) {
	case "model_price_sync":
		if err := TriggerModelPriceSync(ctx); err != nil {
			return "", err
		}
		return "已触发模型价格同步。", nil
	case "system_log_cleanup":
		if err := logutil.ClearSystemLogFile(); err != nil {
			return "", err
		}
		return "已清理系统日志文件。", nil
	case "token_backfill":
		runCtx, cancel := context.WithTimeout(ctx, chatLogOutputSizeBackfillRunTimeout)
		defer cancel()
		updated, err := BackfillChatLogOutputSizes(runCtx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已触发日志输出/token 回填，更新 %d 条日志。", updated), nil
	case "image_cache_refill":
		scheduleTelegramStatusImageRefillWithTrigger(RootContext(), "agent_tool_trigger")
		return "已触发图片缓存补充。", nil
	default:
		return "", errors.New("未知后台任务，可选：model_price_sync、system_log_cleanup、token_backfill、image_cache_refill")
	}
}

func formatTelegramAgentImageCacheSnapshot(snapshot TelegramStatusImageCacheSnapshot) string {
	lines := []string{
		"图片缓存",
		fmt.Sprintf("数量：%d/%d", snapshot.Total, snapshot.Capacity),
		fmt.Sprintf("总大小：%s", formatBytesBinary(uint64(snapshot.Bytes))),
	}
	if len(snapshot.Items) == 0 {
		lines = append(lines, "当前没有缓存图片。")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "明细：")
	for _, item := range snapshot.Items {
		lines = append(lines, fmt.Sprintf("- #%d ID:%d %s｜%s｜%s｜%s",
			item.Index,
			item.ID,
			emptyTextFallback(item.FileName, "unknown"),
			formatBytesBinary(uint64(item.Size)),
			emptyTextFallback(item.MIMEType, "unknown"),
			emptyTextFallback(item.CachedAt, "unknown"),
		))
	}
	return strings.Join(lines, "\n")
}

func normalizeBackgroundTaskName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "price_sync", "model_price", "model_prices", "模型价格同步":
		return "model_price_sync"
	case "log_cleanup", "system_logs_cleanup", "system_log", "系统日志清理":
		return "system_log_cleanup"
	case "completion_token_backfill", "output_size_backfill", "chat_log_backfill", "token补全", "token回填":
		return "token_backfill"
	case "image_cache", "status_image_cache", "status_image_refill", "图片缓存", "补图片缓存":
		return "image_cache_refill"
	default:
		return value
	}
}

func enabledText(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "关闭"
}

func runningText(running bool) string {
	if running {
		return "运行中"
	}
	return "空闲"
}

func emptyTextFallback(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
