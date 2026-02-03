package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/racio/orvion/models"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

var singleFlightGroup singleflight.Group

func GetAuthKey(ctx context.Context, key string) (*models.AuthKey, error) {
	ch := singleFlightGroup.DoChan(key, func() (any, error) {
		// auth_keys.status 在数据库中是 0/1（int），不能用 bool 参数查询
		authKey, err := gorm.G[models.AuthKey](models.DB).Where("key = ?", key).Where("status = ?", 1).First(ctx)
		return &authKey, err
	})

	select {
	case r := <-ch:
		return r.Val.(*models.AuthKey), r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type KeyUpdateItem struct {
	Count  int
	Cost   float64
	UsedAt time.Time
}

var (
	updateCounts = make(map[uint]KeyUpdateItem)
	mu           sync.Mutex
	startOnce    sync.Once
)

func KeyUpdate(keyID uint, usedAt time.Time) {
	mu.Lock()
	item := updateCounts[keyID]
	item.Count++
	if item.UsedAt.IsZero() || usedAt.After(item.UsedAt) {
		item.UsedAt = usedAt
	}
	updateCounts[keyID] = item
	mu.Unlock()

	// 确保后台刷新协程只启动一次
	startOnce.Do(func() {
		go backgroundFlush()
	})
}

func KeyCostUpdate(keyID uint, cost float64, usedAt time.Time) {
	if keyID == 0 || cost <= 0 {
		return
	}
	mu.Lock()
	item := updateCounts[keyID]
	item.Cost += cost
	if item.UsedAt.IsZero() || usedAt.After(item.UsedAt) {
		item.UsedAt = usedAt
	}
	updateCounts[keyID] = item
	mu.Unlock()

	startOnce.Do(func() {
		go backgroundFlush()
	})
}

func backgroundFlush() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		mu.Lock()
		pending := updateCounts
		updateCounts = make(map[uint]KeyUpdateItem) // 清空计数，避免阻塞写入
		mu.Unlock()

		if len(pending) == 0 {
			continue
		}

		ctx := context.Background()
		for keyID, item := range pending {
			updates := map[string]any{
				"usage_count":  gorm.Expr("usage_count + ?", item.Count),
				"last_used_at": item.UsedAt,
			}
			if item.Cost != 0 {
				updates["total_cost"] = gorm.Expr("total_cost + ?", item.Cost)
			}
			if err := models.DB.Model(&models.AuthKey{}).WithContext(ctx).Where("id = ?", keyID).Updates(updates).Error; err != nil {
				slog.Error("Failed to update auth key usage count", "error", err)
			}
		}
	}
}
