package admin

import "testing"

func TestNormalizeTelegramAgentDirectModelsBaseURL(t *testing.T) {
	baseURL, err := normalizeTelegramAgentDirectModelsBaseURL("https://api.example.com/v1/chat/completions?unused=1")
	if err != nil {
		t.Fatalf("归一化 TG Agent 模型 URL 失败: %v", err)
	}
	if baseURL != "https://api.example.com/v1" {
		t.Fatalf("URL 归一化不正确: %s", baseURL)
	}
}
