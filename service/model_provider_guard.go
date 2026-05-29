package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
)

const (
	modelProviderAutoDisableThreshold = 10
	modelProviderAutoDisableWindow    = 30 * time.Second
	modelProviderAutoRecoverAfter     = 5 * time.Minute
	modelProviderAutoRecoverInterval  = 30 * time.Second
	modelProviderAutoDisableWorkers   = 4
	modelProviderAutoDisableQueueSize = 1024
)

var (
	autoDisableQueueStart sync.Once
	autoDisableQueue      = make(chan uint, modelProviderAutoDisableQueueSize)
	autoDisablePendingMu  sync.Mutex
	autoDisablePending    = make(map[uint]struct{})
)

func StartModelProviderAutoRecovery(ctx context.Context) {
	StartModelProviderAutoDisableQueue(ctx)
	pkg.GoSafe("service.model_provider_auto_recovery", func() { modelProviderAutoRecoveryLoop(ctx) })
}

func StartModelProviderAutoDisableQueue(ctx context.Context) {
	autoDisableQueueStart.Do(func() {
		for i := 0; i < modelProviderAutoDisableWorkers; i++ {
			workerID := i + 1
			pkg.GoSafe("service.model_provider_auto_disable_worker", func() {
				modelProviderAutoDisableWorker(ctx, workerID)
			})
		}
	})
}

func ScheduleModelProviderAutoDisableCheck(modelWithProviderID uint) {
	if modelWithProviderID == 0 {
		return
	}
	StartModelProviderAutoDisableQueue(RootContext())

	autoDisablePendingMu.Lock()
	if _, exists := autoDisablePending[modelWithProviderID]; exists {
		autoDisablePendingMu.Unlock()
		return
	}
	autoDisablePending[modelWithProviderID] = struct{}{}
	autoDisablePendingMu.Unlock()

	select {
	case autoDisableQueue <- modelWithProviderID:
	default:
		autoDisablePendingMu.Lock()
		delete(autoDisablePending, modelWithProviderID)
		autoDisablePendingMu.Unlock()
		slog.Warn("模型关联提供商自动关闭检查队列已满，丢弃检查任务",
			"model_with_provider_id", modelWithProviderID,
			"queue_size", modelProviderAutoDisableQueueSize,
		)
	}
}

func modelProviderAutoDisableWorker(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		case modelWithProviderID := <-autoDisableQueue:
			modelProviderAutoDisableWorkerHandle(ctx, workerID, modelWithProviderID)
		}
	}
}

func modelProviderAutoDisableWorkerHandle(ctx context.Context, workerID int, modelWithProviderID uint) {
	defer func() {
		autoDisablePendingMu.Lock()
		delete(autoDisablePending, modelWithProviderID)
		autoDisablePendingMu.Unlock()
	}()

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := TriggerModelProviderAutoDisableIfNeeded(checkCtx, modelWithProviderID); err != nil {
		slog.Error("检查模型关联提供商自动关闭失败",
			"error", err,
			"worker", workerID,
			"model_with_provider_id", modelWithProviderID,
		)
	}
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

	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{StartAt: &windowStart}, "uuid, created_at, model_with_provider_id, status")
	if err != nil {
		return err
	}
	if union.SQL == "" {
		return nil
	}
	var errorCount int64
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT COUNT(1)
		   FROM (`+union.SQL+`) AS logs
		  WHERE model_with_provider_id = ?
		    AND created_at >= ?
		    AND status = ?`,
		modelWithProviderID,
		windowStart,
		"error",
	).Scan(&errorCount).Error; err != nil {
		return err
	}

	if errorCount < modelProviderAutoDisableThreshold {
		return nil
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
		slog.Warn("模型关联提供商因短时间错误过多被自动关闭",
			"model_with_provider_id", modelWithProviderID,
			"resume_at", resumeAt.Format(time.RFC3339),
			"threshold", modelProviderAutoDisableThreshold,
			"error_count", errorCount,
			"window_seconds", int(modelProviderAutoDisableWindow/time.Second),
		)
		pkg.GoSafe("service.model_provider_auto_disable_alert", func() {
			if err := SendModelProviderAutoDisableAlert(RootContext(), ModelProviderAutoDisableAlertEvent{
				ModelWithProviderID: modelWithProviderID,
				ResumeAt:            resumeAt,
				Threshold:           modelProviderAutoDisableThreshold,
				Window:              modelProviderAutoDisableWindow,
			}); err != nil {
				if errors.Is(err, ErrTelegramNotifierNotConfigured) {
					return
				}
				slog.Warn("发送模型关联提供商自动关闭 TG 告警失败",
					"error", err,
					"model_with_provider_id", modelWithProviderID,
				)
			}
		})
	}

	return nil
}
