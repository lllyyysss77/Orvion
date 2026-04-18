package limiter

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// rpmBucket 每个 AuthKey 的滑动窗口桶,持有互斥锁保证 check+record 原子。
type rpmBucket struct {
	mu         sync.Mutex
	timestamps []int64 // 单调递增的 Unix 秒时间戳
}

// trim 移除窗口外的时间戳 (调用方需持锁)。
func (b *rpmBucket) trim(windowStart int64) {
	// 时间戳单调递增,找到首个 > windowStart 的位置即可。
	drop := 0
	for drop < len(b.timestamps) && b.timestamps[drop] <= windowStart {
		drop++
	}
	if drop > 0 {
		b.timestamps = append(b.timestamps[:0], b.timestamps[drop:]...)
	}
}

// RPMLimiter RPM 限流器(内存实现,per-key 锁消除竞态)
type RPMLimiter struct {
	buckets sync.Map // map[uint]*rpmBucket
}

// NewRPMLimiter 创建新的 RPM 限流器
func NewRPMLimiter() *RPMLimiter {
	return &RPMLimiter{}
}

func (r *RPMLimiter) bucket(authKeyID uint) *rpmBucket {
	if v, ok := r.buckets.Load(authKeyID); ok {
		return v.(*rpmBucket)
	}
	b := &rpmBucket{}
	actual, _ := r.buckets.LoadOrStore(authKeyID, b)
	return actual.(*rpmBucket)
}

// TryAcquire 原子地检查配额并记账:在窗口未满时直接登记一次,返回 true;
// 已满则不登记,返回 false。rpmLimit<=0 表示不限流。
func (r *RPMLimiter) TryAcquire(_ context.Context, authKeyID uint, rpmLimit int) (bool, error) {
	if rpmLimit <= 0 {
		return true, nil
	}
	now := time.Now().Unix()
	windowStart := now - 60

	b := r.bucket(authKeyID)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.trim(windowStart)
	if len(b.timestamps) >= rpmLimit {
		return false, nil
	}
	b.timestamps = append(b.timestamps, now)
	return true, nil
}

// CheckRPMLimit 只读判断当前是否还有配额。保留用于向后兼容。
// 注意:与 RecordRequest 组合使用存在竞态,新代码请用 TryAcquire。
func (r *RPMLimiter) CheckRPMLimit(_ context.Context, authKeyID uint, rpmLimit int) (bool, error) {
	if rpmLimit <= 0 {
		return true, nil
	}
	now := time.Now().Unix()
	windowStart := now - 60

	b := r.bucket(authKeyID)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.trim(windowStart)
	return len(b.timestamps) < rpmLimit, nil
}

// RecordRequest 记录一次请求。保留用于向后兼容。
func (r *RPMLimiter) RecordRequest(_ context.Context, authKeyID uint) error {
	now := time.Now().Unix()
	windowStart := now - 120 // 保留 2 分钟数据,避免切片无界增长

	b := r.bucket(authKeyID)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.trim(windowStart)
	b.timestamps = append(b.timestamps, now)
	return nil
}

// GetCurrentRPMCount 获取当前窗口内计数
func (r *RPMLimiter) GetCurrentRPMCount(_ context.Context, authKeyID uint) (int, error) {
	now := time.Now().Unix()
	windowStart := now - 60

	v, ok := r.buckets.Load(authKeyID)
	if !ok {
		return 0, nil
	}
	b := v.(*rpmBucket)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.trim(windowStart)
	return len(b.timestamps), nil
}

// ClearMemoryData 清理内存数据(测试用)
func (r *RPMLimiter) ClearMemoryData() {
	r.buckets.Range(func(key, _ any) bool {
		r.buckets.Delete(key)
		return true
	})
}

// GetStats 获取限流器统计信息
func (r *RPMLimiter) GetStats(_ context.Context) map[string]interface{} {
	authKeyStats := make(map[string]int)
	r.buckets.Range(func(key, value any) bool {
		id, idOK := key.(uint)
		b, bOK := value.(*rpmBucket)
		if !idOK || !bOK {
			return true
		}
		b.mu.Lock()
		count := len(b.timestamps)
		b.mu.Unlock()
		authKeyStats[rpmStatsKey(id)] = count
		return true
	})

	return map[string]interface{}{
		"storage_type": "memory",
		"auth_keys":    authKeyStats,
	}
}

func rpmStatsKey(id uint) string {
	// 保持与旧版一致的 key 前缀,避免前端展示破坏
	return "rpm:auth_key:" + strconv.FormatUint(uint64(id), 10)
}
