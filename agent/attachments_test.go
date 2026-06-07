package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/racio/orvion/models"
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

func TestCreateTelegramAgentAttachmentFileForSVG(t *testing.T) {
	result, err := createTelegramAgentAttachmentFile(telegramAgentToolCallArgs{
		FileName:       "../cute-cat.svg",
		Content:        `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 120"><text x="20" y="60">cat</text></svg>`,
		AttachmentKind: telegramAgentAttachmentKindImage,
		Caption:        "小猫 SVG",
	})
	if err != nil {
		t.Fatalf("创建 SVG 附件失败: %v", err)
	}
	if !strings.Contains(result, "文件名：cute-cat.svg") || !strings.Contains(result, "[orvion:file:") {
		t.Fatalf("SVG 应作为文件附件返回，实际为: %s", result)
	}

	_, attachments := extractTelegramAgentAttachments(result)
	if len(attachments) != 1 {
		t.Fatalf("应解析出 1 个附件，实际为: %#v", attachments)
	}
	if attachments[0].Kind != telegramAgentAttachmentKindFile || attachments[0].Caption != "小猫 SVG" {
		t.Fatalf("附件信息不符合预期: %#v", attachments[0])
	}
	if _, err := os.Stat(attachments[0].Source); err != nil {
		t.Fatalf("附件文件应已落盘: %v", err)
	}
}

func TestCreateAttachmentFileToolStopsLoopWithAttachmentMarker(t *testing.T) {
	toolCalls := []telegramAgentOpenAIToolCall{
		{
			ID:   "call_create_svg",
			Type: "function",
			Function: telegramAgentOpenAIFunctionCall{
				Name:      telegramAgentToolCreateAttachmentFile,
				Arguments: `{"file_name":"cat.svg","content":"<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 10 10\"></svg>","caption":"小猫 SVG"}`,
			},
		},
	}
	messages, directFinalText := appendTelegramAgentToolResults(context.Background(), 123, models.TelegramAgentConfig{}, toolCalls, nil)
	if len(messages) != 1 {
		t.Fatalf("期望写入 1 条 tool message，实际为 %d", len(messages))
	}
	if !strings.Contains(directFinalText, "[orvion:file:") {
		t.Fatalf("创建附件文件后应直接返回附件标记，实际为: %s", directFinalText)
	}
}
