package service

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestRenderTelegramAgentMarkdownV2ConvertsMarkdownTablesToBox(t *testing.T) {
	input := strings.Join([]string{
		"## 🌤️ 佛山天气每日推送",
		"",
		"---",
		"",
		"| 项目 | 详情 |",
		"|------|------|",
		"| 天气现象 | 多云转雷阵雨 |",
		"| 气温范围 | 26℃ ~ 33℃ |",
		"",
		"### 💡 出行建议",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	for _, expected := range []string{
		`*🌤️ 佛山天气每日推送*`,
		`━━━━━━━━━━━━`,
		"```text\n┌",
		`│ 项目 / 详情`,
		`│ 天气现象 : 多云转雷阵雨`,
		`│ 气温范围 : 26℃ ~ 33℃`,
		`└`,
		`*💡 出行建议*`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, got)
		}
	}
	if strings.Contains(got, `\| 项目`) || strings.Contains(got, `• 天气现象`) || strings.Contains(got, `\#\#`) {
		t.Fatalf("表格应转为盒子文本，且标题不应保留为原始 Markdown，实际为: %s", got)
	}
	assertTelegramMarkdownBoxUsesHalfWidthPadding(t, got)
}

func TestRenderTelegramAgentMarkdownV2ConvertsSingleTitleTableToBox(t *testing.T) {
	input := strings.Join([]string{
		"| 🏠 VPS 信息 |",
		"| --- | --- |",
		"| CPU | 15% |",
		"| RAM | 128MB |",
		"| DISK | 512G |",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	for _, expected := range []string{
		"```text\n┌",
		`│ 🏠 VPS 信息`,
		`├`,
		`│ CPU  : 15%`,
		`│ RAM  : 128MB`,
		`│ DISK : 512G`,
		`└`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, got)
		}
	}
	assertTelegramMarkdownBoxHasNoRightBorder(t, got)
	assertTelegramMarkdownBoxUsesHalfWidthPadding(t, got)
}

func assertTelegramMarkdownBoxHasNoRightBorder(t *testing.T, content string) {
	t.Helper()
	start := strings.Index(content, "```text\n")
	if start < 0 {
		t.Fatalf("未找到盒子代码块，实际为: %s", content)
	}
	bodyStart := start + len("```text\n")
	end := strings.Index(content[bodyStart:], "\n```")
	if end < 0 {
		t.Fatalf("盒子代码块缺少结束标记，实际为: %s", content)
	}
	lines := strings.Split(content[bodyStart:bodyStart+end], "\n")
	if len(lines) == 0 {
		t.Fatalf("盒子代码块为空，实际为: %s", content)
	}
	for _, line := range lines {
		if strings.HasSuffix(line, "│") || strings.HasSuffix(line, "┐") || strings.HasSuffix(line, "┤") || strings.HasSuffix(line, "┘") {
			t.Fatalf("盒子代码块不应包含右边框，行内容: %q，完整内容: %s", line, content)
		}
	}
}

func assertTelegramMarkdownBoxUsesHalfWidthPadding(t *testing.T, content string) {
	t.Helper()
	if strings.Contains(content, "　") {
		t.Fatalf("盒子代码块应使用半角空格补齐，避免 Telegram 右边框错位，实际为: %s", content)
	}
}

func TestBuildModelProviderAutoDisableAlertContentAlignsValues(t *testing.T) {
	now := time.Date(2026, 6, 26, 16, 46, 6, 0, time.Local)
	resumeAt := time.Date(2026, 6, 26, 16, 51, 6, 0, time.Local)
	content := buildModelProviderAutoDisableAlertContent(modelProviderAutoDisableAlertDetail{
		ModelName:     "gpt-5.4",
		ProviderName:  "anyrouter",
		ProviderModel: "gpt-5.5",
	}, ModelProviderAutoDisableAlertEvent{
		Threshold: 10,
		Window:    time.Minute,
		ResumeAt:  resumeAt,
	}, now)

	for _, expected := range []string{
		"【Orvion 模型提供商熔断】",
		"时间　　：2026-06-26 16:46:06",
		"模型　　：gpt-5.4",
		"提供商　：anyrouter / gpt-5.5",
		"触发原因：检测窗口内错误达到 10 次",
		"检测窗口：1m0s",
		"恢复时间：2026-06-26 16:51:06",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, content)
		}
	}
	if strings.Contains(content, "```") || strings.Contains(content, `\`) {
		t.Fatalf("模型提供商熔断告警应为纯文本，实际为: %s", content)
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
