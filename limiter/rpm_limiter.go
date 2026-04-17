package limiter

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RPMLimiter RPM 限流器（内存实现）
type RPMLimiter struct {
	memory *sync.Map
}

// RequestRecord 请求记录
type RequestRecord struct {
	Timestamps []int64 `json:"timestamps"`
}

// NewRPMLimiter 创建新的 RPM 限流器
func NewRPMLimiter() *RPMLimiter {
	return &RPMLimiter{
		memory: &sync.Map{},
	}
}

// CheckRPMLimit 检查是否达到 RPM 限制
func (r *RPMLimiter) CheckRPMLimit(_ context.Context, authKeyID uint, rpmLimit int) (bool, error) {
	// 0 表示无限制
	if rpmLimit <= 0 {
		return true, nil
	}

	now := time.Now().Unix()
	windowStart := now - 60 // 1 分钟窗口
	return r.checkRPMLimitMemory(authKeyID, rpmLimit, windowStart), nil
}

// RecordRequest 记录一次请求
func (r *RPMLimiter) RecordRequest(_ context.Context, authKeyID uint) error {
	r.recordRequestMemory(authKeyID, time.Now().Unix())
	return nil
}

// GetCurrentRPMCount 获取当前 RPM 计数
func (r *RPMLimiter) GetCurrentRPMCount(_ context.Context, authKeyID uint) (int, error) {
	now := time.Now().Unix()
	windowStart := now - 60
	return r.getCurrentRPMCountMemory(authKeyID, windowStart), nil
}

// getRPMKey 获取 RPM 存储键
func (r *RPMLimiter) getRPMKey(authKeyID uint) string {
	return fmt.Sprintf("rpm:auth_key:%d", authKeyID)
}

func (r *RPMLimiter) checkRPMLimitMemory(authKeyID uint, rpmLimit int, windowStart int64) bool {
	key := r.getRPMKey(authKeyID)

	value, exists := r.memory.Load(key)
	if !exists {
		return true
	}

	record, ok := value.(*RequestRecord)
	if !ok {
		return true
	}

	// 过滤窗口内请求并回写
	validTimestamps := make([]int64, 0, len(record.Timestamps))
	for _, ts := range record.Timestamps {
		if ts > windowStart {
			validTimestamps = append(validTimestamps, ts)
		}
	}
	record.Timestamps = validTimestamps
	r.memory.Store(key, record)

	return len(validTimestamps) < rpmLimit
}

func (r *RPMLimiter) recordRequestMemory(authKeyID uint, now int64) {
	key := r.getRPMKey(authKeyID)

	value, exists := r.memory.Load(key)
	var record *RequestRecord
	if exists {
		record, _ = value.(*RequestRecord)
	}

	if record == nil {
		record = &RequestRecord{
			Timestamps: make([]int64, 0),
		}
	}

	record.Timestamps = append(record.Timestamps, now)

	// 仅保留最近 2 分钟数据
	windowStart := now - 120
	validTimestamps := make([]int64, 0, len(record.Timestamps))
	for _, ts := range record.Timestamps {
		if ts > windowStart {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	record.Timestamps = validTimestamps
	r.memory.Store(key, record)
}

func (r *RPMLimiter) getCurrentRPMCountMemory(authKeyID uint, windowStart int64) int {
	key := r.getRPMKey(authKeyID)

	value, exists := r.memory.Load(key)
	if !exists {
		return 0
	}

	record, ok := value.(*RequestRecord)
	if !ok {
		return 0
	}

	count := 0
	for _, ts := range record.Timestamps {
		if ts > windowStart {
			count++
		}
	}
	return count
}

// ClearMemoryData 清理内存数据（用于测试）
func (r *RPMLimiter) ClearMemoryData() {
	r.memory = &sync.Map{}
}

// GetStats 获取限流器统计信息
func (r *RPMLimiter) GetStats(_ context.Context) map[string]interface{} {
	authKeyStats := make(map[string]int)
	r.memory.Range(func(key, value interface{}) bool {
		keyStr, keyOK := key.(string)
		record, valOK := value.(*RequestRecord)
		if keyOK && valOK {
			authKeyStats[keyStr] = len(record.Timestamps)
		}
		return true
	})

	return map[string]interface{}{
		"storage_type": "memory",
		"auth_keys":    authKeyStats,
	}
}
