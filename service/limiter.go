package service

import (
	"context"
	"log/slog"

	"github.com/go-redis/redis/v8"
	"github.com/racio/orvion/limiter"
)

// 全局限流管理器
var globalLimiterManager *limiter.Manager

// SetLimiterManager 设置全局限流管理器
func SetLimiterManager(manager *limiter.Manager) {
	globalLimiterManager = manager
	slog.Info("Limiter manager initialized", "enabled", manager.IsEnabled())
}

// GetRedisClient 获取Redis客户端
func GetRedisClient() *redis.Client {
	if globalLimiterManager == nil {
		return nil
	}
	return globalLimiterManager.GetRedisClient()
}

// CheckAuthKeyLimits 检查 API Key 限制
func CheckAuthKeyLimits(ctx context.Context, authKeyID uint, rpmLimit int, modelWithProviderID uint) (bool, string, error) {
	if globalLimiterManager == nil {
		return true, "", nil
	}
	return globalLimiterManager.CheckAuthKeyLimits(ctx, authKeyID, rpmLimit, modelWithProviderID, authKeyID)
}

// RecordAuthKeyAccess 记录 API Key 访问
func RecordAuthKeyAccess(ctx context.Context, authKeyID uint, rpmLimit int) error {
	if globalLimiterManager == nil {
		return nil
	}
	return globalLimiterManager.RecordAuthKeyAccess(ctx, authKeyID, rpmLimit)
}

// GetCurrentRPMCount 获取当前 RPM 计数
func GetCurrentRPMCount(ctx context.Context, authKeyID uint) (int, error) {
	if globalLimiterManager == nil {
		return 0, nil
	}
	return globalLimiterManager.GetCurrentRPMCount(ctx, authKeyID)
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
