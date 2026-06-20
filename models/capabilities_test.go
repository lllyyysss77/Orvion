package models

import "testing"

func TestModelCapabilitiesScanRequiresJSONArray(t *testing.T) {
	var caps ModelCapabilities
	if err := caps.Scan(`["chat","embedding","embed","chat"]`); err != nil {
		t.Fatalf("JSON 数组能力解析失败: %v", err)
	}
	if len(caps) != 2 || caps[0] != "chat" || caps[1] != "embedding" {
		t.Fatalf("应只保留当前能力枚举并去重，实际为: %#v", caps)
	}

	if err := caps.Scan("chat,vision"); err == nil {
		t.Fatalf("旧逗号格式不应继续兼容")
	}
	if err := caps.Scan(`"chat"`); err == nil {
		t.Fatalf("JSON 字符串不应作为能力数组兼容")
	}
}

func TestProviderCapabilitiesScanRequiresJSONArray(t *testing.T) {
	var caps ProviderCapabilities
	if err := caps.Scan(`["chat","openai","responses","claude","chat"]`); err != nil {
		t.Fatalf("JSON 数组能力解析失败: %v", err)
	}
	if len(caps) != 3 || caps[0] != "chat" || caps[1] != "openai" || caps[2] != "claude" {
		t.Fatalf("应只保留当前提供商能力枚举并去重，实际为: %#v", caps)
	}

	if err := caps.Scan("chat,openai"); err == nil {
		t.Fatalf("旧逗号格式不应继续兼容")
	}
	if got := NormalizeProviderCapabilities([]string{"responses", "messages"}); len(got) != 0 {
		t.Fatalf("旧接口别名不应被归一化为提供商能力，实际为: %#v", got)
	}
}
