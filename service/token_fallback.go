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
		PromptTokens:     estimateTokens(model, inputText),
		CompletionTokens: estimateTokens(model, outputText),
	}
	if style == consts.StyleOpenAIEmbeddings || style == consts.StyleGeminiEmbeddings {
		usage.CompletionTokens = 0
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}

func estimateTokens(model string, text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	if counted, err := countTokensWithModel(model, text); err == nil && counted > 0 {
		return counted
	}
	return estimateTokensByHeuristic(text)
}

func estimateTokensByHeuristic(text string) int64 {
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
		messages := gjson.GetBytes(raw, "messages")
		appendTextFromAny(&sb, messages)
		appendOpenAIMessageFunctionAndToolCalls(&sb, messages)
		appendTextFromAny(&sb, gjson.GetBytes(raw, "input"))
		appendTextFromAny(&sb, gjson.GetBytes(raw, "prompt"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tools"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "functions"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tool_choice"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "response_format"))
	case consts.StyleOpenAIRes:
		input := gjson.GetBytes(raw, "input")
		appendTextFromAny(&sb, input)
		appendOpenAIResponsesInputDetails(&sb, input)
		appendTextFromAny(&sb, gjson.GetBytes(raw, "instructions"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tools"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tool_choice"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "text.format"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "response_format"))
	case consts.StyleAnthropic:
		appendTextFromAny(&sb, gjson.GetBytes(raw, "system"))
		appendTextFromAny(&sb, gjson.GetBytes(raw, "messages"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tools"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tool_choice"))
	case consts.StyleGemini, consts.StyleGeminiEmbeddings:
		appendTextFromAny(&sb, gjson.GetBytes(raw, "contents"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tools"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "toolConfig"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "tool_config"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "generationConfig"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "generation_config"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "systemInstruction"))
		appendRawJSONResult(&sb, gjson.GetBytes(raw, "system_instruction"))
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
	text := strings.TrimSpace(sb.String())
	if text != "" {
		return text
	}
	return extractRawOutputText(output)
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

func appendRawJSONResult(sb *strings.Builder, value gjson.Result) {
	if !value.Exists() {
		return
	}
	if value.Type == gjson.String {
		appendText(sb, value.String())
		return
	}
	if value.Type == gjson.JSON || value.IsArray() {
		appendText(sb, value.Raw)
		return
	}
	appendText(sb, value.String())
}

func appendOpenAIMessageFunctionAndToolCalls(sb *strings.Builder, messages gjson.Result) {
	if !messages.Exists() || !messages.IsArray() {
		return
	}
	messages.ForEach(func(_, message gjson.Result) bool {
		if functionCall := message.Get("function_call"); functionCall.Exists() {
			appendText(sb, functionCall.Get("name").String())
			appendText(sb, functionCall.Get("arguments").String())
		}
		if toolCalls := message.Get("tool_calls"); toolCalls.Exists() && toolCalls.IsArray() {
			toolCalls.ForEach(func(_, call gjson.Result) bool {
				appendText(sb, call.Get("id").String())
				appendText(sb, call.Get("type").String())
				function := call.Get("function")
				if function.Exists() {
					appendText(sb, function.Get("name").String())
					appendText(sb, function.Get("description").String())
					appendText(sb, function.Get("arguments").String())
					appendRawJSONResult(sb, function.Get("parameters"))
				}
				return true
			})
		}
		return true
	})
}

func appendOpenAIResponsesInputDetails(sb *strings.Builder, input gjson.Result) {
	if !input.Exists() || !input.IsArray() {
		return
	}
	input.ForEach(func(_, item gjson.Result) bool {
		itemType := strings.TrimSpace(item.Get("type").String())
		switch itemType {
		case "function_call":
			appendText(sb, item.Get("name").String())
			appendText(sb, item.Get("arguments").String())
		case "function_call_output":
			appendText(sb, item.Get("output").String())
		}
		return true
	})
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

func extractRawOutputText(output *models.OutputUnion) string {
	if output == nil {
		return ""
	}
	var sb strings.Builder
	if output.OfString != "" {
		appendText(&sb, output.OfString)
	}
	for _, chunk := range output.OfStringArray {
		appendText(&sb, chunk)
	}
	return strings.TrimSpace(sb.String())
}
