package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIModelsFallbacksWithoutAPIKeyWhenAuthorizedListIsEmpty(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/models" {
			t.Fatalf("请求路径不正确: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"qwen-image-2.0","object":"model","created":1782465808}]}`))
	}))
	defer server.Close()

	client := OpenAI{
		BaseURL: server.URL,
		APIKey:  "sk-test",
	}
	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("获取模型列表失败: %v", err)
	}
	if calls != 2 {
		t.Fatalf("期望带 key 和无 key 各请求一次，实际请求次数: %d", calls)
	}
	if len(models) != 1 || models[0].ID != "qwen-image-2.0" {
		t.Fatalf("兜底模型列表不正确: %+v", models)
	}
}
