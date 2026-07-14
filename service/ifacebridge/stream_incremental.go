package ifacebridge

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type incrementalStreamEmitter struct {
	out         io.Writer
	target      string
	started     bool
	textStarted bool
	id          string
	model       string
	created     int64
}

func newIncrementalStreamEmitter(out io.Writer, target string) *incrementalStreamEmitter {
	return &incrementalStreamEmitter{out: out, target: target}
}

func (e *incrementalStreamEmitter) captureMeta(agg aggregatedChat) {
	if e.id == "" {
		e.id = agg.ID
	}
	if e.model == "" {
		e.model = agg.Model
	}
	if e.created == 0 {
		e.created = agg.Created
		if e.created == 0 {
			e.created = time.Now().Unix()
		}
	}
}

func (e *incrementalStreamEmitter) EmitText(agg aggregatedChat, text string) error {
	if text == "" {
		return nil
	}
	e.captureMeta(agg)
	if err := e.ensureStarted(); err != nil {
		return err
	}
	switch e.target {
	case EndpointChat:
		e.textStarted = true
		return writeSSEData(e.out, map[string]any{
			"id": nonEmptyString(e.id, "chatcmpl_bridge"), "object": "chat.completion.chunk", "created": e.created, "model": e.model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": nil}},
		})
	case EndpointResponses:
		if !e.textStarted {
			e.textStarted = true
			if err := writeSSEData(e.out, map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"id": "msg_" + nonEmptyString(e.id, "bridge"), "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}},
			}); err != nil {
				return err
			}
			if err := writeSSEData(e.out, map[string]any{
				"type": "response.content_part.added", "item_id": "msg_" + nonEmptyString(e.id, "bridge"), "output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": ""},
			}); err != nil {
				return err
			}
		}
		return writeSSEData(e.out, map[string]any{
			"type": "response.output_text.delta", "item_id": "msg_" + nonEmptyString(e.id, "bridge"), "output_index": 0, "content_index": 0, "delta": text,
		})
	case EndpointMessages:
		if !e.textStarted {
			e.textStarted = true
			if err := writeSSEEventData(e.out, "content_block_start", map[string]any{
				"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""},
			}); err != nil {
				return err
			}
		}
		return writeSSEEventData(e.out, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text},
		})
	default:
		return fmt.Errorf("unsupported incremental stream target: %s", e.target)
	}
}

func (e *incrementalStreamEmitter) ensureStarted() error {
	if e.started {
		return nil
	}
	e.started = true
	switch e.target {
	case EndpointChat:
		return writeSSEData(e.out, map[string]any{
			"id": nonEmptyString(e.id, "chatcmpl_bridge"), "object": "chat.completion.chunk", "created": e.created, "model": e.model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
		})
	case EndpointResponses:
		return writeSSEData(e.out, map[string]any{"type": "response.created", "response": map[string]any{
			"id": nonEmptyString(e.id, "resp_bridge"), "object": "response", "created_at": e.created, "status": "in_progress", "model": e.model,
		}})
	case EndpointMessages:
		return writeSSEEventData(e.out, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": nonEmptyString(e.id, "msg_bridge"), "type": "message", "role": "assistant", "model": e.model, "content": []any{},
			"stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		}})
	default:
		return fmt.Errorf("unsupported incremental stream target: %s", e.target)
	}
}

func (e *incrementalStreamEmitter) Finish(agg aggregatedChat) error {
	e.captureMeta(agg)
	if !e.textStarted && agg.Content != "" {
		if err := e.EmitText(agg, agg.Content); err != nil {
			return err
		}
	}
	if err := e.ensureStarted(); err != nil {
		return err
	}
	switch e.target {
	case EndpointChat:
		for index, call := range agg.ToolCalls {
			toolChunk := map[string]any{
				"id": nonEmptyString(e.id, "chatcmpl_bridge"), "object": "chat.completion.chunk", "created": e.created, "model": e.model,
				"choices": []any{map[string]any{
					"index": 0, "finish_reason": nil,
					"delta": map[string]any{"tool_calls": []any{map[string]any{
						"index": index, "id": nonEmptyString(call.ID, fmt.Sprintf("call_%d", index)), "type": "function",
						"function": map[string]any{"name": call.Name, "arguments": call.Arguments},
					}}},
				}},
			}
			if err := writeSSEData(e.out, toolChunk); err != nil {
				return err
			}
		}
		if err := writeSSEData(e.out, map[string]any{
			"id": nonEmptyString(e.id, "chatcmpl_bridge"), "object": "chat.completion.chunk", "created": e.created, "model": e.model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": chatFinishReason(agg)}}, "usage": buildChatUsage(agg),
		}); err != nil {
			return err
		}
		_, err := io.WriteString(e.out, "data: [DONE]\n\n")
		return err

	case EndpointResponses:
		messageOffset := 0
		if e.textStarted {
			messageOffset = 1
			itemID := "msg_" + nonEmptyString(e.id, "bridge")
			for _, event := range []map[string]any{
				{"type": "response.output_text.done", "item_id": itemID, "output_index": 0, "content_index": 0, "text": agg.Content},
				{"type": "response.content_part.done", "item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": agg.Content}},
				{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"id": itemID, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": agg.Content}}}},
			} {
				if err := writeSSEData(e.out, event); err != nil {
					return err
				}
			}
		}
		for index, call := range agg.ToolCalls {
			outputIndex := messageOffset + index
			itemID := fmt.Sprintf("fc_%s_%d", nonEmptyString(e.id, "bridge"), index)
			callID := nonEmptyString(call.ID, fmt.Sprintf("call_%d", index))
			for _, event := range []map[string]any{
				{"type": "response.output_item.added", "output_index": outputIndex, "item": map[string]any{"id": itemID, "type": "function_call", "status": "in_progress", "call_id": callID, "name": call.Name, "arguments": ""}},
				{"type": "response.function_call_arguments.delta", "item_id": itemID, "output_index": outputIndex, "delta": call.Arguments},
				{"type": "response.function_call_arguments.done", "item_id": itemID, "output_index": outputIndex, "arguments": call.Arguments},
				{"type": "response.output_item.done", "output_index": outputIndex, "item": map[string]any{"id": itemID, "type": "function_call", "status": "completed", "call_id": callID, "name": call.Name, "arguments": call.Arguments}},
			} {
				if err := writeSSEData(e.out, event); err != nil {
					return err
				}
			}
		}
		payload, err := buildResponsesNonStreamPayload(agg)
		if err != nil {
			return err
		}
		var response any
		if err := json.Unmarshal(payload, &response); err != nil {
			return err
		}
		eventType := "response.completed"
		if responsesStatus(agg) == "incomplete" {
			eventType = "response.incomplete"
		}
		if err := writeSSEData(e.out, map[string]any{"type": eventType, "response": response}); err != nil {
			return err
		}
		_, err = io.WriteString(e.out, "data: [DONE]\n\n")
		return err

	case EndpointMessages:
		nextIndex := 0
		if e.textStarted {
			if err := writeSSEEventData(e.out, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
				return err
			}
			nextIndex = 1
		}
		for _, call := range agg.ToolCalls {
			if err := writeSSEEventData(e.out, "content_block_start", map[string]any{
				"type": "content_block_start", "index": nextIndex,
				"content_block": map[string]any{"type": "tool_use", "id": nonEmptyString(call.ID, fmt.Sprintf("toolu_%d", nextIndex)), "name": call.Name, "input": map[string]any{}},
			}); err != nil {
				return err
			}
			if strings.TrimSpace(call.Arguments) != "" {
				if err := writeSSEEventData(e.out, "content_block_delta", map[string]any{
					"type": "content_block_delta", "index": nextIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": call.Arguments},
				}); err != nil {
					return err
				}
			}
			if err := writeSSEEventData(e.out, "content_block_stop", map[string]any{"type": "content_block_stop", "index": nextIndex}); err != nil {
				return err
			}
			nextIndex++
		}
		if err := writeSSEEventData(e.out, "message_delta", map[string]any{
			"type": "message_delta", "delta": map[string]any{"stop_reason": messagesStopReason(agg), "stop_sequence": nilIfEmpty(agg.StopSequence)},
			"usage": buildMessagesUsage(agg),
		}); err != nil {
			return err
		}
		return writeSSEEventData(e.out, "message_stop", map[string]any{"type": "message_stop"})
	default:
		return fmt.Errorf("unsupported incremental stream target: %s", e.target)
	}
}
