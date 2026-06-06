package ifacebridge

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/racio/orvion/models"
)

func TestResolvePlanResponsesToChat(t *testing.T) {
	provider := models.Provider{
		Capabilities:               models.ProviderCapabilities([]string{"chat"}),
		InterfaceConversionEnabled: 1,
		InterfaceConversionTarget:  "chat",
	}
	plan, ok := ResolvePlan(provider, "responses")
	if !ok {
		t.Fatalf("expected conversion plan")
	}
	if !plan.Enabled || plan.ClientEndpoint != EndpointResponses || plan.UpstreamEndpoint != EndpointChat {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestResolvePlanFallbackTargetForUnsupportedEndpoint(t *testing.T) {
	provider := models.Provider{
		Capabilities:               models.ProviderCapabilities([]string{"openai", "claude"}),
		InterfaceConversionEnabled: 1,
		InterfaceConversionTarget:  "messages",
	}
	plan, ok := ResolvePlan(provider, "chat")
	if !ok {
		t.Fatalf("expected conversion plan")
	}
	if !plan.Enabled || plan.ClientEndpoint != EndpointChat || plan.UpstreamEndpoint != EndpointMessages {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestConvertRequestResponsesToChat(t *testing.T) {
	raw := []byte(`{"model":"gpt-4o-mini","stream":false,"instructions":"sys","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]},{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"shanghai\"}"},{"type":"function_call_output","call_id":"call_1","output":"sunny"}],"tools":[{"type":"function","name":"weather","parameters":{"type":"object"}}]}`)
	plan := Plan{Enabled: true, ClientEndpoint: EndpointResponses, UpstreamEndpoint: EndpointChat}
	converted, err := ConvertRequestBody(plan, raw)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	content := string(converted)
	for _, expected := range []string{`"messages"`, `"role":"system"`, `"role":"tool"`, `"tool_calls"`, `"tools"`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted request missing %s: %s", expected, content)
		}
	}
}

func TestConvertRequestMessagesToChat(t *testing.T) {
	raw := []byte(`{"model":"claude-sonnet","system":"you are helpful","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"weather","input":{"city":"beijing"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":[{"type":"text","text":"18C"}]}]}],"tools":[{"name":"weather","input_schema":{"type":"object"}}]}`)
	plan := Plan{Enabled: true, ClientEndpoint: EndpointMessages, UpstreamEndpoint: EndpointChat}
	converted, err := ConvertRequestBody(plan, raw)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	content := string(converted)
	for _, expected := range []string{`"role":"system"`, `"tool_calls"`, `"role":"tool"`, `"tool_call_id":"tool_1"`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted request missing %s: %s", expected, content)
		}
	}
}

func TestConvertRequestChatToResponses(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","stream":false,"messages":[{"role":"system","content":"be concise"},{"role":"user","content":"hello"}]}`)
	plan := Plan{Enabled: true, ClientEndpoint: EndpointChat, UpstreamEndpoint: EndpointResponses}
	converted, err := ConvertRequestBody(plan, raw)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	content := string(converted)
	for _, expected := range []string{`"input"`, `"instructions":"be concise"`, `"role":"user"`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted request missing %s: %s", expected, content)
		}
	}
}

func TestConvertResponseChatToResponsesNonStream(t *testing.T) {
	payload := `{"id":"chatcmpl_1","created":1700000000,"model":"gpt-4o-mini","choices":[{"message":{"role":"assistant","content":"hello world"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}
	plan := Plan{Enabled: true, ClientEndpoint: EndpointResponses, UpstreamEndpoint: EndpointChat}

	converted, err := ConvertResponseBody(plan, res, false)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	body, _ := io.ReadAll(converted.Body)
	content := string(body)
	for _, expected := range []string{`"object":"response"`, `"output_text"`, `"input_tokens":10`, `"output_tokens":5`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted response missing %s: %s", expected, content)
		}
	}
}

func TestConvertResponseChatToMessagesStream(t *testing.T) {
	streamPayload := strings.Join([]string{
		"data: {\"id\":\"chatcmpl_2\",\"created\":1700000010,\"model\":\"gpt-4o-mini\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}",
		"",
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}",
		"",
		"data: [DONE]",
		"",
	}, "\n")

	res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(streamPayload)), Header: make(http.Header)}
	plan := Plan{Enabled: true, ClientEndpoint: EndpointMessages, UpstreamEndpoint: EndpointChat}
	converted, err := ConvertResponseBody(plan, res, true)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	body, _ := io.ReadAll(converted.Body)
	content := string(body)
	for _, expected := range []string{"event: message_start", "event: content_block_delta", "hello world", "event: message_stop"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted stream missing %s: %s", expected, content)
		}
	}
}

func TestConvertResponseResponsesToChatNonStream(t *testing.T) {
	payload := `{"id":"resp_1","object":"response","created_at":1700000001,"model":"gpt-4o-mini","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello responses"}]}],"usage":{"input_tokens":12,"output_tokens":6,"total_tokens":18}}`
	res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}
	plan := Plan{Enabled: true, ClientEndpoint: EndpointChat, UpstreamEndpoint: EndpointResponses}

	converted, err := ConvertResponseBody(plan, res, false)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	body, _ := io.ReadAll(converted.Body)
	content := string(body)
	for _, expected := range []string{`"object":"chat.completion"`, `"hello responses"`, `"prompt_tokens":12`, `"completion_tokens":6`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted response missing %s: %s", expected, content)
		}
	}
}

func TestConvertResponseResponsesToolCallStreamToChat(t *testing.T) {
	streamPayload := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_tool","created_at":1700000020,"model":"gpt-4o-mini"}}`,
		"",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"set_model_status","arguments":""}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"target\":\"claude\""}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":",\"enabled\":false}"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_tool","created_at":1700000020,"model":"gpt-4o-mini","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"set_model_status","arguments":"{\"target\":\"claude\",\"enabled\":false}"}],"usage":{"input_tokens":20,"output_tokens":8,"total_tokens":28}}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(streamPayload)), Header: make(http.Header)}
	plan := Plan{Enabled: true, ClientEndpoint: EndpointChat, UpstreamEndpoint: EndpointResponses}
	converted, err := ConvertResponseBody(plan, res, true)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	body, _ := io.ReadAll(converted.Body)
	content := string(body)
	for _, expected := range []string{`"tool_calls"`, `"set_model_status"`, `{\"target\":\"claude\",\"enabled\":false}`, `"finish_reason":"tool_calls"`, `"prompt_tokens":20`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted tool stream missing %s: %s", expected, content)
		}
	}
}

func TestConvertResponseMessagesToolUseStreamToChat(t *testing.T) {
	streamPayload := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_tool","model":"claude-sonnet","usage":{"input_tokens":14}}}`,
		"",
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_system_logs","input":{}}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"level\":\"ERROR\"}"}}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":6}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(streamPayload)), Header: make(http.Header)}
	plan := Plan{Enabled: true, ClientEndpoint: EndpointChat, UpstreamEndpoint: EndpointMessages}
	converted, err := ConvertResponseBody(plan, res, true)
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}
	body, _ := io.ReadAll(converted.Body)
	content := string(body)
	for _, expected := range []string{`"tool_calls"`, `"read_system_logs"`, `{\"level\":\"ERROR\"}`, `"finish_reason":"tool_calls"`, `"completion_tokens":6`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("converted messages tool stream missing %s: %s", expected, content)
		}
	}
}
