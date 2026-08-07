package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"gorm.io/gorm"
)

const (
	proxyHealthCheckDefaultInterval = 30 * time.Minute
	proxyHealthCheckPollInterval    = time.Minute
	proxyHealthCheckConcurrency     = 3
)

// StartProxyHealthCheck 启动代理自动健康检查任务。配置关闭时仅低频轮询配置，不发起网络检查。
func StartProxyHealthCheck(ctx context.Context) {
	pkg.GoSafe("admin.proxy_health_check", func() { proxyHealthCheckLoop(ctx) })
}

func proxyHealthCheckLoop(ctx context.Context) {
	var nextRun time.Time
	for {
		cfg, err := loadProxyHealthCheckConfig(ctx)
		if err != nil {
			slog.Error("读取代理自动健康检查配置失败", "error", err)
		}
		now := time.Now()
		if cfg.Enabled {
			if nextRun.IsZero() || !now.Before(nextRun) {
				runProxyHealthCheck(ctx, cfg)
				nextRun = time.Now().Add(time.Duration(cfg.IntervalMinutes) * time.Minute)
			}
		} else {
			nextRun = time.Time{}
		}

		timer := time.NewTimer(proxyHealthCheckPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func loadProxyHealthCheckConfig(ctx context.Context) (models.ProxyHealthCheckConfig, error) {
	cfg := models.ProxyHealthCheckConfig{
		Enabled:         false,
		IntervalMinutes: int(proxyHealthCheckDefaultInterval / time.Minute),
		Concurrency:     proxyHealthCheckConcurrency,
	}
	config, err := gorm.G[models.Config](models.DB).
		Where(models.ColumnEquals("key"), models.KeyProxyHealthCheck).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal([]byte(config.Value), &cfg); err != nil {
		return cfg, err
	}
	normalizeProxyHealthCheckConfig(&cfg)
	return cfg, nil
}

func normalizeProxyHealthCheckConfig(cfg *models.ProxyHealthCheckConfig) {
	if cfg.IntervalMinutes != 15 && cfg.IntervalMinutes != 30 && cfg.IntervalMinutes != 60 {
		cfg.IntervalMinutes = int(proxyHealthCheckDefaultInterval / time.Minute)
	}
	if cfg.Concurrency <= 0 || cfg.Concurrency > proxyHealthCheckConcurrency {
		cfg.Concurrency = proxyHealthCheckConcurrency
	}
}

func runProxyHealthCheck(ctx context.Context, cfg models.ProxyHealthCheckConfig) {
	var proxies []models.Proxy
	if err := models.DB.WithContext(ctx).Order("id ASC").Find(&proxies).Error; err != nil {
		slog.Error("读取代理列表失败，跳过自动健康检查", "error", err)
		return
	}
	if len(proxies) == 0 {
		return
	}
	concurrency := cfg.Concurrency
	if concurrency <= 0 || concurrency > proxyHealthCheckConcurrency {
		concurrency = proxyHealthCheckConcurrency
	}
	jobs := make(chan models.Proxy)
	done := make(chan struct{})
	worker := func() {
		defer func() { done <- struct{}{} }()
		for proxy := range jobs {
			if ctx.Err() != nil {
				return
			}
			previouslyChecked := proxy.CheckTotal > 0
			previouslyAvailable := proxy.HealthStatus == 1
			checkCtx, cancel := context.WithTimeout(ctx, proxyRegionCheckTimeout)
			result, err := checkProxy(checkCtx, proxy)
			cancel()
			if err != nil {
				slog.Warn("代理自动健康检查保存失败", "proxy_id", proxy.ID, "proxy_name", proxy.Name, "error", err)
				continue
			}
			if previouslyChecked && previouslyAvailable != result.Available {
				slog.Warn("代理健康状态发生变化",
					"proxy_id", proxy.ID,
					"proxy_name", proxy.Name,
					"from", proxyHealthStatusLabel(previouslyAvailable),
					"to", proxyHealthStatusLabel(result.Available),
					"latency_ms", result.LatencyMS,
					"error", result.Error,
				)
			}
		}
	}
	for index := 0; index < concurrency; index++ {
		go worker()
	}
	for _, proxy := range proxies {
		select {
		case jobs <- proxy:
		case <-ctx.Done():
			close(jobs)
			for index := 0; index < concurrency; index++ {
				<-done
			}
			return
		}
	}
	close(jobs)
	for index := 0; index < concurrency; index++ {
		<-done
	}
}

func proxyHealthStatusLabel(available bool) string {
	if available {
		return "可用"
	}
	return "不可用"
}
