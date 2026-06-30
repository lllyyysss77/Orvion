package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestBuildTelegramAgentImageRequestBodyUsesExtraBodyResponseFormat(t *testing.T) {
	body, err := buildTelegramAgentImageRequestBody("agnes-image", "一只小猫")
	if err != nil {
		t.Fatalf("构建生图请求失败: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("生图请求不是合法 JSON: %v", err)
	}
	if payload["model"] != "agnes-image" || payload["prompt"] != "一只小猫" {
		t.Fatalf("生图请求基础字段不正确: %#v", payload)
	}
	extraBody, ok := payload["extra_body"].(map[string]any)
	if !ok || extraBody["response_format"] != "b64_json" {
		t.Fatalf("生图请求应默认使用 extra_body.response_format=b64_json: %#v", payload)
	}
}

func TestParseTelegramAgentImageGenerationResponseSupportsURLAndBase64(t *testing.T) {
	raw := []byte(`{"data":[{"url":"https://example.com/cat.png"},{"b64_json":"aGVsbG8=","mime_type":"image/png"}]}`)
	images, err := parseTelegramAgentImageGenerationResponse(raw, "小猫")
	if err != nil {
		t.Fatalf("解析生图响应失败: %v", err)
	}
	defer cleanupTelegramAgentGeneratedImages(images)
	if len(images) != 2 {
		t.Fatalf("期望解析 2 张图片，实际 %d", len(images))
	}
	if images[0].Source != "https://example.com/cat.png" {
		t.Fatalf("URL 图片解析不正确: %+v", images[0])
	}
	if !strings.HasSuffix(images[1].Source, ".png") {
		t.Fatalf("base64 图片应写入临时 png 文件: %+v", images[1])
	}
	if _, err := os.Stat(images[1].Source); err != nil {
		t.Fatalf("base64 临时图片不存在: %v", err)
	}
}
