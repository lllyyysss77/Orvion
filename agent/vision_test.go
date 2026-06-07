package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
)

func TestTelegramAgentOpenAIMessageIncludesImageURL(t *testing.T) {
	attachments := []TelegramInputAttachment{{
		FileName: "cat.png",
		MIMEType: "image/png",
		Data:     []byte("png-bytes"),
	}}

	messages := toTelegramAgentOpenAIMessages("system", nil, "这是什么图？", attachments)
	if len(messages) != 2 {
		t.Fatalf("消息数量不符合预期: %+v", messages)
	}
	parts, ok := messages[1].Content.([]map[string]any)
	if !ok {
		t.Fatalf("用户消息应为多模态内容数组，实际为 %T: %+v", messages[1].Content, messages[1].Content)
	}
	if len(parts) != 2 || parts[0]["type"] != "text" || parts[1]["type"] != "image_url" {
		t.Fatalf("OpenAI 多模态内容不正确: %+v", parts)
	}
	imageURL := parts[1]["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(imageURL, "data:image/png;base64,") {
		t.Fatalf("图片 data URL 不正确: %s", imageURL)
	}
}

func TestTelegramAgentGeminiRequestIncludesInlineImage(t *testing.T) {
	body, _, err := buildTelegramAgentRequestBody(
		context.Background(),
		models.TelegramAgentConfig{},
		selectedModelProvider{ProviderStyle: consts.StyleGemini},
		nil,
		"描述图片",
		[]TelegramInputAttachment{{
			FileName: "cat.jpg",
			MIMEType: "image/jpeg",
			Data:     []byte("jpg-bytes"),
		}},
	)
	if err != nil {
		t.Fatalf("构造 Gemini 请求失败: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	rawContents := payload["contents"].([]any)
	rawParts := rawContents[0].(map[string]any)["parts"].([]any)
	if len(rawParts) != 2 {
		t.Fatalf("Gemini parts 数量不正确: %+v", rawParts)
	}
	inlineData, ok := rawParts[1].(map[string]any)["inline_data"].(map[string]any)
	if !ok || inlineData["mime_type"] != "image/jpeg" || strings.TrimSpace(inlineData["data"].(string)) == "" {
		t.Fatalf("Gemini 图片块不正确: %+v", rawParts[1])
	}
}

func TestMaskTelegramAgentVisionDataForLog(t *testing.T) {
	raw := `{"url":"data:image/png;base64,QUJDRA==","inline_data":{"mime_type":"image/jpeg","data":"SU1H"}}`
	masked := maskTelegramAgentVisionDataForLog(raw)
	if strings.Contains(masked, "QUJDRA") || strings.Contains(masked, "SU1H") {
		t.Fatalf("日志中不应保留图片 base64: %s", masked)
	}
	if !strings.Contains(masked, telegramAgentVisionDataLogPlaceholder) {
		t.Fatalf("日志中应保留占位符: %s", masked)
	}
	if !strings.Contains(masked, telegramAgentVisionInlineLogPlaceholder) {
		t.Fatalf("日志中应脱敏 inline data: %s", masked)
	}
}
