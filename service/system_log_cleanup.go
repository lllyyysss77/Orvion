package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/pkg/logutil"
	"gorm.io/gorm"
)

const defaultSystemLogCleanupIntervalMinutes = 1440

func StartSystemLogCleanup(ctx context.Context) {
	pkg.GoSafe("service.system_log_cleanup", func() { systemLogCleanupLoop(ctx) })
}

func systemLogCleanupLoop(ctx context.Context) {
	for {
		cfg, err := loadSystemLogCleanupConfig(ctx)
		if err != nil {
			slog.Error("读取系统日志自动清理配置失败", "error", err)
		}

		interval := time.Duration(cfg.IntervalMinutes) * time.Minute
		if interval <= 0 {
			interval = time.Duration(defaultSystemLogCleanupIntervalMinutes) * time.Minute
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		if !cfg.Enabled {
			continue
		}

		if err := logutil.ClearSystemLogFile(); err != nil {
			slog.Error("自动清理系统日志失败", "error", err)
		} else {
			slog.Info("系统日志已按计划自动清理")
		}
	}
}

func loadSystemLogCleanupConfig(ctx context.Context) (models.SystemLogCleanupConfig, error) {
	cfg := models.SystemLogCleanupConfig{
		Enabled:         true,
		IntervalMinutes: defaultSystemLogCleanupIntervalMinutes,
	}

	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeySystemLogCleanup).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := json.Unmarshal([]byte(config.Value), &cfg); err != nil {
		return cfg, err
	}

	if cfg.IntervalMinutes <= 0 {
		cfg.IntervalMinutes = defaultSystemLogCleanupIntervalMinutes
	}
	return cfg, nil
}
