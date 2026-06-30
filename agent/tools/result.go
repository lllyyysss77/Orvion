package tools

import (
	"encoding/json"
	"strings"
)

func ToolResult(ok bool, text string) string {
	return toolResultWithFinal(ok, text, false)
}

func ToolFinalResult(ok bool, text string) string {
	return toolResultWithFinal(ok, text, true)
}

func toolResultWithFinal(ok bool, text string, final bool) string {
	payload := ResultPayload{
		OK:    ok,
		Text:  text,
		Final: final,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return text
	}
	return string(raw)
}

func DirectFinalText(raw string) (string, bool) {
	payload := ParseResultPayload(raw)
	text := strings.TrimSpace(payload.Text)
	return text, payload.Final && text != "" && strings.Contains(text, "[orvion:")
}
