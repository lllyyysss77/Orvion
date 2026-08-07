package admin

import (
	"testing"

	"github.com/racio/orvion/models"
)

func TestNormalizeProxyHealthCheckConfig(t *testing.T) {
	cfg := models.ProxyHealthCheckConfig{Enabled: true, IntervalMinutes: 10, Concurrency: 99}
	normalizeProxyHealthCheckConfig(&cfg)
	if cfg.IntervalMinutes != 30 || cfg.Concurrency != proxyHealthCheckConcurrency {
		t.Fatalf("无效自动检查配置未回退默认值: %+v", cfg)
	}

	cfg = models.ProxyHealthCheckConfig{Enabled: true, IntervalMinutes: 60, Concurrency: 2}
	normalizeProxyHealthCheckConfig(&cfg)
	if cfg.IntervalMinutes != 60 || cfg.Concurrency != 2 {
		t.Fatalf("有效自动检查配置不应被修改: %+v", cfg)
	}
}

func TestProxyHealthStatusLabel(t *testing.T) {
	if got := proxyHealthStatusLabel(true); got != "可用" {
		t.Fatalf("可用状态文本异常: %q", got)
	}
	if got := proxyHealthStatusLabel(false); got != "不可用" {
		t.Fatalf("不可用状态文本异常: %q", got)
	}
}
