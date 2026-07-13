package agent

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"
)

const (
	telegramAgentAttachmentKindImage  = "image"
	telegramAgentAttachmentKindFile   = "file"
	telegramAgentAttachmentMaxCount   = 8
	telegramAgentAttachmentCaptionMax = 900
)

var telegramAgentAttachmentMarkerPattern = regexp.MustCompile(`\[orvion:(image|file):([^\]\r\n]+)\]`)

type telegramAgentAttachment struct {
	Kind    string
	Source  string
	Caption string
}

func telegramAgentTextContainsAttachmentMarker(text string) bool {
	return telegramAgentAttachmentMarkerPattern.MatchString(text)
}

func extractTelegramAgentAttachments(text string) (string, []telegramAgentAttachment) {
	attachments := make([]telegramAgentAttachment, 0)
	cleaned := telegramAgentAttachmentMarkerPattern.ReplaceAllStringFunc(text, func(marker string) string {
		matches := telegramAgentAttachmentMarkerPattern.FindStringSubmatch(marker)
		if len(matches) != 3 {
			return marker
		}
		if len(attachments) >= telegramAgentAttachmentMaxCount {
			return ""
		}
		source, caption := parseTelegramAgentAttachmentPayload(matches[2])
		if source == "" {
			return ""
		}
		attachments = append(attachments, telegramAgentAttachment{
			Kind:    matches[1],
			Source:  source,
			Caption: caption,
		})
		return ""
	})
	return strings.TrimSpace(cleaned), attachments
}

func parseTelegramAgentAttachmentPayload(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	source := raw
	caption := ""
	if left, right, ok := strings.Cut(raw, "|"); ok {
		source = strings.TrimSpace(left)
		caption = strings.TrimSpace(right)
	}
	source = strings.Trim(source, "\"'")
	caption = strings.Trim(caption, "\"'")
	if strings.HasPrefix(strings.ToLower(source), "file://") {
		if parsed, err := url.Parse(source); err == nil {
			source = parsed.Path
		}
	}
	return strings.TrimSpace(source), limitTelegramAgentAttachmentCaption(caption)
}

func sendTelegramAgentTextWithAttachments(ctx context.Context, client TelegramClient, chatID int64, text string) error {
	cleaned, attachments := extractTelegramAgentAttachments(text)
	if strings.TrimSpace(cleaned) == "" && len(attachments) > 0 {
		cleaned = "已生成附件。"
	}
	for _, part := range splitTelegramMessage(strings.TrimSpace(cleaned)) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, err := client.SendMessage(ctx, chatID, part); err != nil {
			return err
		}
	}
	return sendTelegramAgentAttachments(ctx, client, chatID, attachments)
}

func sendTelegramAgentAttachments(ctx context.Context, client TelegramClient, chatID int64, attachments []telegramAgentAttachment) error {
	if len(attachments) == 0 {
		return nil
	}
	attachmentClient, ok := client.(TelegramAttachmentClient)
	if !ok {
		_, err := client.SendMessage(ctx, chatID, "当前 Telegram 客户端不支持附件发送。")
		return err
	}
	for _, attachment := range attachments {
		source := strings.TrimSpace(attachment.Source)
		if source == "" {
			continue
		}
		switch attachment.Kind {
		case telegramAgentAttachmentKindImage:
			if err := attachmentClient.SendPhoto(ctx, chatID, source, attachment.Caption); err != nil {
				return fmt.Errorf("发送图片附件失败: %w", err)
			}
		case telegramAgentAttachmentKindFile:
			if err := attachmentClient.SendDocument(ctx, chatID, source, attachment.Caption); err != nil {
				return fmt.Errorf("发送文件附件失败: %w", err)
			}
		}
	}
	return nil
}

func limitTelegramAgentAttachmentCaption(caption string) string {
	caption = strings.TrimSpace(caption)
	runes := []rune(caption)
	units := 0
	for index, r := range runes {
		width := utf16.RuneLen(r)
		if width < 1 {
			width = 1
		}
		if units+width > telegramAgentAttachmentCaptionMax {
			return string(runes[:index])
		}
		units += width
	}
	return caption
}
