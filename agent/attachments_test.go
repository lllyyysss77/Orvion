package agent

import (
	"context"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

type telegramAttachmentTestClient struct {
	sent      []string
	photos    []string
	documents []string
}

func (c *telegramAttachmentTestClient) SendMessage(_ context.Context, _ int64, text string) (int64, error) {
	c.sent = append(c.sent, text)
	return int64(len(c.sent)), nil
}

func (c *telegramAttachmentTestClient) EditMessage(context.Context, int64, int64, string) error {
	return nil
}

func (c *telegramAttachmentTestClient) SendTyping(context.Context, int64) error {
	return nil
}

func (c *telegramAttachmentTestClient) SendPhoto(_ context.Context, _ int64, source string, caption string) error {
	c.photos = append(c.photos, source+"|"+caption)
	return nil
}

func (c *telegramAttachmentTestClient) SendDocument(_ context.Context, _ int64, source string, caption string) error {
	c.documents = append(c.documents, source+"|"+caption)
	return nil
}

func TestExtractTelegramAgentAttachments(t *testing.T) {
	text, attachments := extractTelegramAgentAttachments("结果如下\n[orvion:image:/tmp/a.png|图片]\n[orvion:file:file:///tmp/a.zip|压缩包]")
	if text != "结果如下" {
		t.Fatalf("文本清理不符合预期: %q", text)
	}
	if len(attachments) != 2 {
		t.Fatalf("附件数量不符合预期: %d", len(attachments))
	}
	if attachments[0].Kind != telegramAgentAttachmentKindImage || attachments[0].Source != "/tmp/a.png" || attachments[0].Caption != "图片" {
		t.Fatalf("图片附件解析不符合预期: %#v", attachments[0])
	}
	if attachments[1].Kind != telegramAgentAttachmentKindFile || attachments[1].Source != "/tmp/a.zip" || attachments[1].Caption != "压缩包" {
		t.Fatalf("文件附件解析不符合预期: %#v", attachments[1])
	}
}

func TestSendTelegramAgentTextWithAttachments(t *testing.T) {
	client := &telegramAttachmentTestClient{}
	err := sendTelegramAgentTextWithAttachments(context.Background(), client, 123, "已生成\n[orvion:image:https://example.com/a.png|图]\n[orvion:file:/tmp/a.txt|文件]")
	if err != nil {
		t.Fatalf("发送附件失败: %v", err)
	}
	if len(client.sent) != 1 || client.sent[0] != "已生成" {
		t.Fatalf("文本发送不符合预期: %#v", client.sent)
	}
	if len(client.photos) != 1 || client.photos[0] != "https://example.com/a.png|图" {
		t.Fatalf("图片发送不符合预期: %#v", client.photos)
	}
	if len(client.documents) != 1 || client.documents[0] != "/tmp/a.txt|文件" {
		t.Fatalf("文件发送不符合预期: %#v", client.documents)
	}
}

func TestLimitTelegramAgentAttachmentCaptionKeepsValidUTF8(t *testing.T) {
	caption := strings.Repeat("图", telegramAgentAttachmentCaptionMax+10)
	got := limitTelegramAgentAttachmentCaption(caption)
	if !utf8.ValidString(got) {
		t.Fatalf("caption 截断后必须保持合法 UTF-8")
	}
	if len([]rune(got)) != telegramAgentAttachmentCaptionMax {
		t.Fatalf("中文 caption 应按字符安全截断，实际字符数=%d", len([]rune(got)))
	}

	emoji := strings.Repeat("😀", telegramAgentAttachmentCaptionMax)
	got = limitTelegramAgentAttachmentCaption(emoji)
	if units := len(utf16.Encode([]rune(got))); units > telegramAgentAttachmentCaptionMax {
		t.Fatalf("emoji caption 不应超过 Telegram UTF-16 长度限制，实际=%d", units)
	}
}
