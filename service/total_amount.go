package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	totalAmountMu            sync.Mutex
	totalAmountPendingDelta  float64
	totalAmountFlushingDelta float64
	totalAmountFlusherStart  sync.Once
	totalAmountFlusherDone   = make(chan struct{})
	totalAmountFlusherAlive  bool
	totalAmountShutdownOnce  sync.Once
	totalAmountShutdownCh    = make(chan struct{})
	totalAmountFlushTrigger  = make(chan struct{}, 1)
)

// StartTotalAmountFlusher 启动累计金额聚合写入 goroutine。重复调用无副作用。
func StartTotalAmountFlusher(_ context.Context) {
	totalAmountFlusherStart.Do(func() {
		totalAmountFlusherAlive = true
		pkg.GoSafe("service.total_amount_flusher", totalAmountFlushLoop)
	})
}

// ShutdownTotalAmountFlusher 通知累计金额 flusher 做最后一次落库并退出。
func ShutdownTotalAmountFlusher() {
	totalAmountShutdownOnce.Do(func() { close(totalAmountShutdownCh) })
}

// WaitTotalAmountFlusher 等待累计金额 flusher 退出。
func WaitTotalAmountFlusher(timeout time.Duration) {
	if !totalAmountFlusherAlive {
		return
	}
	select {
	case <-totalAmountFlusherDone:
	case <-time.After(timeout):
		slog.Warn("累计金额 flusher 未在超时时间内退出", "timeout", timeout)
	}
}

// GetOrInitTotalConsumedAmount 获取全局累计金额（持久化于 configs.key=total_consumed_amount）。
// 规则：
// - 若配置不存在：以当前未删除日志的 total_cost 求和作为初始值写入；
// - 若配置存在但值非法：回退到当前未删除日志求和并修正配置。
func GetOrInitTotalConsumedAmount(ctx context.Context) (float64, error) {
	totalAmountMu.Lock()
	defer totalAmountMu.Unlock()

	value, err := getOrInitStoredTotalConsumedAmount(ctx)
	if err != nil {
		return 0, err
	}
	return sanitizeAmount(value + totalAmountPendingDelta + totalAmountFlushingDelta), nil
}

func getOrInitStoredTotalConsumedAmount(ctx context.Context) (float64, error) {
	cfg, err := gorm.G[models.Config](models.DB).Where(models.ColumnEquals("key"), models.KeyTotalConsumedAmount).First(ctx)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}

		initValue, sumErr := sumCurrentTotalAmount(ctx)
		if sumErr != nil {
			return 0, sumErr
		}

		if err := models.DB.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoNothing: true,
			}).
			Create(&models.Config{
				Key:   models.KeyTotalConsumedAmount,
				Value: formatAmountValue(initValue),
			}).Error; err != nil {
			return 0, err
		}

		cfg, err = gorm.G[models.Config](models.DB).Where(models.ColumnEquals("key"), models.KeyTotalConsumedAmount).First(ctx)
		if err != nil {
			return 0, err
		}
	}

	value, parseErr := parseAmountValue(cfg.Value)
	if parseErr == nil {
		return value, nil
	}

	fallback, sumErr := sumCurrentTotalAmount(ctx)
	if sumErr != nil {
		return 0, sumErr
	}
	_, _ = gorm.G[models.Config](models.DB).
		Where("id = ?", cfg.ID).
		Updates(ctx, models.Config{Value: formatAmountValue(fallback)})
	return fallback, nil
}

// AddTotalConsumedAmount 将本次请求费用累加到内存聚合队列，由后台批量落库。
func AddTotalConsumedAmount(ctx context.Context, delta float64) error {
	if !isValidPositiveAmount(delta) {
		return nil
	}

	totalAmountMu.Lock()
	totalAmountPendingDelta += delta
	shouldKick := totalAmountPendingDelta >= 1
	totalAmountMu.Unlock()

	if shouldKick {
		kickTotalAmountFlusher()
	}
	return nil
}

func totalAmountFlushLoop() {
	defer close(totalAmountFlusherDone)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-totalAmountShutdownCh:
			finalCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flushTotalAmountPending(finalCtx)
			cancel()
			return
		case <-totalAmountFlushTrigger:
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flushTotalAmountPending(flushCtx)
			cancel()
		case <-ticker.C:
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flushTotalAmountPending(flushCtx)
			cancel()
		}
	}
}

func kickTotalAmountFlusher() {
	select {
	case totalAmountFlushTrigger <- struct{}{}:
	default:
	}
}

func flushTotalAmountPending(ctx context.Context) {
	totalAmountMu.Lock()
	delta := totalAmountPendingDelta
	if !isValidPositiveAmount(delta) {
		totalAmountMu.Unlock()
		return
	}
	totalAmountPendingDelta = 0
	totalAmountFlushingDelta += delta
	totalAmountMu.Unlock()

	current, err := getOrInitStoredTotalConsumedAmount(ctx)
	if err == nil {
		next := current + delta
		_, err = gorm.G[models.Config](models.DB).
			Where(models.ColumnEquals("key"), models.KeyTotalConsumedAmount).
			Updates(ctx, models.Config{Value: formatAmountValue(next)})
	}
	totalAmountMu.Lock()
	totalAmountFlushingDelta -= delta
	if err != nil {
		totalAmountPendingDelta += delta
		slog.Warn("累计总金额批量落库失败", "error", err)
	}
	totalAmountMu.Unlock()
}

func sumCurrentTotalAmount(ctx context.Context) (float64, error) {
	total, err := models.QueryChatLogFloatSum(ctx, models.ChatLogQueryScope{}, "total_cost", "")
	if err != nil {
		return 0, err
	}
	return sanitizeAmount(total), nil
}

func parseAmountValue(raw string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, err
	}
	return sanitizeAmount(value), nil
}

func formatAmountValue(value float64) string {
	value = sanitizeAmount(value)
	return strconv.FormatFloat(value, 'f', 8, 64)
}

func sanitizeAmount(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func isValidPositiveAmount(value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	return value > 0
}
