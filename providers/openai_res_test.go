package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/racio/orvion/consts"
)

func TestOpenAIResBuildReq_DefaultPath(t *testing.T) {
	provider := &OpenAIRes{
		BaseURL: "https://api.example.com",
		APIKey:  "test-key",
	}

	req, err := provider.BuildReq(context.Background(), http.Header{}, "gpt-test", []byte(`{"input":"hello"}`))
	if err != nil {
		t.Fatalf("BuildReq 返回错误: %v", err)
	}

	if req.URL.String() != "https://api.example.com/responses" {
		t.Fatalf("默认路径异常: got=%q", req.URL.String())
	}
	if req.Header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("鉴权头异常: %q", req.Header.Get("Authorization"))
	}
}

func TestOpenAIResBuildReq_EndpointOverride(t *testing.T) {
	provider := &OpenAIRes{
		BaseURL: "https://api.example.com/",
		APIKey:  "test-key",
	}

	ctx := context.WithValue(context.Background(), consts.ContextKeyOpenAIEndpoint, "responses/compact")
	req, err := provider.BuildReq(ctx, http.Header{}, "gpt-compact", []byte(`{"input":"hello"}`))
	if err != nil {
		t.Fatalf("BuildReq 返回错误: %v", err)
	}

	if req.URL.String() != "https://api.example.com/responses/compact" {
		t.Fatalf("覆盖路径异常: got=%q", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("请求体解析失败: %v", err)
	}
	if payload["model"] != "gpt-compact" {
		t.Fatalf("模型字段未正确覆盖: got=%v", payload["model"])
	}
}
