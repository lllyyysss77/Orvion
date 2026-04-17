package limiter

import (
	"context"
	"log/slog"
)

// Manager 限流管理器
type Manager struct {
	rpmLimiter *RPMLimiter
	enabled    bool
}

// NewManager 创建新的限流管理器
func NewManager() *Manager {
	return &Manager{
		rpmLimiter: NewRPMLimiter(),
		enabled:    true,
	}
}

// SetEnabled 设置限流器是否启用
func (m *Manager) SetEnabled(enabled bool) {
	m.enabled = enabled
}

// IsEnabled 检查限流器是否启用
func (m *Manager) IsEnabled() bool {
	return m.enabled
}

// CheckRPMLimit 检查 RPM 限制
func (m *Manager) CheckRPMLimit(ctx context.Context, authKeyID uint, rpmLimit int) (bool, error) {
	if !m.enabled {
		return true, nil
	}
	return m.rpmLimiter.CheckRPMLimit(ctx, authKeyID, rpmLimit)
}

// RecordRPMRequest 记录 RPM 请求
func (m *Manager) RecordRPMRequest(ctx context.Context, authKeyID uint) error {
	if !m.enabled {
		return nil
	}
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
func (m *Manager) CheckAuthKeyLimits(ctx context.Context, authKeyID uint, rpmLimit int) (bool, string, error) {
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
