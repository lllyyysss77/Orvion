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

func TestRenderTelegramAgentMarkdownV2TreatsCodeWrappedBoldAsBold(t *testing.T) {
	got := renderTelegramAgentMarkdownV2("标题：`** 总结 **`，模型：`gpt-5.5`")
	for _, expected := range []string{
		"标题：*总结*",
		"模型：`gpt-5.5`",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, got)
		}
	}
	if strings.Contains(got, "`**") || strings.Contains(got, "**`") {
		t.Fatalf("反引号包裹的加粗语法不应继续渲染为行内代码，实际为: %s", got)
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

func TestRenderTelegramAgentMarkdownV2DoesNotNormalizeInsideCodeBlock(t *testing.T) {
	input := strings.Join([]string{
		"```text",
		"| 项目 | 详情 |",
		"|------|------|",
		"| 错误码 | 400 |",
		"```",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	if strings.Count(got, "```") != 2 {
		t.Fatalf("代码块内部的表格不应生成嵌套代码块，实际为: %s", got)
	}
	if strings.Contains(got, "┌") || strings.Contains(got, "├") || strings.Contains(got, "└") {
		t.Fatalf("已有代码块内部不应再次转换表格，实际为: %s", got)
	}
	if !strings.Contains(got, "```text\n| 项目 | 详情 |") {
		t.Fatalf("应保留原始代码块内容，实际为: %s", got)
	}
}

func TestRenderTelegramAgentMarkdownV2RepairsMalformedTextCodeBlock(t *testing.T) {
	input := strings.Join([]string{
		"`text",
		"┌────────────────",
		"│ 项目 / 内容",
		"├────────────────",
		"│ 错误 : 401 Unauthorized",
		"└────────────────",
		"``",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	if !strings.Contains(got, "```text\n┌────────────────") {
		t.Fatalf("应将 `text/`` 修复为 text 代码块，实际为: %s", got)
	}
	if strings.HasPrefix(got, "`text\n") {
		t.Fatalf("不应保留损坏的代码块围栏，实际为: %s", got)
	}
	if strings.Count(got, "```") != 2 {
		t.Fatalf("修复后应只有一对标准代码块围栏，实际为: %s", got)
	}
}

func TestRenderTelegramAgentMarkdownV2WrapsBareBoxDrawingBlock(t *testing.T) {
	input := strings.Join([]string{
		"🔍 豆包下架智能体 — 最新搜索结果",
		"",
		"📰 事件时间线",
		"┌────────────────────────────────",
		"│ 时间 / 事件",
		"├────────────────────────────────",
		"│ 2026年7月4日 : 宣布下线智能体功能",
		"└────────────────────────────────",
		"",
		"🥇 核心原因一：监管合规压力",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	for _, expected := range []string{
		`🔍 豆包下架智能体 — 最新搜索结果`,
		"```text\n┌────────────────────────────────",
		"│ 时间 / 事件",
		"└────────────────────────────────\n```",
		`🥇 核心原因一：监管合规压力`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, got)
		}
	}
	if strings.Count(got, "```text") != 1 || strings.Count(got, "```") != 2 {
		t.Fatalf("裸盒子块应只包裹为一个 text 代码块，实际为: %s", got)
	}
}

func TestRenderTelegramAgentMarkdownV2KeepsShortBareBoxBorderLength(t *testing.T) {
	input := strings.Join([]string{
		"┌────────",
		"│ 短表格",
		"└────────",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	if !strings.Contains(got, "┌────────\n") || !strings.Contains(got, "└────────\n```") {
		t.Fatalf("短裸盒子边框不应被扩展，实际为: %s", got)
	}
}

func TestRenderTelegramAgentMarkdownV2LimitsBoxBorderLength(t *testing.T) {
	input := strings.Join([]string{
		"| 问题 | 非常长非常长非常长非常长非常长非常长非常长非常长的答案 |",
		"| --- | --- |",
		"| 豆包为什么下架智能体？ | 合规压力、算力成本和战略聚焦共同影响 |",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	assertTelegramMarkdownBoxBorderMaxLength(t, got)
	if !strings.Contains(got, "┌─────────────────────────────────") {
		t.Fatalf("盒子边框应限制为固定最大长度，实际为: %s", got)
	}
}

func TestRenderTelegramAgentMarkdownV2LimitsBareBoxBorderLength(t *testing.T) {
	input := strings.Join([]string{
		"┌────────────────────────────────────────────────────────────────────────",
		"│ 时间 / 事件",
		"├────────────────────────────────────────────────────────────────────────",
		"│ 2026年7月4日 : 宣布下线智能体功能",
		"└────────────────────────────────────────────────────────────────────────",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	assertTelegramMarkdownBoxBorderMaxLength(t, got)
	for _, unexpected := range []string{
		"┌────────────────────────────────────────────────",
		"├────────────────────────────────────────────────",
		"└────────────────────────────────────────────────",
	} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("裸盒子长边框应被裁短，实际为: %s", got)
		}
	}
}

func TestRenderTelegramAgentMarkdownV2WrapsDoubleLineBareBox(t *testing.T) {
	input := strings.Join([]string{
		"╔════════════════════════════════════════════════",
		"║ 双线表格",
		"╠════════════════════════════════════════════════",
		"║ 内容",
		"╚════════════════════════════════════════════════",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	if !strings.Contains(got, "```text\n╔─────────────────────────────────") {
		t.Fatalf("双线裸盒子应被包裹并限制边框长度，实际为: %s", got)
	}
	assertTelegramMarkdownBoxBorderMaxLength(t, got)
}

func TestRenderTelegramAgentMarkdownV2ParsesLinksWithParentheses(t *testing.T) {
	got := renderTelegramAgentMarkdownV2("链接：[Wiki](https://example.com/a_(b))")
	expected := "[Wiki](https://example.com/a_(b\\))"
	if !strings.Contains(got, expected) {
		t.Fatalf("链接 URL 中的括号应保留在链接内，期望包含 %q，实际为: %s", expected, got)
	}
	if strings.Contains(got, `\)`) && strings.HasSuffix(got, `\)`) && !strings.HasSuffix(got, `b\))`) {
		t.Fatalf("不应把链接 URL 的结尾括号拆到链接外，实际为: %s", got)
	}
}

func TestRenderTelegramAgentMarkdownV2ConvertsBlockQuoteListToCard(t *testing.T) {
	input := strings.Join([]string{
		"> ⚠️ 禁止行为包括：",
		"> - ❌ 不得生成鼓励/美化自残自杀等损害身心健康的内容",
		"> - ❌ 不得向未成年人提供虚拟亲属、虚拟伴侣等虚拟亲密关系服务",
		"> - ❌ 不得过度迎合用户、诱导情感依赖或沉迷",
		"> - ❌ 不得语言暴力、损害人格尊严",
	}, "\n")

	got := renderTelegramAgentMarkdownV2(input)
	for _, expected := range []string{
		"⚠️ 禁止行为包括",
		"━━━━━━━━━━━━",
		"• 不得生成鼓励/美化自残自杀等损害身心健康的内容",
		"• 不得向未成年人提供虚拟亲属、虚拟伴侣等虚拟亲密关系服务",
		"• 不得过度迎合用户、诱导情感依赖或沉迷",
		"• 不得语言暴力、损害人格尊严",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("期望包含 %q，实际为: %s", expected, got)
		}
	}
	for _, unexpected := range []string{`>`, `\- ❌`, `❌ 不得`} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("引用列表应转为 TG 友好提示卡片，仍包含 %q，实际为: %s", unexpected, got)
		}
	}
}

func TestRenderTelegramAgentMarkdownV2ConvertsPlainBlockQuoteToBullet(t *testing.T) {
	got := renderTelegramAgentMarkdownV2("> 💡 记得保存重要内容")
	expected := "• 💡 记得保存重要内容"
	if !strings.Contains(got, expected) {
		t.Fatalf("普通引用应转为圆点行，期望包含 %q，实际为: %s", expected, got)
	}
	if strings.Contains(got, ">") || strings.Contains(got, "━━━━━━━━━━━━") {
		t.Fatalf("普通引用不应保留引用符或提示卡片分隔线，实际为: %s", got)
	}
}

func TestRenderTelegramAgentMarkdownV2MovesEntityPaddingOutside(t *testing.T) {
	got := renderTelegramAgentMarkdownV2("**🐾 具体情况分析： **")
	if got != "*🐾 具体情况分析：* " {
		t.Fatalf("粗体尾部空格应移到实体外，实际为: %q", got)
	}
}

func assertTelegramMarkdownBoxBorderMaxLength(t *testing.T, content string) {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if len([]rune(trimmed)) == 0 {
			continue
		}
		first := []rune(trimmed)[0]
		if first != '┌' && first != '├' && first != '└' {
			continue
		}
		borderLen := 0
		for _, r := range []rune(trimmed)[1:] {
			if r == '─' {
				borderLen++
			}
		}
		if borderLen > telegramMarkdownBoxBorderMax {
			t.Fatalf("盒子边框超过最大长度 %d，line=%q content=%s", telegramMarkdownBoxBorderMax, line, content)
		}
	}
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
