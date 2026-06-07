package agent

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultTelegramAgentImagePrompt         = "请识别并描述我发送的图片。"
	telegramAgentInputImageMaxCount         = 4
	telegramAgentInputImageMaxBytes         = 10 << 20
	telegramAgentVisionDataLogPlaceholder   = "data:image/placeholder;base64,omitted"
	telegramAgentVisionInlineLogPlaceholder = "image_base64_omitted"
)

var telegramAgentVisionDataURLPattern = regexp.MustCompile(`data:image/[^;"\s]+;base64,[A-Za-z0-9+/=]+`)

func normalizeTelegramAgentInputAttachments(attachments []TelegramInputAttachment) []TelegramInputAttachment {
	normalized := make([]TelegramInputAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		data := attachment.Data
		if len(data) == 0 || len(data) > telegramAgentInputImageMaxBytes {
			continue
		}
		mimeType := normalizeTelegramAgentImageMIMEType(attachment.MIMEType, attachment.FileName, data)
		if !strings.HasPrefix(mimeType, "image/") {
			continue
		}
		fileName := strings.TrimSpace(filepath.Base(attachment.FileName))
		if fileName == "" || fileName == "." {
			fileName = "image"
		}
		normalized = append(normalized, TelegramInputAttachment{
			FileName: fileName,
			MIMEType: mimeType,
			Data:     append([]byte(nil), data...),
		})
		if len(normalized) >= telegramAgentInputImageMaxCount {
			break
		}
	}
	return normalized
}

func normalizeTelegramAgentImageMIMEType(raw string, fileName string, data []byte) string {
	mimeType := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(mimeType, "image/") {
		return mimeType
	}
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	}
	detected := strings.ToLower(http.DetectContentType(data))
	if strings.HasPrefix(detected, "image/") {
		return detected
	}
	return ""
}

func buildTelegramAgentHistoryPrompt(prompt string, attachments []TelegramInputAttachment) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" && len(attachments) > 0 {
		prompt = defaultTelegramAgentImagePrompt
	}
	attachments = normalizeTelegramAgentInputAttachments(attachments)
	if len(attachments) == 0 {
		return prompt
	}
	names := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		names = append(names, attachment.FileName)
	}
	note := "用户发送了图片：" + strings.Join(names, "、")
	if prompt == "" {
		return note
	}
	return prompt + "\n\n[" + note + "]"
}

func toOpenAIChatContent(text string, attachments []TelegramInputAttachment) any {
	attachments = normalizeTelegramAgentInputAttachments(attachments)
	text = strings.TrimSpace(text)
	if len(attachments) == 0 {
		return text
	}
	if text == "" {
		text = defaultTelegramAgentImagePrompt
	}
	parts := make([]map[string]any, 0, len(attachments)+1)
	parts = append(parts, map[string]any{"type": "text", "text": text})
	for _, attachment := range attachments {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": telegramAgentImageDataURL(attachment),
			},
		})
	}
	return parts
}

func toOpenAIResponsesContent(text string, attachments []TelegramInputAttachment) any {
	attachments = normalizeTelegramAgentInputAttachments(attachments)
	text = strings.TrimSpace(text)
	if len(attachments) == 0 {
		return text
	}
	if text == "" {
		text = defaultTelegramAgentImagePrompt
	}
	parts := make([]map[string]any, 0, len(attachments)+1)
	parts = append(parts, map[string]any{"type": "input_text", "text": text})
	for _, attachment := range attachments {
		parts = append(parts, map[string]any{
			"type":      "input_image",
			"image_url": telegramAgentImageDataURL(attachment),
		})
	}
	return parts
}

func toAnthropicMessageContent(text string, attachments []TelegramInputAttachment) any {
	attachments = normalizeTelegramAgentInputAttachments(attachments)
	text = strings.TrimSpace(text)
	if len(attachments) == 0 {
		return text
	}
	if text == "" {
		text = defaultTelegramAgentImagePrompt
	}
	parts := make([]map[string]any, 0, len(attachments)+1)
	parts = append(parts, map[string]any{"type": "text", "text": text})
	for _, attachment := range attachments {
		parts = append(parts, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": attachment.MIMEType,
				"data":       base64.StdEncoding.EncodeToString(attachment.Data),
			},
		})
	}
	return parts
}

func toGeminiParts(text string, attachments []TelegramInputAttachment) []map[string]any {
	attachments = normalizeTelegramAgentInputAttachments(attachments)
	text = strings.TrimSpace(text)
	if text == "" && len(attachments) > 0 {
		text = defaultTelegramAgentImagePrompt
	}
	parts := make([]map[string]any, 0, len(attachments)+1)
	if text != "" {
		parts = append(parts, map[string]any{"text": text})
	}
	for _, attachment := range attachments {
		parts = append(parts, map[string]any{
			"inline_data": map[string]any{
				"mime_type": attachment.MIMEType,
				"data":      base64.StdEncoding.EncodeToString(attachment.Data),
			},
		})
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"text": defaultTelegramAgentImagePrompt})
	}
	return parts
}

func telegramAgentImageDataURL(attachment TelegramInputAttachment) string {
	mimeType := normalizeTelegramAgentImageMIMEType(attachment.MIMEType, attachment.FileName, attachment.Data)
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(attachment.Data)
}

func maskTelegramAgentVisionDataForLog(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	masked := telegramAgentVisionDataURLPattern.ReplaceAllString(raw, telegramAgentVisionDataLogPlaceholder)
	var payload any
	if err := json.Unmarshal([]byte(masked), &payload); err != nil {
		return masked
	}
	maskTelegramAgentVisionJSONValue(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return masked
	}
	return string(encoded)
}

func maskTelegramAgentVisionJSONValue(value any) {
	switch current := value.(type) {
	case map[string]any:
		if data, ok := current["data"].(string); ok && data != "" && telegramAgentJSONMapLooksLikeImageData(current) {
			current["data"] = telegramAgentVisionInlineLogPlaceholder
		}
		for _, child := range current {
			maskTelegramAgentVisionJSONValue(child)
		}
	case []any:
		for _, child := range current {
			maskTelegramAgentVisionJSONValue(child)
		}
	}
}

func telegramAgentJSONMapLooksLikeImageData(value map[string]any) bool {
	for _, key := range []string{"mime_type", "media_type"} {
		if raw, ok := value[key].(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "image/") {
			return true
		}
	}
	if typ, ok := value["type"].(string); ok && strings.ToLower(strings.TrimSpace(typ)) == "base64" {
		return true
	}
	return false
}
