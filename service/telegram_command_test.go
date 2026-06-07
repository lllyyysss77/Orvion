package service

import (
	"context"
	"strings"
	"testing"
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
