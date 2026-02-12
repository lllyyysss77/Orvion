package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var totalAmountMu sync.Mutex

// GetOrInitTotalConsumedAmount 获取全局累计金额（持久化于 configs.key=total_consumed_amount）。
// 规则：
// - 若配置不存在：以当前未删除日志的 total_cost 求和作为初始值写入；
// - 若配置存在但值非法：回退到当前未删除日志求和并修正配置。
func GetOrInitTotalConsumedAmount(ctx context.Context) (float64, error) {
	cfg, err := gorm.G[models.Config](models.DB).Where("key = ?", models.KeyTotalConsumedAmount).First(ctx)
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

		cfg, err = gorm.G[models.Config](models.DB).Where("key = ?", models.KeyTotalConsumedAmount).First(ctx)
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

// AddTotalConsumedAmount 将本次请求费用累加到全局累计金额。
func AddTotalConsumedAmount(ctx context.Context, delta float64) error {
	if !isValidPositiveAmount(delta) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	totalAmountMu.Lock()
	defer totalAmountMu.Unlock()

	current, err := GetOrInitTotalConsumedAmount(ctx)
	if err != nil {
		return err
	}

	next := current + delta
	_, err = gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyTotalConsumedAmount).
		Updates(ctx, models.Config{Value: formatAmountValue(next)})
	return err
}

func sumCurrentTotalAmount(ctx context.Context) (float64, error) {
	var total sql.NullFloat64
	if err := models.DB.WithContext(ctx).
		Model(&models.ChatLog{}).
		Where("deleted_at IS NULL").
		Select("COALESCE(SUM(total_cost),0) AS total").
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return sanitizeAmount(total.Float64), nil
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
