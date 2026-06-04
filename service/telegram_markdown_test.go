package service

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderTelegramAgentMarkdownV2(t *testing.T) {
	input := strings.Join([]string{
		"目前所有 Claude 模型均处于**禁用**状态：",
		"",
		"- claude-haiku-4-5-20251001 (ID 4)",
		"- claude-opus-4-6 (ID 3)",
		"最近失败模型：`gpt-5.5`",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)

	if !strings.Contains(got, "*禁用*") {
		t.Fatalf("期望 MarkdownV2 粗体被保留，实际为: %s", got)
	}
	for _, expected := range []string{
		`目前所有 Claude 模型均处于`,
		`\- claude\-haiku\-4\-5\-20251001 \(ID 4\)`,
		`\- claude\-opus\-4\-6 \(ID 3\)`,
		"`gpt-5.5`",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, got)
		}
	}
}

func TestRenderTelegramAgentMarkdownV2SupportsCommonEntities(t *testing.T) {
	input := strings.Join([]string{
		"粗体：**文字**",
		"斜体：*文字* 或 _文字_",
		"删除线：~~文字~~",
		"下划线：__文字__",
		"代码：" + "`gpt-5.5`",
		"链接：[百度](https://www.baidu.com)",
		"剧透：||文字||",
		"字段：model_with_provider_id",
		"代码块：",
		"```go",
		`fmt.Println("hi")`,
		"```",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	for _, expected := range []string{
		`粗体：*文字*`,
		`斜体：_文字_ 或 _文字_`,
		`删除线：~文字~`,
		`下划线：__文字__`,
		"`gpt-5.5`",
		`链接：[百度](https://www.baidu.com)`,
		`剧透：||文字||`,
		`字段：model\_with\_provider\_id`,
		"```go\nfmt.Println(\"hi\")\n```",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, got)
		}
	}
}

func TestIsTelegramMessageNotModifiedError(t *testing.T) {
	err := errors.New("telegram status=400 description=Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message")
	if !isTelegramMessageNotModifiedError(err) {
		t.Fatalf("期望识别 TG message is not modified 错误")
	}
	if isTelegramMessageNotModifiedError(errors.New("telegram status=400 description=Bad Request: message text is empty")) {
		t.Fatalf("不应把其它 TG 400 错误识别为未修改")
	}
}
