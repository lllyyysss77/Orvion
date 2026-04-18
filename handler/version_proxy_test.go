package handler

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseVMessLink(t *testing.T) {
	payload := `{"add":"example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","aid":"0","net":"tcp","type":"none","host":"","path":"","tls":"tls"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	cfg, err := parseVMessLink("vmess://" + encoded)
	if err != nil {
		t.Fatalf("parseVMessLink 返回错误: %v", err)
	}
	if cfg.Address != "example.com" || cfg.Port != "443" {
		t.Fatalf("解析结果异常: %+v", cfg)
	}
}

func TestParseTrojanLink(t *testing.T) {
	link := "trojan://pass-123@example.com:8443?type=tcp&security=tls&sni=iosapps.itunes.apple.com&allowInsecure=1&udp=1"
	cfg, err := parseTrojanLink(link)
	if err != nil {
		t.Fatalf("parseTrojanLink 返回错误: %v", err)
	}
	if cfg.Address != "example.com" || cfg.Port != "8443" || cfg.Password != "pass-123" {
		t.Fatalf("trojan 基础字段解析异常: %+v", cfg)
	}
	if !cfg.AllowInsecure || !cfg.UDP {
		t.Fatalf("trojan 布尔参数解析异常: %+v", cfg)
	}
	if cfg.SNI != "iosapps.itunes.apple.com" {
		t.Fatalf("trojan sni 解析异常: %+v", cfg)
	}
}

func TestBuildXrayConfigJSONWithTrojan(t *testing.T) {
	link := "trojan://pass-123@example.com:8443?type=tcp&security=tls&sni=iosapps.itunes.apple.com&allowInsecure=1&udp=true"
	content, err := buildXrayConfigJSON(link, 17890)
	if err != nil {
		t.Fatalf("buildXrayConfigJSON 返回错误: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(content, &cfg); err != nil {
		t.Fatalf("xray 配置 JSON 解析失败: %v", err)
	}

	inbounds, ok := cfg["inbounds"].([]any)
	if !ok || len(inbounds) == 0 {
		t.Fatalf("inbounds 字段异常: %v", cfg["inbounds"])
	}
	in0, ok := inbounds[0].(map[string]any)
	if !ok {
		t.Fatalf("inbounds[0] 类型异常: %T", inbounds[0])
	}
	settings, ok := in0["settings"].(map[string]any)
	if !ok {
		t.Fatalf("inbounds[0].settings 类型异常: %T", in0["settings"])
	}
	udpEnabled, ok := settings["udp"].(bool)
	if !ok || !udpEnabled {
		t.Fatalf("trojan udp 未生效: %v", settings["udp"])
	}

	outbounds, ok := cfg["outbounds"].([]any)
	if !ok || len(outbounds) == 0 {
		t.Fatalf("outbounds 字段异常: %v", cfg["outbounds"])
	}
	out0, ok := outbounds[0].(map[string]any)
	if !ok {
		t.Fatalf("outbounds[0] 类型异常: %T", outbounds[0])
	}
	protocol, _ := out0["protocol"].(string)
	if !strings.EqualFold(protocol, "trojan") {
		t.Fatalf("outbound 协议异常: %v", out0["protocol"])
	}
}

func TestResolveProxyCandidatesDefaultOrder(t *testing.T) {
	t.Setenv(envGitHubVMessURL, "")
	candidates := resolveProxyCandidates()
	if len(candidates) != 3 {
		t.Fatalf("默认候选数量异常: got=%d", len(candidates))
	}

	expectedNames := []string{
		"trojan-jp01-3x",
		"trojan-sg01-3x",
		"trojan-fallback",
	}
	for idx, name := range expectedNames {
		if candidates[idx].name != name {
			t.Fatalf("默认候选顺序异常: idx=%d got=%q want=%q", idx, candidates[idx].name, name)
		}
	}

	for idx, candidate := range candidates {
		if !strings.HasPrefix(candidate.link, "trojan://") {
			t.Fatalf("默认 Trojan 候选异常: idx=%d link=%q", idx, candidate.link)
		}
	}
}

func TestResolveProxyCandidatesEnvOverride(t *testing.T) {
	custom := "trojan://pass@example.com:443?security=tls&sni=example.com"
	t.Setenv(envGitHubVMessURL, custom)

	candidates := resolveProxyCandidates()
	if len(candidates) != 1 {
		t.Fatalf("环境变量覆盖失败: got=%d", len(candidates))
	}
	if candidates[0].name != "env-github-vmess-url" || candidates[0].link != custom {
		t.Fatalf("环境变量候选异常: %+v", candidates[0])
	}
}

func TestFindProxyCandidateByLink(t *testing.T) {
	candidates := []proxyCandidate{
		{name: "a", link: "trojan://a"},
		{name: "b", link: "trojan://b"},
	}
	got := findProxyCandidateByLink(candidates, "trojan://b")
	if got.name != "b" {
		t.Fatalf("候选匹配失败: %+v", got)
	}
}

func TestRotateProxyCandidatesFromNext(t *testing.T) {
	candidates := []proxyCandidate{
		{name: "a", link: "trojan://a"},
		{name: "b", link: "trojan://b"},
		{name: "c", link: "trojan://c"},
	}
	got := rotateProxyCandidatesFromNext(candidates, "trojan://b")
	if len(got) != 3 {
		t.Fatalf("轮转后数量异常: %d", len(got))
	}
	expected := []string{"c", "a", "b"}
	for i, name := range expected {
		if got[i].name != name {
			t.Fatalf("轮转顺序异常: idx=%d got=%q want=%q", i, got[i].name, name)
		}
	}
}
