package service

import (
	"strings"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/tidwall/gjson"
)

func estimateUsageFromIO(style string, model string, inputRaw []byte, output *models.OutputUnion) models.Usage {
	inputText := extractInputText(style, inputRaw)
	outputText := extractOutputText(style, output)

	usage := models.Usage{
		PromptTokens:     estimateTokens(inputText),
		CompletionTokens: estimateTokens(outputText),
	}
	if style == consts.StyleOpenAIEmbeddings || style == consts.StyleGeminiEmbeddings {
		usage.CompletionTokens = 0
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}

func estimatePromptTokensFromInput(style string, inputRaw []byte) int64 {
	inputText := extractInputText(style, inputRaw)
	return estimateTokens(inputText)
}

func estimateTokens(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	asciiBytes := 0
	nonASCII := 0
	for _, r := range text {
		if r <= 0x7f {
			asciiBytes++
		} else {
			nonASCII++
		}
	}
	asciiTokens := (asciiBytes + 3) / 4
	return int64(asciiTokens + nonASCII)
}

func extractInputText(style string, raw []byte) string {
	var sb strings.Builder
	switch style {
	case consts.StyleOpenAI, consts.StyleOpenAIEmbeddings:
		appendTextFromAny(&sb, gjson.GetBytes(raw, "messages"))
		appendTextFromAny(&sb, gjson.GetBytes(raw, "input"))
		appendTextFromAny(&sb, gjson.GetBytes(raw, "prompt"))
	case consts.StyleOpenAIRes:
		appendTextFromAny(&sb, gjson.GetBytes(raw, "input"))
		appendTextFromAny(&sb, gjson.GetBytes(raw, "instructions"))
	case consts.StyleAnthropic:
		appendTextFromAny(&sb, gjson.GetBytes(raw, "system"))
		appendTextFromAny(&sb, gjson.GetBytes(raw, "messages"))
	case consts.StyleGemini, consts.StyleGeminiEmbeddings:
		appendTextFromAny(&sb, gjson.GetBytes(raw, "contents"))
	}
	return sb.String()
}

func extractOutputText(style string, output *models.OutputUnion) string {
	if output == nil {
		return ""
	}
	var sb strings.Builder
	switch style {
	case consts.StyleOpenAI, consts.StyleOpenAIEmbeddings:
		appendOpenAIOutput(&sb, output)
	case consts.StyleOpenAIRes:
		appendOpenAIResOutput(&sb, output)
	case consts.StyleAnthropic:
		appendAnthropicOutput(&sb, output)
	case consts.StyleGemini, consts.StyleGeminiEmbeddings:
		appendGeminiOutput(&sb, output)
	}
	return sb.String()
}

func appendOpenAIOutput(sb *strings.Builder, output *models.OutputUnion) {
	if output.OfString != "" {
		appendOpenAIContentFromJSON(sb, output.OfString)
	}
	for _, chunk := range output.OfStringArray {
		appendOpenAIContentFromJSON(sb, chunk)
	}
}

func appendOpenAIContentFromJSON(sb *strings.Builder, payload string) {
	choices := gjson.Get(payload, "choices")
	if !choices.Exists() {
		return
	}
	choices.ForEach(func(_, choice gjson.Result) bool {
		if message := choice.Get("message"); message.Exists() {
			appendTextFromAny(sb, message.Get("content"))
		}
		if delta := choice.Get("delta"); delta.Exists() {
			appendTextFromAny(sb, delta.Get("content"))
			if text := delta.Get("text"); text.Exists() {
				appendText(sb, text.String())
			}
		}
		if text := choice.Get("text"); text.Exists() {
			appendText(sb, text.String())
		}
		return true
	})
}

func appendOpenAIResOutput(sb *strings.Builder, output *models.OutputUnion) {
	if output.OfString != "" {
		appendOpenAIResContentFromJSON(sb, output.OfString)
	}
	for _, chunk := range output.OfStringArray {
		appendOpenAIResStreamChunk(sb, chunk)
	}
}

func appendOpenAIResStreamChunk(sb *strings.Builder, payload string) {
	if typ := gjson.Get(payload, "type").String(); typ != "" {
		switch typ {
		case "response.output_text.delta":
			appendText(sb, gjson.Get(payload, "delta").String())
		case "response.output_text":
			appendText(sb, gjson.Get(payload, "text").String())
		case "response.completed":
			appendOpenAIResContentFromJSON(sb, payload)
		}
		return
	}
	appendOpenAIResContentFromJSON(sb, payload)
}

func appendOpenAIResContentFromJSON(sb *strings.Builder, payload string) {
	paths := []string{"output", "response.output"}
	for _, path := range paths {
		items := gjson.Get(payload, path)
		if !items.Exists() {
			continue
		}
		items.ForEach(func(_, item gjson.Result) bool {
			appendTextFromAny(sb, item.Get("content"))
			appendTextFromAny(sb, item.Get("output"))
			appendTextFromAny(sb, item.Get("text"))
			return true
		})
	}
}

func appendAnthropicOutput(sb *strings.Builder, output *models.OutputUnion) {
	if output.OfString != "" {
		appendAnthropicContentFromJSON(sb, output.OfString)
	}
	for _, chunk := range output.OfStringArray {
		appendAnthropicStreamChunk(sb, chunk)
	}
}

func appendAnthropicStreamChunk(sb *strings.Builder, payload string) {
	if typ := gjson.Get(payload, "type").String(); typ != "" {
		switch typ {
		case "content_block_delta":
			appendText(sb, gjson.Get(payload, "delta.text").String())
		case "content_block_start":
			appendText(sb, gjson.Get(payload, "content_block.text").String())
		case "message_start", "message_delta", "message_stop":
			// no-op
		}
		return
	}
	appendAnthropicContentFromJSON(sb, payload)
}

func appendAnthropicContentFromJSON(sb *strings.Builder, payload string) {
	content := gjson.Get(payload, "content")
	if !content.Exists() {
		return
	}
	content.ForEach(func(_, item gjson.Result) bool {
		appendText(sb, item.Get("text").String())
		return true
	})
}

func appendGeminiOutput(sb *strings.Builder, output *models.OutputUnion) {
	if output.OfString != "" {
		appendGeminiContentFromJSON(sb, output.OfString)
	}
	for _, chunk := range output.OfStringArray {
		appendGeminiContentFromJSON(sb, chunk)
	}
}

func appendGeminiContentFromJSON(sb *strings.Builder, payload string) {
	candidates := gjson.Get(payload, "candidates")
	if !candidates.Exists() {
		return
	}
	candidates.ForEach(func(_, cand gjson.Result) bool {
		appendTextFromAny(sb, cand.Get("content.parts"))
		return true
	})
}

func appendTextFromAny(sb *strings.Builder, value gjson.Result) {
	if !value.Exists() {
		return
	}
	if value.Type == gjson.String {
		appendText(sb, value.String())
		return
	}
	if value.IsArray() {
		value.ForEach(func(_, item gjson.Result) bool {
			appendTextFromAny(sb, item)
			return true
		})
		return
	}
	if value.Type == gjson.JSON {
		if text := value.Get("text"); text.Exists() {
			appendText(sb, text.String())
		}
		if text := value.Get("input_text"); text.Exists() {
			appendText(sb, text.String())
		}
		if text := value.Get("output_text"); text.Exists() {
			appendText(sb, text.String())
		}
		if text := value.Get("delta"); text.Exists() && text.Type == gjson.String {
			appendText(sb, text.String())
		}
		if content := value.Get("content"); content.Exists() {
			appendTextFromAny(sb, content)
		}
		if parts := value.Get("parts"); parts.Exists() {
			appendTextFromAny(sb, parts)
		}
	}
}

func appendText(sb *strings.Builder, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString(text)
}
