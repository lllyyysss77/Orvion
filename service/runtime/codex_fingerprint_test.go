package runtime

import (
	"net/http"
	"testing"

	"github.com/racio/orvion/models"
)

func TestApplyCodexFingerprintHeadersReplacesClientFeatureHeaders(t *testing.T) {
	header := http.Header{
		"Authorization":       []string{"Bearer user-token"},
		"OpenAI-Beta":         []string{"responses=v1"},
		"User-Agent":          []string{"some-client/1.0"},
		"Version":             []string{"0.1.0"},
		"X-Client-Request-Id": []string{"client-request"},
		"X-Custom":            []string{"keep"},
	}

	got := ApplyCodexFingerprintHeaders(header, models.CodexFingerprintConfig{
		Enabled: true,
		Headers: map[string]string{
			"User-Agent": "codex-tui/0.135.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.135.0)",
			"Originator": "codex-tui",
		},
	}, true)

	if got.Get("User-Agent") != "codex-tui/0.135.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.135.0)" {
		t.Fatalf("User-Agent 未替换为 Codex 特征: %q", got.Get("User-Agent"))
	}
	if got.Get("Originator") != "codex-tui" {
		t.Fatalf("Originator 未写入 Codex 特征: %q", got.Get("Originator"))
	}
	if got.Get("Accept") != "text/event-stream" {
		t.Fatalf("流式请求 Accept 未替换为 Codex 特征: %q", got.Get("Accept"))
	}
	if got.Get("Connection") != "Keep-Alive" {
		t.Fatalf("Connection 未替换为 Codex 特征: %q", got.Get("Connection"))
	}
	if got.Get("Version") != "" || got.Get("X-Client-Request-Id") != "" || got.Get("OpenAI-Beta") != "" {
		t.Fatalf("旧客户端特征头未清理干净: version=%q request_id=%q beta=%q", got.Get("Version"), got.Get("X-Client-Request-Id"), got.Get("OpenAI-Beta"))
	}
	if got.Get("Session_id") == "" {
		t.Fatalf("Mac OS Codex User-Agent 应自动生成 Session_id")
	}
	if got.Get("Authorization") != "Bearer user-token" {
		t.Fatalf("不应在指纹模拟中删除 Authorization")
	}
	if got.Get("X-Custom") != "keep" {
		t.Fatalf("不应删除无关自定义请求头")
	}
}

func TestApplyCodexFingerprintHeadersSkipsWhenDisabled(t *testing.T) {
	header := http.Header{"User-Agent": []string{"some-client/1.0"}}

	got := ApplyCodexFingerprintHeaders(header, models.CodexFingerprintConfig{
		Enabled: false,
		Headers: map[string]string{
			"User-Agent": "codex-tui/0.135.0",
		},
	}, false)

	if got.Get("User-Agent") != "some-client/1.0" {
		t.Fatalf("关闭时不应修改请求头: %q", got.Get("User-Agent"))
	}
}

func TestApplyCodexFingerprintHeadersUsesJSONAcceptForNonStream(t *testing.T) {
	got := ApplyCodexFingerprintHeaders(http.Header{}, models.CodexFingerprintConfig{
		Enabled: true,
		Headers: map[string]string{
			"User-Agent": "codex-tui/0.135.0",
			"Originator": "codex-tui",
		},
	}, false)

	if got.Get("Accept") != "application/json" {
		t.Fatalf("非流式请求 Accept 未替换为 Codex 特征: %q", got.Get("Accept"))
	}
}
