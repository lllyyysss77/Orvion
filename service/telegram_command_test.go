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
	assertTelegramStatusRightColumnsAligned(t, message, []string{"内存", "成功率", "失败"})
	assertTelegramStatusColumnOffset(t, message, "内存", "启用提供方", 1)

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

func TestWidenTelegramMessageSkipsThinkingPlaceholder(t *testing.T) {
	if got := widenTelegramMessageForTelegram("正在思考..."); got != "正在思考..." {
		t.Fatalf("思考占位消息不应追加宽度填充，实际为 %q", got)
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

func TestNormalizeTelegramPhotoCaptionNeverAddsWidthPadding(t *testing.T) {
	caption := "  图片说明  "
	if got := normalizeTelegramPhotoCaption(caption); got != "图片说明" {
		t.Fatalf("图片 caption 只能清理首尾空白，不能追加宽度填充，实际为 %q", got)
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

func assertTelegramStatusColumnOffset(t *testing.T, message string, baseLabel string, offsetLabel string, wantOffset int) {
	t.Helper()
	baseLine := findTelegramStatusLineContaining(message, "- "+baseLabel+"：")
	offsetLine := findTelegramStatusLineContaining(message, "- "+offsetLabel+"：")
	baseIndex := strings.Index(baseLine, "- "+baseLabel+"：")
	offsetIndex := strings.Index(offsetLine, "- "+offsetLabel+"：")
	if baseIndex < 0 || offsetIndex < 0 {
		t.Fatalf("未找到状态列，base=%q offset=%q", baseLabel, offsetLabel)
	}
	gotOffset := telegramTextDisplayWidth(offsetLine[:offsetIndex]) - telegramTextDisplayWidth(baseLine[:baseIndex])
	if gotOffset != wantOffset {
		t.Fatalf("状态列 %q 应比 %q 右移 %d 格，实际右移 %d 格", offsetLabel, baseLabel, wantOffset, gotOffset)
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
