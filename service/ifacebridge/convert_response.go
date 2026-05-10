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
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
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
			if strings.ToLower(strings.TrimSpace(toString(item["type"]))) != "message" {
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
			if strings.ToLower(strings.TrimSpace(toString(part["type"]))) == "text" {
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
	}
	if err := scanner.Err(); err != nil {
		return aggregatedChat{}, err
	}
	if agg.TotalTokens == 0 {
		agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	}
	agg.Content = contentBuilder.String()
	return agg, nil
}

func aggregateResponsesFromStream(upstream io.Reader) (aggregatedChat, error) {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 0, 8192), 64*1024*1024)
	agg := aggregatedChat{Created: time.Now().Unix()}
	var contentBuilder strings.Builder

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
				if contentBuilder.Len() == 0 {
					if output, ok := response["output"].([]any); ok {
						for _, rawItem := range output {
							item, ok := rawItem.(map[string]any)
							if !ok || strings.ToLower(strings.TrimSpace(toString(item["type"]))) != "message" {
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
	}
	if err := scanner.Err(); err != nil {
		return aggregatedChat{}, err
	}
	agg.Content = contentBuilder.String()
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
		case "content_block_delta":
			if delta, ok := chunk["delta"].(map[string]any); ok {
				if strings.ToLower(strings.TrimSpace(toString(delta["type"]))) == "text_delta" {
					contentBuilder.WriteString(toString(delta["text"]))
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
	agg.TotalTokens = agg.PromptTokens + agg.CompletionTokens
	return agg, nil
}

func buildChatNonStreamPayload(agg aggregatedChat) ([]byte, error) {
	id := agg.ID
	if id == "" {
		id = fmt.Sprintf("chatcmpl_%d", time.Now().UnixNano())
	}
	payload := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": agg.Created,
		"model":   agg.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": "stop",
			"message": map[string]any{
				"role":    "assistant",
				"content": agg.Content,
			},
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

	payload := map[string]any{
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
					map[string]any{
						"type": "output_text",
						"text": agg.Content,
					},
				},
			},
		},
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
	payload := map[string]any{
		"id":            messageID,
		"type":          "message",
		"role":          "assistant",
		"model":         agg.Model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"content": []any{
			map[string]any{
				"type": "text",
				"text": agg.Content,
			},
		},
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
	chunk1 := map[string]any{
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
	chunk2 := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": agg.Created,
		"model":   agg.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     agg.PromptTokens,
			"completion_tokens": agg.CompletionTokens,
			"total_tokens":      agg.TotalTokens,
		},
	}
	if err := writeSSEData(out, chunk1); err != nil {
		return err
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
	contentStart := map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}
	contentDelta := map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type": "text_delta",
			"text": agg.Content,
		},
	}
	contentStop := map[string]any{"type": "content_block_stop", "index": 0}
	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
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
	if err := writeSSEEventData(out, "content_block_start", contentStart); err != nil {
		return err
	}
	if agg.Content != "" {
		if err := writeSSEEventData(out, "content_block_delta", contentDelta); err != nil {
			return err
		}
	}
	if err := writeSSEEventData(out, "content_block_stop", contentStop); err != nil {
		return err
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
