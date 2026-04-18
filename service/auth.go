package service

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// ========= AuthKey 查询缓存 =========
//
// 热点 AuthKey 每次请求都会命中 GetAuthKey,直接打 DB 压力大。
// 这里加一层进程内 TTL 缓存:命中则零 DB 开销,未命中经 singleflight 合并并发回源。
// 对于 Auth 变更(CreateAuthKey/UpdateAuthKey/DeleteAuthKey/ToggleAuthKeyStatus),
// handler 层显式调用 InvalidateAuthKeys() 做整表失效,简单可靠。

const authKeyCacheTTL = 30 * time.Second

type authKeyCacheEntry struct {
	key       *models.AuthKey
	err       error
	expiresAt time.Time
}

var (
	authKeyCache  sync.Map // map[string]*authKeyCacheEntry
	authKeyGroup  singleflight.Group
	authKeyCacheV atomic.Int64 // 版本号,每次 Invalidate 递增
)

// GetAuthKey 读取指定 plaintext key 对应的 AuthKey。带 TTL 缓存 + singleflight。
func GetAuthKey(ctx context.Context, key string) (*models.AuthKey, error) {
	if v, ok := authKeyCache.Load(key); ok {
		entry := v.(*authKeyCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.key, entry.err
		}
		authKeyCache.Delete(key)
	}

	versionAtCall := authKeyCacheV.Load()

	ch := authKeyGroup.DoChan(key, func() (any, error) {
		// auth_keys.status 在数据库中是 0/1(int),不能用 bool 参数查询
		authKey, err := gorm.G[models.AuthKey](models.DB).Where("key = ?", key).Where("status = ?", 1).First(ctx)
		if err != nil {
			return (*models.AuthKey)(nil), err
		}
		return &authKey, nil
	})

	select {
	case r := <-ch:
		var result *models.AuthKey
		if r.Val != nil {
			result, _ = r.Val.(*models.AuthKey)
		}
		// 回源期间若发生了 Invalidate,则不要写入缓存(否则会回写脏值)。
		if versionAtCall == authKeyCacheV.Load() {
			authKeyCache.Store(key, &authKeyCacheEntry{
				key:       result,
				err:       r.Err,
				expiresAt: time.Now().Add(authKeyCacheTTL),
			})
		}
		return result, r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// InvalidateAuthKeys 清空 AuthKey 缓存。AuthKey 的增删改都应调用一次。
func InvalidateAuthKeys() {
	authKeyCacheV.Add(1)
	authKeyCache.Range(func(k, _ any) bool {
		authKeyCache.Delete(k)
		return true
	})
}

// ========= 使用次数/消费金额后台聚合 =========
//
// 每个请求都同步 UPDATE auth_keys 代价太大,这里在内存聚合,由后台 goroutine
// 按固定节奏(默认 10s)批量落库,并在进程关闭时做最后一次 flush。

type KeyUpdateItem struct {
	Count  int
	Cost   float64
	UsedAt time.Time
}

const (
	// authKeyUpdateSoftCap 达到此数量触发一次提前 flush(避免等到下一个 10s tick)。
	authKeyUpdateSoftCap = 10_000
	// authKeyUpdateHardCap 达到此数量后新 key 的更新会被丢弃,防止 DB 持续不可用时 OOM。
	// 已存在的 key 继续累加,不会受此上限影响。
	authKeyUpdateHardCap = 50_000
)

var (
	flushMu       sync.Mutex
	updateCounts  = make(map[uint]KeyUpdateItem)
	flusherStart  sync.Once
	flusherDone   = make(chan struct{})
	flusherActive bool

	shutdownOnce sync.Once
	shutdownCh   = make(chan struct{})

	// flushTrigger 允许 KeyUpdate/KeyCostUpdate 在 map 超软上限时主动唤醒 flusher。
	flushTrigger = make(chan struct{}, 1)

	// lastOverflowWarnUnix 丢弃日志的速率限制(每 30s 最多一条)。
	lastOverflowWarnUnix atomic.Int64
)

// StartAuthKeyFlusher 启动后台聚合 flush goroutine。重复调用无副作用。
// 退出由 ShutdownAuthKeyFlusher 显式触发,而非跟随 ctx 取消——这是为了
// 让 server.Shutdown 期间仍在进行的请求所产生的记账也能被最后一次 flush 捕获。
func StartAuthKeyFlusher(_ context.Context) {
	flusherStart.Do(func() {
		flusherActive = true
		pkg.GoSafe("service.auth_key_flusher", authKeyFlushLoop)
	})
}

// ShutdownAuthKeyFlusher 通知 flusher 做最后一次 flush 并退出。
// main 应在 server.Shutdown 返回之后再调用,确保在途请求都已完成记账。
func ShutdownAuthKeyFlusher() {
	shutdownOnce.Do(func() { close(shutdownCh) })
}

// WaitAuthKeyFlusher 阻塞等待 flusher goroutine 退出。
// 若未启动或超时,立即返回。
func WaitAuthKeyFlusher(timeout time.Duration) {
	if !flusherActive {
		return
	}
	select {
	case <-flusherDone:
	case <-time.After(timeout):
		slog.Warn("AuthKey flusher did not finish within timeout", "timeout", timeout)
	}
}

func KeyUpdate(keyID uint, usedAt time.Time) {
	if keyID == 0 {
		return
	}
	flushMu.Lock()
	// 只对新 key 做硬上限检查;已存在的 key 直接累加不占新槽位。
	if _, exists := updateCounts[keyID]; !exists && len(updateCounts) >= authKeyUpdateHardCap {
		flushMu.Unlock()
		warnAuthKeyFlusherOverflow()
		return
	}
	item := updateCounts[keyID]
	item.Count++
	if item.UsedAt.IsZero() || usedAt.After(item.UsedAt) {
		item.UsedAt = usedAt
	}
	updateCounts[keyID] = item
	shouldKick := len(updateCounts) >= authKeyUpdateSoftCap
	flushMu.Unlock()
	if shouldKick {
		kickAuthKeyFlusher()
	}
}

func KeyCostUpdate(keyID uint, cost float64, usedAt time.Time) {
	if keyID == 0 || cost <= 0 {
		return
	}
	flushMu.Lock()
	if _, exists := updateCounts[keyID]; !exists && len(updateCounts) >= authKeyUpdateHardCap {
		flushMu.Unlock()
		warnAuthKeyFlusherOverflow()
		return
	}
	item := updateCounts[keyID]
	item.Cost += cost
	if item.UsedAt.IsZero() || usedAt.After(item.UsedAt) {
		item.UsedAt = usedAt
	}
	updateCounts[keyID] = item
	shouldKick := len(updateCounts) >= authKeyUpdateSoftCap
	flushMu.Unlock()
	if shouldKick {
		kickAuthKeyFlusher()
	}
}

func kickAuthKeyFlusher() {
	select {
	case flushTrigger <- struct{}{}:
	default:
	}
}

func warnAuthKeyFlusherOverflow() {
	now := time.Now().Unix()
	prev := lastOverflowWarnUnix.Load()
	if now-prev < 30 {
		return
	}
	if !lastOverflowWarnUnix.CompareAndSwap(prev, now) {
		return
	}
	slog.Warn("AuthKey 聚合 map 已达硬上限,丢弃新 key 的更新",
		"hard_cap", authKeyUpdateHardCap,
	)
}

func authKeyFlushLoop() {
	defer close(flusherDone)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-shutdownCh:
			// 进程退出前兜底 flush:用独立 ctx,避免主 ctx 已取消后写不进去。
			finalCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flushPendingKeyUpdates(finalCtx)
			cancel()
			return
		case <-flushTrigger:
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flushPendingKeyUpdates(flushCtx)
			cancel()
		case <-ticker.C:
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flushPendingKeyUpdates(flushCtx)
			cancel()
		}
	}
}

func flushPendingKeyUpdates(ctx context.Context) {
	flushMu.Lock()
	if len(updateCounts) == 0 {
		flushMu.Unlock()
		return
	}
	pending := updateCounts
	updateCounts = make(map[uint]KeyUpdateItem)
	flushMu.Unlock()

	for keyID, item := range pending {
		updates := map[string]any{
			"usage_count":  gorm.Expr("COALESCE(usage_count, 0) + ?", item.Count),
			"last_used_at": item.UsedAt,
		}
		if item.Cost != 0 {
			updates["total_cost"] = gorm.Expr("COALESCE(total_cost, 0) + ?", item.Cost)
		}
		if err := models.DB.Model(&models.AuthKey{}).WithContext(ctx).Where("id = ?", keyID).Updates(updates).Error; err != nil {
			slog.Error("Failed to update auth key usage count", "error", err, "auth_key_id", keyID)
		}
	}
}
