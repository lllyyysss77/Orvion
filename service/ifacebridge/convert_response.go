package ifacebridge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type aggregatedChat struct {
	ID               string
	Model            string
	Created          int64
	Content          string
	ToolCalls        []aggregatedToolCall
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

type aggregatedToolCall struct {
	ID        string
	Name      string
	Arguments string
}

func ConvertResponseBody(plan Plan, upstreamRes *http.Response, stream bool) (*http.Response, error) {
	if !plan.Enabled {
		return upstreamRes, nil
	}
	from := NormalizeEndpoint(plan.UpstreamEndpoint)
	to := NormalizeEndpoint(plan.ClientEndpoint)
	if !SupportsConversion(from, to) {
		return nil, fmt.Errorf("unsupported response conversion: %s -> %s", from, to)
	}

	if !stream {
		body, err := io.ReadAll(upstreamRes.Body)
		if err != nil {
			return nil, err
		}
		_ = upstreamRes.Body.Close()

		agg, err := aggregateFromNonStream(body, from)
		if err != nil {
			return nil, err
		}
		converted, contentType, err := buildNonStreamFromAggregated(agg, to)
		if err != nil {
			return nil, err
		}

		upstreamRes.Body = io.NopCloser(bytes.NewReader(converted))
		upstreamRes.ContentLength = int64(len(converted))
		upstreamRes.Header.Del("Content-Length")
		upstreamRes.Header.Set("Content-Length", strconv.Itoa(len(converted)))
		upstreamRes.Header.Set("Content-Type", contentType)
		return upstreamRes, nil
	}

	pr, pw := io.Pipe()
	originBody := upstreamRes.Body
	go func() {
		err := streamConvert(originBody, pw, from, to)
		_ = originBody.Close()
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	upstreamRes.Body = pr
	upstreamRes.ContentLength = -1
	upstreamRes.Header.Del("Content-Length")
	upstreamRes.Header.Set("Content-Type", "text/event-stream")
	return upstreamRes, nil
}

func streamConvert(upstream io.Reader, out io.Writer, fromEndpoint string, toEndpoint string) error {
	agg, err := aggregateFromStream(upstream, fromEndpoint)
	if err != nil {
		return err
	}
	return buildStreamFromAggregated(out, agg, toEndpoint)
}

func aggregateFromNonStream(raw []byte, endpoint string) (aggregatedChat, error) {
	switch endpoint {
	case EndpointChat:
		return aggregateChatFromNonStream(raw)
	case EndpointResponses:
		return aggregateResponsesFromNonStream(raw)
	case EndpointMessages:
		return aggregateMessagesFromNonStream(raw)
	default:
		return aggregatedChat{}, fmt.Errorf("unsupported non-stream source endpoint: %s", endpoint)
	}
}

func aggregateFromStream(upstream io.Reader, endpoint string) (aggregatedChat, error) {
	switch endpoint {
	case EndpointChat:
		return aggregateChatFromStream(upstream)
	case EndpointResponses:
		return aggregateResponsesFromStream(upstream)
	case EndpointMessages:
		return aggregateMessagesFromStream(upstream)
	default:
		return aggregatedChat{}, fmt.Errorf("unsupported stream source endpoint: %s", endpoint)
	}
}

func buildNonStreamFromAggregated(agg aggregatedChat, endpoint string) ([]byte, string, error) {
	switch endpoint {
	case EndpointChat:
		payload, err := buildChatNonStreamPayload(agg)
		return payload, "application/json", err
	case EndpointResponses:
		payload, err := buildResponsesNonStreamPayload(agg)
		return payload, "application/json", err
	case EndpointMessages:
		payload, err := buildMessagesNonStreamPayload(agg)
		return payload, "application/json", err
	default:
		return nil, "", fmt.Errorf("unsupported target endpoint: %s", endpoint)
	}
}

func buildStreamFromAggregated(out io.Writer, agg aggregatedChat, endpoint string) error {
	switch endpoint {
	case EndpointChat:
		return writeChatStream(out, agg)
	case EndpointResponses:
		return writeResponsesStream(out, agg)
	case EndpointMessages:
		return writeMessagesStream(out, agg)
	default:
		return fmt.Errorf("unsupported target endpoint: %s", endpoint)
	}
}

func aggregateChatFromNonStream(raw []byte) (aggregatedChat, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return aggregatedChat{}, err
	}

	agg := aggregatedChat{}
	agg.ID = strings.TrimSpace(toString(payload["id"]))
	agg.Model = strings.TrimSpace(toString(payload["model"]))
	agg.Created = toInt64(payload["created"])
	if agg.Created == 0 {
		agg.Created = time.Now().Unix()
	}
	if usage, ok := payload["usage"].(map[string]any); ok {
		agg.PromptTokens = toInt64(usage["prompt_tokens"])
		agg.CompletionTokens = toInt64(usage["completion_tokens"])
		agg.TotalTokens = toInt64(usage["total_tokens"])
	}
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if message, ok := first["message"].(map[string]any); ok {
				agg.Content = extractMessageText(message["content"])
				agg.ToolCalls = extractChatToolCalls(message["tool_calls"])
			}
		}
	}
	if agg.TotalTokens == 0 {
		agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	}
	return agg, nil
}

func aggregateResponsesFromNonStream(raw []byte) (aggregatedChat, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return aggregatedChat{}, err
	}
	agg := aggregatedChat{}
	agg.ID = strings.TrimSpace(toString(payload["id"]))
	agg.Model = strings.TrimSpace(toString(payload["model"]))
	agg.Created = toInt64(payload["created_at"])
	if agg.Created == 0 {
		agg.Created = time.Now().Unix()
	}
	if usage, ok := payload["usage"].(map[string]any); ok {
		agg.PromptTokens = toInt64(usage["input_tokens"])
		agg.CompletionTokens = toInt64(usage["output_tokens"])
		agg.TotalTokens = toInt64(usage["total_tokens"])
	}
	if output, ok := payload["output"].([]any); ok {
		for _, rawItem := range output {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			itemType := strings.ToLower(strings.TrimSpace(toString(item["type"])))
			if itemType == "function_call" {
				if call := extractResponsesToolCall(item); call.Name != "" {
					agg.ToolCalls = append(agg.ToolCalls, call)
				}
				continue
			}
			if itemType != "message" {
				continue
			}
			if content, ok := item["content"].([]any); ok {
				for _, rawPart := range content {
					part, ok := rawPart.(map[string]any)
					if !ok {
						continue
					}
					if strings.ToLower(strings.TrimSpace(toString(part["type"]))) == "output_text" {
						agg.Content += toString(part["text"])
					}
				}
			}
		}
	}
	if agg.TotalTokens == 0 {
		agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	}
	return agg, nil
}

func aggregateMessagesFromNonStream(raw []byte) (aggregatedChat, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return aggregatedChat{}, err
	}
	agg := aggregatedChat{}
	agg.ID = strings.TrimSpace(toString(payload["id"]))
	agg.Model = strings.TrimSpace(toString(payload["model"]))
	agg.Created = time.Now().Unix()
	if usage, ok := payload["usage"].(map[string]any); ok {
		agg.PromptTokens = toInt64(usage["input_tokens"])
		agg.CompletionTokens = toInt64(usage["output_tokens"])
	}
	if content, ok := payload["content"].([]any); ok {
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			partType := strings.ToLower(strings.TrimSpace(toString(part["type"])))
			if partType == "tool_use" {
				if call := extractMessagesToolCall(part); call.Name != "" {
					agg.ToolCalls = append(agg.ToolCalls, call)
				}
				continue
			}
			if partType == "text" {
				agg.Content += toString(part["text"])
			}
		}
	}
	agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	return agg, nil
}

func aggregateChatFromStream(upstream io.Reader) (aggregatedChat, error) {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 0, 8192), 64*1024*1024)

	agg := aggregatedChat{Created: time.Now().Unix()}
	var contentBuilder strings.Builder
	toolPartials := map[int]*aggregatedToolCall{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if agg.ID == "" {
			agg.ID = strings.TrimSpace(toString(chunk["id"]))
		}
		if agg.Model == "" {
			agg.Model = strings.TrimSpace(toString(chunk["model"]))
		}
		if created := toInt64(chunk["created"]); created > 0 {
			agg.Created = created
		}

		if usage, ok := chunk["usage"].(map[string]any); ok {
			if v := toInt64(usage["prompt_tokens"]); v > agg.PromptTokens {
				agg.PromptTokens = v
			}
			if v := toInt64(usage["completion_tokens"]); v > agg.CompletionTokens {
				agg.CompletionTokens = v
			}
			if v := toInt64(usage["total_tokens"]); v > agg.TotalTokens {
				agg.TotalTokens = v
			}
		}

		choices, ok := chunk["choices"].([]any)
		if !ok || len(choices) == 0 {
			continue
		}
		first, ok := choices[0].(map[string]any)
		if !ok {
			continue
		}
		delta, ok := first["delta"].(map[string]any)
		if !ok {
			continue
		}
		text := extractMessageText(delta["content"])
		if text != "" {
			contentBuilder.WriteString(text)
		}
		mergeChatToolCallDeltas(toolPartials, delta["tool_calls"])
	}
	if err := scanner.Err(); err != nil {
		return aggregatedChat{}, err
	}
	if agg.TotalTokens == 0 {
		agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	}
	agg.Content = contentBuilder.String()
	agg.ToolCalls = orderedToolCallPartials(toolPartials)
	return agg, nil
}

func aggregateResponsesFromStream(upstream io.Reader) (aggregatedChat, error) {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 0, 8192), 64*1024*1024)
	agg := aggregatedChat{Created: time.Now().Unix()}
	var contentBuilder strings.Builder
	toolPartials := map[int]*aggregatedToolCall{}
	toolIndexByItemID := map[string]int{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		typeName := strings.ToLower(strings.TrimSpace(toString(chunk["type"])))
		switch typeName {
		case "response.created":
			if response, ok := chunk["response"].(map[string]any); ok {
				if agg.ID == "" {
					agg.ID = strings.TrimSpace(toString(response["id"]))
				}
				if agg.Model == "" {
					agg.Model = strings.TrimSpace(toString(response["model"]))
				}
				if created := toInt64(response["created_at"]); created > 0 {
					agg.Created = created
				}
			}
		case "response.output_item.added", "response.output_item.done":
			if item, ok := chunk["item"].(map[string]any); ok {
				if strings.ToLower(strings.TrimSpace(toString(item["type"]))) == "function_call" {
					index := int(toInt64(chunk["output_index"]))
					mergeResponsesToolCallItem(toolPartials, toolIndexByItemID, index, item)
				}
			}
		case "response.function_call_arguments.delta":
			index := int(toInt64(chunk["output_index"]))
			if index == 0 {
				if itemID := strings.TrimSpace(toString(chunk["item_id"])); itemID != "" {
					if knownIndex, ok := toolIndexByItemID[itemID]; ok {
						index = knownIndex
					}
				}
			}
			call := ensureAggregatedToolCallPartial(toolPartials, index)
			call.Arguments += toString(chunk["delta"])
		case "response.function_call_arguments.done":
			index := int(toInt64(chunk["output_index"]))
			if index == 0 {
				if itemID := strings.TrimSpace(toString(chunk["item_id"])); itemID != "" {
					if knownIndex, ok := toolIndexByItemID[itemID]; ok {
						index = knownIndex
					}
				}
			}
			if arguments := toString(chunk["arguments"]); strings.TrimSpace(arguments) != "" {
				call := ensureAggregatedToolCallPartial(toolPartials, index)
				call.Arguments = arguments
			}
		case "response.output_text.delta":
			contentBuilder.WriteString(toString(chunk["delta"]))
		case "response.completed":
			if response, ok := chunk["response"].(map[string]any); ok {
				if agg.ID == "" {
					agg.ID = strings.TrimSpace(toString(response["id"]))
				}
				if agg.Model == "" {
					agg.Model = strings.TrimSpace(toString(response["model"]))
				}
				if created := toInt64(response["created_at"]); created > 0 {
					agg.Created = created
				}
				if usage, ok := response["usage"].(map[string]any); ok {
					agg.PromptTokens = maxInt64(agg.PromptTokens, toInt64(usage["input_tokens"]))
					agg.CompletionTokens = maxInt64(agg.CompletionTokens, toInt64(usage["output_tokens"]))
					agg.TotalTokens = maxInt64(agg.TotalTokens, toInt64(usage["total_tokens"]))
				}
				if output, ok := response["output"].([]any); ok {
					for itemIndex, rawItem := range output {
						item, ok := rawItem.(map[string]any)
						if !ok {
							continue
						}
						itemType := strings.ToLower(strings.TrimSpace(toString(item["type"])))
						if itemType == "function_call" {
							mergeResponsesToolCallItem(toolPartials, toolIndexByItemID, itemIndex, item)
							continue
						}
						if contentBuilder.Len() > 0 || itemType != "message" {
							continue
						}
						if blocks, ok := item["content"].([]any); ok {
							for _, rawBlock := range blocks {
								block, ok := rawBlock.(map[string]any)
								if !ok {
									continue
								}
								if strings.ToLower(strings.TrimSpace(toString(block["type"]))) == "output_text" {
									contentBuilder.WriteString(toString(block["text"]))
								}
							}
						}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return aggregatedChat{}, err
	}
	agg.Content = contentBuilder.String()
	agg.ToolCalls = orderedToolCallPartials(toolPartials)
	if agg.TotalTokens == 0 {
		agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	}
	return agg, nil
}

func aggregateMessagesFromStream(upstream io.Reader) (aggregatedChat, error) {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 0, 8192), 64*1024*1024)
	agg := aggregatedChat{Created: time.Now().Unix()}
	var contentBuilder strings.Builder
	toolPartials := map[int]*aggregatedToolCall{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		typeName := strings.ToLower(strings.TrimSpace(toString(chunk["type"])))
		switch typeName {
		case "message_start":
			if message, ok := chunk["message"].(map[string]any); ok {
				if agg.ID == "" {
					agg.ID = strings.TrimSpace(toString(message["id"]))
				}
				if agg.Model == "" {
					agg.Model = strings.TrimSpace(toString(message["model"]))
				}
				if usage, ok := message["usage"].(map[string]any); ok {
					agg.PromptTokens = maxInt64(agg.PromptTokens, toInt64(usage["input_tokens"]))
				}
			}
		case "content_block_start":
			if block, ok := chunk["content_block"].(map[string]any); ok {
				if strings.ToLower(strings.TrimSpace(toString(block["type"]))) == "tool_use" {
					index := int(toInt64(chunk["index"]))
					mergeMessagesToolUseBlock(toolPartials, index, block)
				}
			}
		case "content_block_delta":
			if delta, ok := chunk["delta"].(map[string]any); ok {
				deltaType := strings.ToLower(strings.TrimSpace(toString(delta["type"])))
				if deltaType == "text_delta" {
					contentBuilder.WriteString(toString(delta["text"]))
				}
				if deltaType == "input_json_delta" {
					index := int(toInt64(chunk["index"]))
					call := ensureAggregatedToolCallPartial(toolPartials, index)
					call.Arguments += toString(delta["partial_json"])
				}
			}
		case "message_delta":
			if usage, ok := chunk["usage"].(map[string]any); ok {
				agg.CompletionTokens = maxInt64(agg.CompletionTokens, toInt64(usage["output_tokens"]))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return aggregatedChat{}, err
	}
	agg.Content = contentBuilder.String()
	agg.ToolCalls = orderedToolCallPartials(toolPartials)
	agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	return agg, nil
}

func buildChatNonStreamPayload(agg aggregatedChat) ([]byte, error) {
	id := agg.ID
	if id == "" {
		id = fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
	}
	message := map[string]any{
		"role":    "assistant",
		"content": agg.Content,
	}
	finishReason := "stop"
	if len(agg.ToolCalls) > 0 {
		message["tool_calls"] = buildChatToolCallPayloads(agg.ToolCalls)
		if agg.Content == "" {
			message["content"] = nil
		}
		finishReason = "tool_calls"
	}
	payload := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": agg.Created,
		"model":   agg.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": finishReason,
			"message":       message,
		}},
		"usage": map[string]any{
			"prompt_tokens":     agg.PromptTokens,
			"completion_tokens": agg.CompletionTokens,
			"total_tokens":      agg.TotalTokens,
		},
	}
	return json.Marshal(payload)
}

func buildResponsesNonStreamPayload(agg aggregatedChat) ([]byte, error) {
	responseID := agg.ID
	if responseID == "" {
		responseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	messageID := "msg_" + responseID
	output := make([]any, 0, 1+len(agg.ToolCalls))
	if strings.TrimSpace(agg.Content) != "" || len(agg.ToolCalls) == 0 {
		output = append(output, map[string]any{
			"id":     messageID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{
				map[string]any{
					"type": "output_text",
					"text": agg.Content,
				},
			},
		})
	}
	for index, toolCall := range agg.ToolCalls {
		output = append(output, map[string]any{
			"id":        fmt.Sprintf("fc_%s_%d", responseID, index),
			"type":      "function_call",
			"status":    "completed",
			"call_id":   nonEmptyString(toolCall.ID, fmt.Sprintf("call_%d", index)),
			"name":      toolCall.Name,
			"arguments": toolCall.Arguments,
		})
	}

	payload := map[string]any{
		"id":         responseID,
		"object":     "response",
		"created_at": agg.Created,
		"status":     "completed",
		"model":      agg.Model,
		"output":     output,
		"usage": map[string]any{
			"input_tokens":  agg.PromptTokens,
			"output_tokens": agg.CompletionTokens,
			"total_tokens":  agg.TotalTokens,
		},
	}
	return json.Marshal(payload)
}

func buildMessagesNonStreamPayload(agg aggregatedChat) ([]byte, error) {
	messageID := agg.ID
	if messageID == "" {
		messageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	content := make([]any, 0, 1+len(agg.ToolCalls))
	if strings.TrimSpace(agg.Content) != "" || len(agg.ToolCalls) == 0 {
		content = append(content, map[string]any{
			"type": "text",
			"text": agg.Content,
		})
	}
	for index, toolCall := range agg.ToolCalls {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    nonEmptyString(toolCall.ID, fmt.Sprintf("toolu_%d", index)),
			"name":  toolCall.Name,
			"input": parseJSONStringOrObject(toolCall.Arguments),
		})
	}
	payload := map[string]any{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"model":         agg.Model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"content":       content,
		"usage": map[string]any{
			"input_tokens":  agg.PromptTokens,
			"output_tokens": agg.CompletionTokens,
		},
	}
	return json.Marshal(payload)
}

func writeChatStream(out io.Writer, agg aggregatedChat) error {
	id := agg.ID
	if id == "" {
		id = fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
	}
	if agg.Content != "" || len(agg.ToolCalls) == 0 {
		chunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": agg.Created,
			"model":   agg.Model,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"role":    "assistant",
					"content": agg.Content,
				},
				"finish_reason": nil,
			}},
		}
		if err := writeSSEData(out, chunk); err != nil {
			return err
		}
	}
	for index, toolCall := range agg.ToolCalls {
		chunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": agg.Created,
			"model":   agg.Model,
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": index,
						"id":    nonEmptyString(toolCall.ID, fmt.Sprintf("call_%d", index)),
						"type":  "function",
						"function": map[string]any{
							"name":      toolCall.Name,
							"arguments": toolCall.Arguments,
						},
					}},
				},
				"finish_reason": nil,
			}},
		}
		if err := writeSSEData(out, chunk); err != nil {
			return err
		}
	}
	finishReason := "stop"
	if len(agg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	chunk2 := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": agg.Created,
		"model":   agg.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     agg.PromptTokens,
			"completion_tokens": agg.CompletionTokens,
			"total_tokens":      agg.TotalTokens,
		},
	}
	if err := writeSSEData(out, chunk2); err != nil {
		return err
	}
	_, err := io.WriteString(out, "data: [DONE]\n\n")
	return err
}

func writeResponsesStream(out io.Writer, agg aggregatedChat) error {
	responseID := agg.ID
	if responseID == "" {
		responseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	messageID := "msg_" + responseID

	created := map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": agg.Created,
			"status":     "in_progress",
		},
	}
	delta := map[string]any{
		"type":  "response.output_text.delta",
		"delta": agg.Content,
	}
	completed := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         responseID,
			"object":     "response",
			"created_at": agg.Created,
			"status":     "completed",
			"model":      agg.Model,
			"output": []any{
				map[string]any{
					"id":     messageID,
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []any{
						map[string]any{"type": "output_text", "text": agg.Content},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  agg.PromptTokens,
				"output_tokens": agg.CompletionTokens,
				"total_tokens":  agg.TotalTokens,
			},
		},
	}
	if err := writeSSEData(out, created); err != nil {
		return err
	}
	if agg.Content != "" {
		if err := writeSSEData(out, delta); err != nil {
			return err
		}
	}
	for index, toolCall := range agg.ToolCalls {
		itemID := fmt.Sprintf("fc_%s_%d", responseID, index)
		callID := nonEmptyString(toolCall.ID, fmt.Sprintf("call_%d", index))
		added := map[string]any{
			"type":         "response.output_item.added",
			"output_index": index,
			"item": map[string]any{
				"id":        itemID,
				"type":      "function_call",
				"status":    "in_progress",
				"call_id":   callID,
				"name":      toolCall.Name,
				"arguments": "",
			},
		}
		argsDelta := map[string]any{
			"type":         "response.function_call_arguments.delta",
			"item_id":      itemID,
			"output_index": index,
			"delta":        toolCall.Arguments,
		}
		argsDone := map[string]any{
			"type":         "response.function_call_arguments.done",
			"item_id":      itemID,
			"output_index": index,
			"arguments":    toolCall.Arguments,
		}
		done := map[string]any{
			"type":         "response.output_item.done",
			"output_index": index,
			"item": map[string]any{
				"id":        itemID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   callID,
				"name":      toolCall.Name,
				"arguments": toolCall.Arguments,
			},
		}
		if err := writeSSEData(out, added); err != nil {
			return err
		}
		if toolCall.Arguments != "" {
			if err := writeSSEData(out, argsDelta); err != nil {
				return err
			}
		}
		if err := writeSSEData(out, argsDone); err != nil {
			return err
		}
		if err := writeSSEData(out, done); err != nil {
			return err
		}
	}
	output := make([]any, 0, 1+len(agg.ToolCalls))
	if strings.TrimSpace(agg.Content) != "" || len(agg.ToolCalls) == 0 {
		output = append(output, map[string]any{
			"id":     messageID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{
				map[string]any{"type": "output_text", "text": agg.Content},
			},
		})
	}
	for index, toolCall := range agg.ToolCalls {
		output = append(output, map[string]any{
			"id":        fmt.Sprintf("fc_%s_%d", responseID, index),
			"type":      "function_call",
			"status":    "completed",
			"call_id":   nonEmptyString(toolCall.ID, fmt.Sprintf("call_%d", index)),
			"name":      toolCall.Name,
			"arguments": toolCall.Arguments,
		})
	}
	completedResponse, _ := completed["response"].(map[string]any)
	completedResponse["output"] = output
	if err := writeSSEData(out, completed); err != nil {
		return err
	}
	_, err := io.WriteString(out, "data: [DONE]\n\n")
	return err
}

func writeMessagesStream(out io.Writer, agg aggregatedChat) error {
	messageID := agg.ID
	if messageID == "" {
		messageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}

	messageStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         agg.Model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  agg.PromptTokens,
				"output_tokens": 0,
			},
		},
	}
	stopReason := "end_turn"
	if len(agg.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": agg.CompletionTokens,
		},
	}
	messageStop := map[string]any{"type": "message_stop"}

	if err := writeSSEEventData(out, "message_start", messageStart); err != nil {
		return err
	}
	nextIndex := 0
	if agg.Content != "" || len(agg.ToolCalls) == 0 {
		contentStart := map[string]any{
			"type":  "content_block_start",
			"index": nextIndex,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		}
		contentDelta := map[string]any{
			"type":  "content_block_delta",
			"index": nextIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": agg.Content,
			},
		}
		contentStop := map[string]any{"type": "content_block_stop", "index": nextIndex}
		if err := writeSSEEventData(out, "content_block_start", contentStart); err != nil {
			return err
		}
		if err := writeSSEEventData(out, "content_block_delta", contentDelta); err != nil {
			return err
		}
		if err := writeSSEEventData(out, "content_block_stop", contentStop); err != nil {
			return err
		}
		nextIndex++
	}
	for _, toolCall := range agg.ToolCalls {
		contentStart := map[string]any{
			"type":  "content_block_start",
			"index": nextIndex,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    nonEmptyString(toolCall.ID, fmt.Sprintf("toolu_%d", nextIndex)),
				"name":  toolCall.Name,
				"input": map[string]any{},
			},
		}
		contentDelta := map[string]any{
			"type":  "content_block_delta",
			"index": nextIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": toolCall.Arguments,
			},
		}
		contentStop := map[string]any{"type": "content_block_stop", "index": nextIndex}
		if err := writeSSEEventData(out, "content_block_start", contentStart); err != nil {
			return err
		}
		if toolCall.Arguments != "" {
			if err := writeSSEEventData(out, "content_block_delta", contentDelta); err != nil {
				return err
			}
		}
		if err := writeSSEEventData(out, "content_block_stop", contentStop); err != nil {
			return err
		}
		nextIndex++
	}
	if err := writeSSEEventData(out, "message_delta", messageDelta); err != nil {
		return err
	}
	return writeSSEEventData(out, "message_stop", messageStop)
}

func writeSSEData(out io.Writer, payload map[string]any) error {
	content, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, "data: "+string(content)+"\n\n")
	return err
}

func writeSSEEventData(out io.Writer, event string, payload map[string]any) error {
	content, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, "event: "+event+"\n"+"data: "+string(content)+"\n\n")
	return err
}

func buildChatToolCallPayloads(toolCalls []aggregatedToolCall) []any {
	payloads := make([]any, 0, len(toolCalls))
	for index, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.Name) == "" {
			continue
		}
		payloads = append(payloads, map[string]any{
			"id":   nonEmptyString(toolCall.ID, fmt.Sprintf("call_%d", index)),
			"type": "function",
			"function": map[string]any{
				"name":      toolCall.Name,
				"arguments": toolCall.Arguments,
			},
		})
	}
	return payloads
}

func extractChatToolCalls(raw any) []aggregatedToolCall {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	toolCalls := make([]aggregatedToolCall, 0, len(items))
	for _, itemRaw := range items {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		function, ok := item["function"].(map[string]any)
		if !ok {
			continue
		}
		call := aggregatedToolCall{
			ID:        strings.TrimSpace(toString(item["id"])),
			Name:      strings.TrimSpace(toString(function["name"])),
			Arguments: toString(function["arguments"]),
		}
		if call.Name != "" {
			toolCalls = append(toolCalls, call)
		}
	}
	return toolCalls
}

func mergeChatToolCallDeltas(partials map[int]*aggregatedToolCall, raw any) {
	items, ok := raw.([]any)
	if !ok {
		return
	}
	for _, itemRaw := range items {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		index := int(toInt64(item["index"]))
		call := ensureAggregatedToolCallPartial(partials, index)
		if id := strings.TrimSpace(toString(item["id"])); id != "" {
			call.ID = id
		}
		if function, ok := item["function"].(map[string]any); ok {
			if name := toString(function["name"]); name != "" {
				call.Name += name
			}
			if arguments := toString(function["arguments"]); arguments != "" {
				call.Arguments += arguments
			}
		}
	}
}

func extractResponsesToolCall(item map[string]any) aggregatedToolCall {
	return aggregatedToolCall{
		ID:        nonEmptyString(strings.TrimSpace(toString(item["call_id"])), strings.TrimSpace(toString(item["id"]))),
		Name:      strings.TrimSpace(toString(item["name"])),
		Arguments: toString(item["arguments"]),
	}
}

func mergeResponsesToolCallItem(partials map[int]*aggregatedToolCall, indexByItemID map[string]int, index int, item map[string]any) {
	itemID := strings.TrimSpace(toString(item["id"]))
	if itemID != "" {
		indexByItemID[itemID] = index
	}
	incoming := extractResponsesToolCall(item)
	call := ensureAggregatedToolCallPartial(partials, index)
	if incoming.ID != "" {
		call.ID = incoming.ID
	}
	if incoming.Name != "" {
		call.Name = incoming.Name
	}
	if incoming.Arguments != "" {
		call.Arguments = incoming.Arguments
	}
}

func extractMessagesToolCall(block map[string]any) aggregatedToolCall {
	return aggregatedToolCall{
		ID:        strings.TrimSpace(toString(block["id"])),
		Name:      strings.TrimSpace(toString(block["name"])),
		Arguments: normalizeToolCallArguments(block["input"]),
	}
}

func mergeMessagesToolUseBlock(partials map[int]*aggregatedToolCall, index int, block map[string]any) {
	incoming := extractMessagesToolCall(block)
	call := ensureAggregatedToolCallPartial(partials, index)
	if incoming.ID != "" {
		call.ID = incoming.ID
	}
	if incoming.Name != "" {
		call.Name = incoming.Name
	}
	if incoming.Arguments != "" && incoming.Arguments != "{}" {
		call.Arguments = incoming.Arguments
	}
}

func ensureAggregatedToolCallPartial(partials map[int]*aggregatedToolCall, index int) *aggregatedToolCall {
	call := partials[index]
	if call == nil {
		call = &aggregatedToolCall{}
		partials[index] = call
	}
	return call
}

func orderedToolCallPartials(partials map[int]*aggregatedToolCall) []aggregatedToolCall {
	if len(partials) == 0 {
		return nil
	}
	maxIndex := 0
	for index := range partials {
		if index > maxIndex {
			maxIndex = index
		}
	}
	toolCalls := make([]aggregatedToolCall, 0, len(partials))
	for index := 0; index <= maxIndex; index++ {
		call := partials[index]
		if call == nil || strings.TrimSpace(call.Name) == "" {
			continue
		}
		if strings.TrimSpace(call.ID) == "" {
			call.ID = fmt.Sprintf("call_%d", index)
		}
		toolCalls = append(toolCalls, *call)
	}
	return toolCalls
}

func normalizeToolCallArguments(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return mustJSONString(v)
	}
}

func nonEmptyString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func extractMessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, raw := range v {
			switch item := raw.(type) {
			case string:
				if strings.TrimSpace(item) != "" {
					parts = append(parts, item)
				}
			case map[string]any:
				text := strings.TrimSpace(toString(item["text"]))
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int8:
		return int64(n)
	case int16:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case uint:
		return int64(n)
	case uint8:
		return int64(n)
	case uint16:
		return int64(n)
	case uint32:
		return int64(n)
	case uint64:
		return int64(n)
	case float32:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return int64(f)
		}
		return 0
	default:
		return 0
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
