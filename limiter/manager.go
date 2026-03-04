package limiter

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// Manager 限流管理器
type Manager struct {
	rpmLimiter   *RPMLimiter
	tokenLocker  *TokenLocker
	redisClient  *redis.Client
	enabled      bool
	redisTimeout time.Duration
}

// NewManager 创建新的限流管理器
func NewManager(redisClient *redis.Client) *Manager {
	redisTimeout := 300 * time.Millisecond
	if v := os.Getenv("REDIS_OP_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			redisTimeout = time.Duration(ms) * time.Millisecond
		}
	}
	return &Manager{
		rpmLimiter:   NewRPMLimiter(redisClient),
		tokenLocker:  NewTokenLocker(redisClient, 2*time.Minute),
		redisClient:  redisClient,
		enabled:      true,
		redisTimeout: redisTimeout,
	}
}

func (m *Manager) withRedisTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	// 内存模式不需要额外超时控制
	if m.redisClient == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, m.redisTimeout)
}

// SetEnabled 设置限流器是否启用
func (m *Manager) SetEnabled(enabled bool) {
	m.enabled = enabled
}

// IsEnabled 检查限流器是否启用
func (m *Manager) IsEnabled() bool {
	return m.enabled
}

// GetRedisClient 获取Redis客户端
func (m *Manager) GetRedisClient() *redis.Client {
	return m.redisClient
}

// CheckRPMLimit 检查 RPM 限制
func (m *Manager) CheckRPMLimit(ctx context.Context, authKeyID uint, rpmLimit int) (bool, error) {
	if !m.enabled {
		return true, nil
	}
	ctx, cancel := m.withRedisTimeout(ctx)
	defer cancel()
	return m.rpmLimiter.CheckRPMLimit(ctx, authKeyID, rpmLimit)
}

// RecordRPMRequest 记录 RPM 请求
func (m *Manager) RecordRPMRequest(ctx context.Context, authKeyID uint) error {
	if !m.enabled {
		return nil
	}
	ctx, cancel := m.withRedisTimeout(ctx)
	defer cancel()
	return m.rpmLimiter.RecordRequest(ctx, authKeyID)
}

// GetRPMStats 获取RPM统计信息
func (m *Manager) GetRPMStats(ctx context.Context) map[string]interface{} {
	if !m.enabled {
		return map[string]interface{}{
			"enabled": false,
		}
	}
	stats := m.rpmLimiter.GetStats(ctx)
	stats["enabled"] = true
	return stats
}

// CheckAuthKeyLimits 检查 API Key 的所有限制
func (m *Manager) CheckAuthKeyLimits(ctx context.Context, authKeyID uint, rpmLimit int, modelWithProviderID uint, tokenID uint) (bool, string, error) {
	if !m.enabled {
		return true, "", nil
	}

	// 检查 RPM 限制
	if rpmLimit > 0 {
		canProceed, err := m.CheckRPMLimit(ctx, authKeyID, rpmLimit)
		if err != nil {
			slog.Warn("RPM limit check failed", "auth_key_id", authKeyID, "error", err)
			// 用户选择 fail-closed：限流依赖不可用时直接拒绝
			return false, "limiter_unavailable", err
		} else if !canProceed {
			return false, "rpm_limit_exceeded", nil
		}
	}

	// token 独占锁
	if tokenID > 0 && modelWithProviderID > 0 && m.tokenLocker != nil {
		ok, err := m.tokenLocker.CheckAndTouch(ctx, modelWithProviderID, tokenID)
		if err != nil {
			slog.Warn("Token lock check failed", "auth_key_id", authKeyID, "model_with_provider_id", modelWithProviderID, "token_id", tokenID, "error", err)
			return false, "limiter_unavailable", err
		}
		if !ok {
			return false, "token_access_denied", nil
		}
	}

	return true, "", nil
}

// RecordAuthKeyAccess 记录 API Key 访问
func (m *Manager) RecordAuthKeyAccess(ctx context.Context, authKeyID uint, rpmLimit int) error {
	if !m.enabled {
		return nil
	}

	// 记录 RPM 请求
	if rpmLimit > 0 {
		if err := m.RecordRPMRequest(ctx, authKeyID); err != nil {
			slog.Warn("Failed to record RPM request", "auth_key_id", authKeyID, "error", err)
		}
	}

	return nil
}

// GetCurrentRPMCount 获取当前 RPM 计数
func (m *Manager) GetCurrentRPMCount(ctx context.Context, authKeyID uint) (int, error) {
	if !m.enabled {
		return 0, nil
	}
	return m.rpmLimiter.GetCurrentRPMCount(ctx, authKeyID)
}

// ClearMemoryData 清理内存数据（用于测试）
func (m *Manager) ClearMemoryData() {
	if m.rpmLimiter != nil {
		m.rpmLimiter.ClearMemoryData()
	}
}
