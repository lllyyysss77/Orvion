package limiter

import (
	"context"
	"log/slog"
)

// Manager 限流管理器
type Manager struct {
	rpmLimiter *RPMLimiter
}

// NewManager 创建新的限流管理器
func NewManager() *Manager {
	return &Manager{
		rpmLimiter: NewRPMLimiter(),
	}
}

// GetRPMStats 获取RPM统计信息
func (m *Manager) GetRPMStats(ctx context.Context) map[string]interface{} {
	stats := m.rpmLimiter.GetStats(ctx)
	stats["enabled"] = true
	return stats
}

// TryAcquireAuthKey 原子地执行 check+record,消除 Check/Record 两步调用的竞态。
// 返回 (是否通过, 拒绝原因, 错误)。rpmLimit<=0 时不限流。
func (m *Manager) TryAcquireAuthKey(ctx context.Context, authKeyID uint, rpmLimit int) (bool, string, error) {
	if rpmLimit <= 0 {
		return true, "", nil
	}
	ok, err := m.rpmLimiter.TryAcquire(ctx, authKeyID, rpmLimit)
	if err != nil {
		slog.Warn("RPM acquire failed", "auth_key_id", authKeyID, "error", err)
		return false, "limiter_unavailable", err
	}
	if !ok {
		return false, "rpm_limit_exceeded", nil
	}
	return true, "", nil
}
