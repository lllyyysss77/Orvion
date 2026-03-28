package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	modelProviderAutoDisableThreshold = 10
	modelProviderAutoDisableWindow    = time.Minute
	modelProviderAutoRecoverAfter     = 5 * time.Minute
	modelProviderAutoRecoverInterval  = 30 * time.Second
)

func StartModelProviderAutoRecovery(ctx context.Context) {
	go modelProviderAutoRecoveryLoop(ctx)
}

func modelProviderAutoRecoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(modelProviderAutoRecoverInterval)
	defer ticker.Stop()

	for {
		if err := RestoreExpiredAutoDisabledModelProviders(ctx); err != nil {
			slog.Error("自动恢复模型关联提供商失败", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func RestoreExpiredAutoDisabledModelProviders(ctx context.Context) error {
	now := time.Now()
	return models.DB.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Where("status = ?", 0).
		Where("auto_disabled_until IS NOT NULL").
		Where("auto_disabled_until <= ?", now).
		Updates(map[string]any{
			"status":              1,
			"auto_disabled_until": nil,
		}).Error
}

func TriggerModelProviderAutoDisableIfNeeded(ctx context.Context, modelWithProviderID uint) error {
	if modelWithProviderID == 0 {
		return nil
	}

	now := time.Now()
	windowStart := now.Add(-modelProviderAutoDisableWindow)

	logs, err := gorm.G[models.ChatLog](models.DB).
		Where("model_with_provider_id = ?", modelWithProviderID).
		Where("deleted_at IS NULL").
		Where("created_at >= ?", windowStart).
		Order("created_at DESC").
		Limit(modelProviderAutoDisableThreshold).
		Find(ctx)
	if err != nil {
		return err
	}

	if len(logs) < modelProviderAutoDisableThreshold {
		return nil
	}

	for _, log := range logs {
		if log.Status != "error" {
			return nil
		}
	}

	resumeAt := now.Add(modelProviderAutoRecoverAfter)
	result := models.DB.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Where("id = ?", modelWithProviderID).
		Where("status = ?", 1).
		Updates(map[string]any{
			"status":              0,
			"auto_disabled_until": resumeAt,
		})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected > 0 {
		slog.Warn("模型关联提供商因连续错误被自动关闭",
			"model_with_provider_id", modelWithProviderID,
			"resume_at", resumeAt.Format(time.RFC3339),
			"threshold", modelProviderAutoDisableThreshold,
			"window_seconds", int(modelProviderAutoDisableWindow/time.Second),
		)
	}

	return nil
}
