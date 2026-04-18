package service

import (
	"fmt"
	"testing"
)

func TestResolveTelegramProxyCandidatesPreferExplicit(t *testing.T) {
	t.Setenv(envGitHubHTTPProxyForTG, "http://127.0.0.1:8999")
	t.Setenv(envGitHubXraySocksPortForTG, "19090")

	candidates, err := resolveTelegramProxyCandidates("http://127.0.0.1:7890", true)
	if err != nil {
		t.Fatalf("解析显式代理失败: %v", err)
	}
	if len(candidates) != 1 || candidates[0] != "http://127.0.0.1:7890" {
		t.Fatalf("显式代理应优先，got=%v", candidates)
	}
}

func TestResolveTelegramProxyCandidatesNoFallbackWhenDisabled(t *testing.T) {
	t.Setenv(envGitHubHTTPProxyForTG, "http://127.0.0.1:8999")
	t.Setenv(envGitHubXraySocksPortForTG, "19090")

	candidates, err := resolveTelegramProxyCandidates("", false)
	if err != nil {
		t.Fatalf("解析候选失败: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("禁用内置回退时应直连，got=%v", candidates)
	}
}

func TestResolveTelegramProxyCandidatesBuiltinFallback(t *testing.T) {
	t.Setenv(envGitHubHTTPProxyForTG, "http://127.0.0.1:8999")
	t.Setenv(envGitHubXraySocksPortForTG, "19090")

	candidates, err := resolveTelegramProxyCandidates("", true)
	if err != nil {
		t.Fatalf("解析内置候选失败: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("内置候选数量异常，got=%v", candidates)
	}
	if candidates[0] != "http://127.0.0.1:8999" {
		t.Fatalf("应优先共享 HTTP 代理，got=%v", candidates)
	}
	if candidates[1] != "socks5://127.0.0.1:19090" {
		t.Fatalf("应包含内置 socks 代理，got=%v", candidates)
	}
}

func TestTelegramProxySelectorSwitchOnProbeFailure(t *testing.T) {
	oldProbe := telegramProxyProbeFunc
	defer func() { telegramProxyProbeFunc = oldProbe }()

	state := map[string]bool{
		"http://127.0.0.1:8001": false,
		"http://127.0.0.1:8002": true,
	}
	telegramProxyProbeFunc = func(proxyURL string) bool {
		return state[proxyURL]
	}

	selector := &telegramProxySelector{
		candidates: []string{"http://127.0.0.1:8001", "http://127.0.0.1:8002"},
	}

	picked := selector.pickProxyURL()
	if picked != "http://127.0.0.1:8002" {
		t.Fatalf("应自动切换到可用候选，got=%q", picked)
	}
}

func TestParseTelegramBuiltinSocksPortInvalidFallbackDefault(t *testing.T) {
	t.Setenv(envGitHubXraySocksPortForTG, "invalid")
	if got := parseTelegramBuiltinSocksPort(); got != defaultTelegramBuiltinSocksPort {
		t.Fatalf("非法端口应回退默认值，got=%d", got)
	}
}

func TestResolveTelegramProxyCandidatesRejectInvalidSharedProxy(t *testing.T) {
	t.Setenv(envGitHubHTTPProxyForTG, "127.0.0.1:8999")
	t.Setenv(envGitHubXraySocksPortForTG, "19090")

	_, err := resolveTelegramProxyCandidates("", true)
	if err == nil {
		t.Fatalf("无 scheme 的共享代理应报错")
	}
	if got := fmt.Sprint(err); got == "" {
		t.Fatalf("期望有错误信息")
	}
}
