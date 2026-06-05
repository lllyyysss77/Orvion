package service

import (
	"context"
	"log/slog"

	"github.com/racio/orvion/limiter"
)

// 全局限流管理器
var globalLimiterManager *limiter.Manager

// SetLimiterManager 设置全局限流管理器
func SetLimiterManager(manager *limiter.Manager) {
	globalLimiterManager = manager
	slog.Info("Limiter manager initialized", "enabled", true)
}

// TryAcquireAuthKey 原子检查并记账 RPM,消除 Check/Record 两步的竞态。
func TryAcquireAuthKey(ctx context.Context, authKeyID uint, rpmLimit int) (bool, string, error) {
	if globalLimiterManager == nil {
		return true, "", nil
	}
	return globalLimiterManager.TryAcquireAuthKey(ctx, authKeyID, rpmLimit)
}

// GetRPMStats 获取RPM统计信息
func GetRPMStats(ctx context.Context) map[string]interface{} {
	if globalLimiterManager == nil {
		return map[string]interface{}{
			"enabled": false,
			"error":   "limiter not initialized",
		}
	}
	return globalLimiterManager.GetRPMStats(ctx)
}
