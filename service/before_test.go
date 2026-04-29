package service

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestBeforerOpenAI_AutofillSingleToolCallID(t *testing.T) {
	input := []byte(`{
		"model":"gpt-4.1",
		"messages":[
			{"role":"user","content":"查天气"},
			{"role":"assistant","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
			{"role":"tool","content":"{\"temperature\":26}"}
		]
	}`)

	before, err := BeforerOpenAI(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolCallID := gjson.GetBytes(before.raw, "messages.2.tool_call_id").String()
	if toolCallID != "call_abc" {
		t.Fatalf("tool_call_id mismatch, got=%q want=%q", toolCallID, "call_abc")
	}
}

func TestBeforerOpenAI_NoAutofillWhenMultipleToolCallIDs(t *testing.T) {
	input := []byte(`{
		"model":"gpt-4.1",
		"messages":[
			{"role":"assistant","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"a","arguments":"{}"}},
				{"id":"call_2","type":"function","function":{"name":"b","arguments":"{}"}}
			]},
			{"role":"tool","content":"done"}
		]
	}`)

	before, err := BeforerOpenAI(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gjson.GetBytes(before.raw, "messages.1.tool_call_id").Exists() {
		t.Fatalf("tool_call_id should not be auto-filled when multiple candidates exist")
	}
}

func TestBeforerOpenAI_KeepExistingToolCallID(t *testing.T) {
	input := []byte(`{
		"model":"gpt-4.1",
		"messages":[
			{"role":"assistant","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_abc","content":"ok"}
		]
	}`)

	before, err := BeforerOpenAI(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolCallID := gjson.GetBytes(before.raw, "messages.1.tool_call_id").String()
	if toolCallID != "call_abc" {
		t.Fatalf("existing tool_call_id should be preserved, got=%q", toolCallID)
	}
}
