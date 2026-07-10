package service

import (
	"context"
	"strings"
	"testing"

	"github.com/racio/orvion/consts"
)

func TestBuildTelegramCommandReplyDoesNotTreatModelStatusAsSystemStatus(t *testing.T) {
	reply, shouldReply := buildTelegramCommandReply(context.Background(), telegramMessage{
		Text: "禁用claude的模型和开启deepseek的模型，然后检查gpt状态",
		Chat: telegramChat{ID: 123},
	}, "123")
	if shouldReply || strings.TrimSpace(reply) != "" {
		t.Fatalf("模型状态查询不应触发系统状态回复，shouldReply=%v reply=%q", shouldReply, reply)
	}
}

func TestBuildTelegramCommandReplyTreatsExplicitSystemStatusAsSystemStatus(t *testing.T) {
	if !isTelegramSystemStatusText("系统状态") {
		t.Fatalf("明确系统状态查询应命中系统状态关键词")
	}
}

func TestBuildTelegramSystemStatusMessageUsesMarkdownTemplate(t *testing.T) {
	message := buildTelegramSystemStatusMessage(context.Background())
	for _, expected := range []string{
		"## 🤖 Orvion 系统状态",
		"**🕒 时间**：",
		"**🏷️ 版本**：`",
		"### 💻 资源使用",
		"`- CPU：",
		"### 📈 今日统计",
		"### 💰 消耗",
		"### ⏳ 时间",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("系统状态 Markdown 缺少 %q，实际为: %s", expected, message)
		}
	}
	assertTelegramStatusRightColumnsAligned(t, message, []string{"内存", "启用提供方", "成功率", "失败"})

	rendered := renderTelegramAgentMarkdownV2(message)
	for _, expected := range []string{
		"*🤖 Orvion 系统状态*",
		"*🕒 时间*：",
		"`" + consts.Version + "`",
		"*💻 资源使用*",
		"`- CPU：",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("系统状态 MarkdownV2 渲染缺少 %q，实际为: %s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "\\#\\#") {
		t.Fatalf("系统状态 MarkdownV2 不应保留原始标题符号，实际为: %s", rendered)
	}
}

func TestBuildTelegramHelpMessageIncludesImageCommand(t *testing.T) {
	message := buildTelegramHelpMessage()
	if !strings.Contains(message, "/img <提示词>") || !strings.Contains(message, "使用生图模型生成图片") {
		t.Fatalf("帮助信息应包含 /img 生图命令，实际为: %s", message)
	}
}

func TestWidenTelegramMessagePadsShortText(t *testing.T) {
	message := widenTelegramMessageForTelegram("正在思考...")
	lines := strings.Split(message, "\n")
	if len(lines) != 2 {
		t.Fatalf("短消息应追加一行宽度填充，实际为 %q", message)
	}
	if lines[0] != "正在思考..." {
		t.Fatalf("短消息正文不应改变，实际为 %q", lines[0])
	}
	if telegramTextDisplayWidth(lines[1]) < telegramWideMessageWidth {
		t.Fatalf("填充行宽度不足，宽度=%d 内容=%q", telegramTextDisplayWidth(lines[1]), lines[1])
	}
}

func TestWidenTelegramMessageIsIdempotent(t *testing.T) {
	once := widenTelegramMessageForTelegram("完成。")
	twice := widenTelegramMessageForTelegram(once)
	if once != twice {
		t.Fatalf("重复补宽不应追加第二次，once=%q twice=%q", once, twice)
	}
}

func TestWidenTelegramMessageSkipsWideText(t *testing.T) {
	wide := strings.Repeat("A", telegramWideMessageWidth)
	if got := widenTelegramMessageForTelegram(wide); got != wide {
		t.Fatalf("已有足够宽度的消息不应追加填充，实际为 %q", got)
	}
}

func TestWidenTelegramCaptionSkipsLongCaption(t *testing.T) {
	longCaption := strings.Repeat("短", telegramWideCaptionMaxRunes+1)
	if got := widenTelegramCaptionForTelegram(longCaption); got != longCaption {
		t.Fatalf("过长 caption 不应追加填充，实际长度=%d", len([]rune(got)))
	}
}

func assertTelegramStatusRightColumnsAligned(t *testing.T, message string, labels []string) {
	t.Helper()
	expectedWidth := -1
	for _, label := range labels {
		marker := "- " + label + "："
		line := findTelegramStatusLineContaining(message, marker)
		if line == "" {
			t.Fatalf("系统状态缺少右侧字段 %q，实际为: %s", label, message)
		}
		index := strings.Index(line, marker)
		width := telegramTextDisplayWidth(line[:index])
		if expectedWidth < 0 {
			expectedWidth = width
			continue
		}
		if width != expectedWidth {
			t.Fatalf("系统状态右侧字段未对齐，字段=%s 期望宽度=%d 实际宽度=%d 行=%q", label, expectedWidth, width, line)
		}
	}
}

func findTelegramStatusLineContaining(message string, marker string) string {
	for _, line := range strings.Split(message, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

func TestSelectTelegramLargestPhoto(t *testing.T) {
	selected, ok := selectTelegramLargestPhoto([]telegramPhotoSize{
		{FileID: "small", Width: 100, Height: 100, FileSize: 1000},
		{FileID: "large", Width: 300, Height: 300, FileSize: 3000},
	})
	if !ok || selected.FileID != "large" {
		t.Fatalf("应选择最大图片，实际为 ok=%v selected=%+v", ok, selected)
	}
}

func TestTelegramImageDocumentDetection(t *testing.T) {
	if !isTelegramImageDocument(telegramDocumentFile{FileName: "cat.png"}) {
		t.Fatalf("应按扩展名识别图片 document")
	}
	if isTelegramImageDocument(telegramDocumentFile{FileName: "archive.zip", MIMEType: "application/zip"}) {
		t.Fatalf("非图片 document 不应进入视觉输入")
	}
}

func TestTelegramFileURLEscapesPath(t *testing.T) {
	notifier := &telegramNotifier{
		apiBase:  "https://api.telegram.org",
		botToken: "token",
	}
	fileURL := notifier.telegramFileURL("photos/cat image.png")
	if fileURL != "https://api.telegram.org/file/bottoken/photos/cat%20image.png" {
		t.Fatalf("Telegram 文件 URL 转义不正确: %s", fileURL)
	}
}
