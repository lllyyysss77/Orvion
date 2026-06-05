package runtime

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIChatCompletionPayloadCollapsesNonStreamSSE(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"id":"msg_1","object":"chat.completion.chunk","created":1780576672,"model":"grok-4.3-high","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}],"usage":null}`,
		`data: {"id":"msg_1","object":"chat.completion.chunk","created":1780576672,"model":"grok-4.3-high","choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":null}],"usage":null}`,
		`data:{"id":"msg_1","object":"chat.completion.chunk","created":1780576672,"model":"grok-4.3-high","choices":[{"index":0,"delta":{"content":"，世界"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		`data: [DONE]`,
	}, "\n")

	got := NormalizeOpenAIChatCompletionPayload([]byte(raw), false)
	if !gjson.ValidBytes(got) {
		t.Fatalf("归一化后应该是 JSON，got=%s", string(got))
	}
	if content := gjson.GetBytes(got, "choices.0.message.content").String(); content != "你好，世界" {
		t.Fatalf("content mismatch: %q", content)
	}
	if object := gjson.GetBytes(got, "object").String(); object != "chat.completion" {
		t.Fatalf("object mismatch: %q", object)
	}
	if finishReason := gjson.GetBytes(got, "choices.0.finish_reason").String(); finishReason != "stop" {
		t.Fatalf("finish_reason mismatch: %q", finishReason)
	}
	if total := gjson.GetBytes(got, "usage.total_tokens").Int(); total != 7 {
		t.Fatalf("usage.total_tokens mismatch: %d", total)
	}
}

func TestNormalizeOpenAIChatCompletionPayloadKeepsInvalidNonSSE(t *testing.T) {
	raw := []byte("not-json")
	got := NormalizeOpenAIChatCompletionPayload(raw, false)
	if string(got) != string(raw) {
		t.Fatalf("非 SSE 非 JSON 内容不应被改写: %s", string(got))
	}
}
