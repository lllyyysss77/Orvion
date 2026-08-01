package admin

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service"
)

func TestPickModelConnectivityPromptUsesConfiguredShortPrompts(t *testing.T) {
	prompt := pickModelConnectivityPrompt()
	if !slices.Contains(modelConnectivityPrompts, prompt) {
		t.Fatalf("提示词应来自预设列表，实际为 %q", prompt)
	}

	for _, item := range modelConnectivityPrompts {
		length := len([]rune(item))
		if length < 8 || length > 15 {
			t.Fatalf("提示词长度应为 8-15 个字，%q 长度为 %d", item, length)
		}
	}
}

func TestModelConnectivitySupportsChat(t *testing.T) {
	if !modelConnectivitySupportsChat(nil) {
		t.Fatal("空能力应按默认对话模型处理")
	}
	if !modelConnectivitySupportsChat(models.ModelCapabilities{"chat", "vision"}) {
		t.Fatal("包含 chat 能力时应允许连通性测试")
	}
	if modelConnectivitySupportsChat(models.ModelCapabilities{"embedding"}) {
		t.Fatal("非对话模型不应使用对话连通性测试")
	}
}

func TestBuildSingleProviderConnectivityMetaUsesOnlyTargetCandidate(t *testing.T) {
	source := &service.ProvidersWithMeta{
		ModelWithProviderMap: map[uint]models.ModelWithProvider{
			10: {ID: 10, ProviderID: 1, ProviderModel: "upstream-a"},
			11: {ID: 11, ProviderID: 2, ProviderModel: "upstream-b"},
		},
		WeightItems: map[uint]int{10: 2, 11: 1},
		ProviderMap: map[uint]models.Provider{
			1: {ID: 1, Name: "provider-a"},
			2: {ID: 2, Name: "provider-b"},
		},
		ModelID:         3,
		ModelName:       "demo-model",
		FallbackModelID: 99,
		Endpoint:        "chat",
		MaxRetry:        8,
		TimeOut:         60,
		Strategy:        "weight_round_robin",
		Breaker:         true,
	}

	single := buildSingleProviderConnectivityMeta(source, 10)
	if len(single.WeightItems) != 1 || single.WeightItems[10] != 2 {
		t.Fatalf("应只保留目标候选权重，实际 %#v", single.WeightItems)
	}
	if len(single.ModelWithProviderMap) != 1 || single.ModelWithProviderMap[10].ProviderModel != "upstream-a" {
		t.Fatalf("应只保留目标模型关联，实际 %#v", single.ModelWithProviderMap)
	}
	if len(single.ProviderMap) != 1 || single.ProviderMap[1].Name != "provider-a" {
		t.Fatalf("应只保留目标提供商，实际 %#v", single.ProviderMap)
	}
	if single.MaxRetry != 1 {
		t.Fatalf("连通性单提供商测试应只尝试一次，实际 %d", single.MaxRetry)
	}
	if single.FallbackModelID != 0 {
		t.Fatalf("单提供商连通性测试不应进入模型回退，实际 %d", single.FallbackModelID)
	}
}

func TestBuildModelConnectivityPayloadUsesStream(t *testing.T) {
	body, err := buildModelConnectivityPayload("gpt-test", "今天适合喝什么茶")
	if err != nil {
		t.Fatalf("构建连通性请求失败: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("解析连通性请求失败: %v", err)
	}
	if payload["stream"] != true {
		t.Fatalf("连通性测试应使用流式请求，实际 stream=%v", payload["stream"])
	}
}
