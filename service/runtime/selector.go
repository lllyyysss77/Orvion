package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/racio/orvion/balancers"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func NewBalancer(strategy string, breaker bool, weightItems map[uint]int) balancers.Balancer {
	var balancer balancers.Balancer
	switch strategy {
	case consts.BalancerLottery:
		balancer = balancers.NewLottery(weightItems)
	case consts.BalancerRotor:
		balancer = balancers.NewRotor(weightItems)
	default:
		balancer = balancers.NewLottery(weightItems)
	}

	if breaker {
		balancer = balancers.BalancerWrapperBreaker(balancer)
	}
	return balancer
}

func IsRetryableStatus(code int) bool {
	return code >= 500 && code <= 599
}

func LoadAnthropicProxyIPConfig(ctx context.Context) (models.AnthropicProxyIPConfig, bool) {
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyAnthropicProxyIP).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.AnthropicProxyIPConfig{}, false
		}
		slog.Error("读取全局代理 IP 配置失败", "error", err)
		return models.AnthropicProxyIPConfig{}, false
	}

	raw := strings.TrimSpace(config.Value)
	if raw == "" {
		return models.AnthropicProxyIPConfig{}, false
	}

	var cfg models.AnthropicProxyIPConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Error("解析全局代理 IP 配置失败", "error", err)
		return models.AnthropicProxyIPConfig{}, false
	}
	cfg.ProxyIP = strings.TrimSpace(cfg.ProxyIP)
	if !cfg.Enabled || cfg.ProxyIP == "" {
		return models.AnthropicProxyIPConfig{}, false
	}
	return cfg, true
}
