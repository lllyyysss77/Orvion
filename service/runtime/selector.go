package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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
	if code == 429 {
		return true
	}
	return code >= 500 && code <= 599
}

func IsFallbackStatus(code int) bool {
	switch code {
	case http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

func LoadForwardedIPOverrideConfig(ctx context.Context) (models.ForwardedIPOverrideConfig, bool) {
	config, err := gorm.G[models.Config](models.DB).
		Where(models.ColumnEquals("key"), models.KeyNetworkForwarding).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ForwardedIPOverrideConfig{}, false
		}
		slog.Error("读取全局代理 IP 配置失败", "error", err)
		return models.ForwardedIPOverrideConfig{}, false
	}

	raw := strings.TrimSpace(config.Value)
	if raw == "" {
		return models.ForwardedIPOverrideConfig{}, false
	}

	var cfg models.NetworkForwardingConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Error("解析全局代理 IP 配置失败", "error", err)
		return models.ForwardedIPOverrideConfig{}, false
	}
	cfg.ProxyIP = strings.TrimSpace(cfg.ProxyIP)
	if !cfg.ProxyIPEnabled || cfg.ProxyIP == "" {
		return models.ForwardedIPOverrideConfig{}, false
	}
	return models.ForwardedIPOverrideConfig{
		Enabled: true,
		ProxyIP: cfg.ProxyIP,
	}, true
}
