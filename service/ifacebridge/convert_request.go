package ifacebridge

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func ConvertRequestBody(plan Plan, raw []byte) ([]byte, error) {
	if !plan.Enabled {
		return raw, nil
	}
	from := NormalizeEndpoint(plan.ClientEndpoint)
	to := NormalizeEndpoint(plan.UpstreamEndpoint)
	if !SupportsConversion(from, to) {
		return nil, fmt.Errorf("unsupported request conversion: %s -> %s", from, to)
	}

	switch {
	case from == EndpointResponses && to == EndpointChat:
		return convertResponsesToChat(raw)
	case from == EndpointMessages && to == EndpointChat:
		return convertMessagesToChat(raw)
	case from == EndpointChat && to == EndpointResponses:
		return convertChatToResponses(raw)
	case from == EndpointChat && to == EndpointMessages:
		return convertChatToMessages(raw)
	case from == EndpointResponses && to == EndpointMessages:
		chatRaw, err := convertResponsesToChat(raw)
		if err != nil {
			return nil, err
		}
		return convertChatToMessages(chatRaw)
	case from == EndpointMessages && to == EndpointResponses:
		chatRaw, err := convertMessagesToChat(raw)
		if err != nil {
			return nil, err
		}
		return convertChatToResponses(chatRaw)
	default:
		return nil, fmt.Errorf("unsupported request conversion: %s -> %s", from, to)
	}
}

func convertResponsesToChat(raw []byte) ([]byte, error) {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := map[string]any{}
	copyIfExists(in, out, "model")
	copyIfExists(in, out, "stream")
	copyIfExists(in, out, "temperature")
	copyIfExists(in, out, "top_p")
	copyIfExists(in, out, "presence_penalty")
	copyIfExists(in, out, "frequency_penalty")
	copyIfExists(in, out, "parallel_tool_calls")
	copyIfExists(in, out, "tool_choice")
	copyIfExists(in, out, "metadata")
	if maxOutputTokens, ok := in["max_output_tokens"]; ok {
		out["max_tokens"] = maxOutputTokens
	}
	if reasoning, ok := in["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"]; ok {
			out["reasoning_effort"] = effort
		}
	}

	messages := make([]any, 0)
	if instructions := strings.TrimSpace(toString(in["instructions"])); instructions != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": instructions,
		})
	}

	if input, ok := in["input"]; ok {
		switch v := input.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				messages = append(messages, map[string]any{"role": "user", "content": v})
			}
		case []any:
			for _, item := range v {
				converted, err := convertResponsesInputItem(item)
				if err != nil {
					return nil, err
				}
				if len(converted) == 0 {
					continue
				}
				messages = append(messages, converted...)
			}
		}
	}
	if len(messages) == 0 {
		return nil, errors.New("input is empty after conversion")
	}
	out["messages"] = messages

	if tools, ok := in["tools"].([]any); ok {
		convertedTools := make([]any, 0, len(tools))
		for _, toolRaw := range tools {
			tool, ok := toolRaw.(map[string]any)
			if !ok {
				continue
			}
			toolType := strings.TrimSpace(toString(tool["type"]))
			if toolType != "" && toolType != "function" {
				continue
			}
			function := map[string]any{}
			copyIfExists(tool, function, "name")
			copyIfExists(tool, function, "description")
			if params, ok := tool["parameters"]; ok {
				function["parameters"] = params
			}
			if len(function) == 0 {
				continue
			}
			convertedTools = append(convertedTools, map[string]any{
				"type":     "function",
				"function": function,
			})
		}
		if len(convertedTools) > 0 {
			out["tools"] = convertedTools
		}
	}

	return json.Marshal(out)
}

func convertChatToResponses(raw []byte) ([]byte, error) {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := map[string]any{}
	copyIfExists(in, out, "model")
	copyIfExists(in, out, "stream")
	copyIfExists(in, out, "temperature")
	copyIfExists(in, out, "top_p")
	copyIfExists(in, out, "presence_penalty")
	copyIfExists(in, out, "frequency_penalty")
	copyIfExists(in, out, "parallel_tool_calls")
	copyIfExists(in, out, "tool_choice")
	copyIfExists(in, out, "metadata")
	if maxTokens, ok := in["max_tokens"]; ok {
		out["max_output_tokens"] = maxTokens
	}
	if effort := strings.TrimSpace(toString(in["reasoning_effort"])); effort != "" {
		out["reasoning"] = map[string]any{"effort": effort}
	}

	rawMessages, ok := in["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return nil, errors.New("messages is empty")
	}

	input := make([]any, 0)
	systemTexts := make([]string, 0)
	for _, rawMsg := range rawMessages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(toString(msg["role"])))
		if role == "" {
			continue
		}
		if role == "system" {
			text := extractMessageText(msg["content"])
			if strings.TrimSpace(text) != "" {
				systemTexts = append(systemTexts, text)
			}
			continue
		}
		converted := convertChatMessageToResponsesInput(role, msg)
		input = append(input, converted...)
	}

	if len(systemTexts) > 0 {
		out["instructions"] = strings.Join(systemTexts, "\n")
	}
	if len(input) == 0 {
		return nil, errors.New("messages is empty after conversion")
	}
	out["input"] = input

	if tools, ok := in["tools"].([]any); ok {
		convertedTools := make([]any, 0, len(tools))
		for _, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			if strings.ToLower(strings.TrimSpace(toString(tool["type"]))) != "function" {
				continue
			}
			function, ok := tool["function"].(map[string]any)
			if !ok {
				continue
			}
			next := map[string]any{"type": "function"}
			copyIfExists(function, next, "name")
			copyIfExists(function, next, "description")
			if params, ok := function["parameters"]; ok {
				next["parameters"] = params
			}
			convertedTools = append(convertedTools, next)
		}
		if len(convertedTools) > 0 {
			out["tools"] = convertedTools
		}
	}

	return json.Marshal(out)
}

func convertChatMessageToResponsesInput(role string, msg map[string]any) []any {
	items := make([]any, 0)
	switch role {
	case "tool":
		items = append(items, map[string]any{
			"type":    "function_call_output",
			"call_id": strings.TrimSpace(toString(msg["tool_call_id"])),
			"output":  normalizeToolResultContent(msg["content"]),
		})
		return items
	case "assistant":
		messageItem := map[string]any{
			"type":    "message",
			"role":    "assistant",
			"content": toResponsesContentArray(msg["content"], true),
		}
		items = append(items, messageItem)
		if toolCalls, ok := msg["tool_calls"].([]any); ok {
			for _, rawCall := range toolCalls {
				call, ok := rawCall.(map[string]any)
				if !ok {
					continue
				}
				function, ok := call["function"].(map[string]any)
				if !ok {
					continue
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   strings.TrimSpace(toString(call["id"])),
					"name":      toString(function["name"]),
					"arguments": toString(function["arguments"]),
				})
			}
		}
		return items
	default:
		items = append(items, map[string]any{
			"type":    "message",
			"role":    role,
			"content": toResponsesContentArray(msg["content"], false),
		})
		return items
	}
}

func toResponsesContentArray(content any, assistant bool) []any {
	textType := "input_text"
	if assistant {
		textType = "output_text"
	}
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return []any{}
		}
		return []any{map[string]any{"type": textType, "text": v}}
	case []any:
		parts := make([]any, 0, len(v))
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typeName := strings.ToLower(strings.TrimSpace(toString(part["type"])))
			switch typeName {
			case "text", "input_text", "output_text", "":
				text := toString(part["text"])
				if strings.TrimSpace(text) == "" {
					continue
				}
				parts = append(parts, map[string]any{"type": textType, "text": text})
			case "image_url":
				imageURL := strings.TrimSpace(toString(part["image_url"]))
				if imageMap, ok := part["image_url"].(map[string]any); ok {
					imageURL = strings.TrimSpace(toString(imageMap["url"]))
				}
				if imageURL == "" {
					continue
				}
				parts = append(parts, map[string]any{"type": "input_image", "image_url": imageURL})
			}
		}
		return parts
	default:
		return []any{}
	}
}

func convertResponsesInputItem(item any) ([]any, error) {
	src, ok := item.(map[string]any)
	if !ok {
		return nil, nil
	}

	itemType := strings.TrimSpace(toString(src["type"]))
	if itemType == "" && strings.TrimSpace(toString(src["role"])) != "" {
		itemType = "message"
	}

	switch itemType {
	case "message", "":
		role := strings.ToLower(strings.TrimSpace(toString(src["role"])))
		if role == "" {
			role = "user"
		}
		if role == "developer" {
			role = "system"
		}
		message := map[string]any{"role": role}
		if content, ok := src["content"]; ok {
			switch c := content.(type) {
			case string:
				message["content"] = c
			case []any:
				parts := make([]any, 0, len(c))
				for _, partRaw := range c {
					part, ok := partRaw.(map[string]any)
					if !ok {
						continue
					}
					typeName := strings.TrimSpace(toString(part["type"]))
					switch typeName {
					case "input_text", "output_text", "text", "":
						text := toString(part["text"])
						if strings.TrimSpace(text) == "" {
							continue
						}
						parts = append(parts, map[string]any{"type": "text", "text": text})
					case "input_image", "image":
						imageURL := strings.TrimSpace(toString(part["image_url"]))
						if imageURL == "" {
							continue
						}
						parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
					}
				}
				if len(parts) > 0 {
					message["content"] = parts
				}
			}
		}
		if _, ok := message["content"]; !ok {
			message["content"] = ""
		}
		return []any{message}, nil
	case "function_call":
		toolCall := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":      toString(src["name"]),
				"arguments": toString(src["arguments"]),
			},
		}
		if callID := strings.TrimSpace(toString(src["call_id"])); callID != "" {
			toolCall["id"] = callID
		}
		return []any{map[string]any{"role": "assistant", "tool_calls": []any{toolCall}}}, nil
	case "function_call_output":
		toolMessage := map[string]any{"role": "tool", "content": toString(src["output"])}
		if callID := strings.TrimSpace(toString(src["call_id"])); callID != "" {
			toolMessage["tool_call_id"] = callID
		}
		return []any{toolMessage}, nil
	default:
		return nil, nil
	}
}

func convertMessagesToChat(raw []byte) ([]byte, error) {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}

	out := map[string]any{}
	copyIfExists(in, out, "model")
	copyIfExists(in, out, "stream")
	copyIfExists(in, out, "temperature")
	copyIfExists(in, out, "top_p")
	copyIfExists(in, out, "metadata")
	copyIfExists(in, out, "stop_sequences")
	if maxTokens, ok := in["max_tokens"]; ok {
		out["max_tokens"] = maxTokens
	}

	messages := make([]any, 0)
	systemText := normalizeAnthropicSystem(in["system"])
	if systemText != "" {
		messages = append(messages, map[string]any{"role": "system", "content": systemText})
	}

	rawMessages, ok := in["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return nil, errors.New("messages is empty")
	}
	for _, rawMsg := range rawMessages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(toString(msg["role"])))
		if role == "" {
			continue
		}
		converted, err := convertAnthropicMessage(role, msg["content"])
		if err != nil {
			return nil, err
		}
		messages = append(messages, converted...)
	}
	if len(messages) == 0 {
		return nil, errors.New("messages is empty after conversion")
	}
	out["messages"] = messages

	if tools, ok := in["tools"].([]any); ok {
		convertedTools := make([]any, 0, len(tools))
		for _, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			function := map[string]any{}
			copyIfExists(tool, function, "name")
			copyIfExists(tool, function, "description")
			if schema, ok := tool["input_schema"]; ok {
				function["parameters"] = schema
			}
			if len(function) == 0 {
				continue
			}
			convertedTools = append(convertedTools, map[string]any{
				"type":     "function",
				"function": function,
			})
		}
		if len(convertedTools) > 0 {
			out["tools"] = convertedTools
		}
	}

	if toolChoice, ok := in["tool_choice"].(map[string]any); ok {
		typeName := strings.ToLower(strings.TrimSpace(toString(toolChoice["type"])))
		switch typeName {
		case "auto":
			out["tool_choice"] = "auto"
		case "any":
			out["tool_choice"] = "required"
		case "tool":
			name := strings.TrimSpace(toString(toolChoice["name"]))
			if name != "" {
				out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": name}}
			}
		}
	}

	return json.Marshal(out)
}

func convertChatToMessages(raw []byte) ([]byte, error) {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}

	out := map[string]any{}
	copyIfExists(in, out, "model")
	copyIfExists(in, out, "stream")
	copyIfExists(in, out, "temperature")
	copyIfExists(in, out, "top_p")
	copyIfExists(in, out, "metadata")
	if maxTokens, ok := in["max_tokens"]; ok {
		out["max_tokens"] = maxTokens
	}

	rawMessages, ok := in["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return nil, errors.New("messages is empty")
	}

	systemBlocks := make([]any, 0)
	messages := make([]any, 0)
	for _, rawMsg := range rawMessages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(toString(msg["role"])))
		if role == "" {
			continue
		}

		if role == "system" {
			text := extractMessageText(msg["content"])
			if strings.TrimSpace(text) != "" {
				systemBlocks = append(systemBlocks, map[string]any{"type": "text", "text": text})
			}
			continue
		}

		if role == "tool" {
			messages = append(messages, map[string]any{
				"role": "user",
				"content": []any{map[string]any{
					"type":        "tool_result",
					"tool_use_id": strings.TrimSpace(toString(msg["tool_call_id"])),
					"content": []any{map[string]any{
						"type": "text",
						"text": normalizeToolResultContent(msg["content"]),
					}},
				}},
			})
			continue
		}

		blocks := toAnthropicContentBlocks(msg["content"])
		if role == "assistant" {
			if toolCalls, ok := msg["tool_calls"].([]any); ok {
				for _, rawCall := range toolCalls {
					call, ok := rawCall.(map[string]any)
					if !ok {
						continue
					}
					function, ok := call["function"].(map[string]any)
					if !ok {
						continue
					}
					blocks = append(blocks, map[string]any{
						"type":  "tool_use",
						"id":    strings.TrimSpace(toString(call["id"])),
						"name":  toString(function["name"]),
						"input": parseJSONStringOrObject(function["arguments"]),
					})
				}
			}
		}

		if len(blocks) == 0 {
			blocks = append(blocks, map[string]any{"type": "text", "text": ""})
		}
		messages = append(messages, map[string]any{"role": role, "content": blocks})
	}

	if len(systemBlocks) > 0 {
		out["system"] = systemBlocks
	}
	if len(messages) == 0 {
		return nil, errors.New("messages is empty after conversion")
	}
	out["messages"] = messages

	if tools, ok := in["tools"].([]any); ok {
		convertedTools := make([]any, 0, len(tools))
		for _, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			if strings.ToLower(strings.TrimSpace(toString(tool["type"]))) != "function" {
				continue
			}
			function, ok := tool["function"].(map[string]any)
			if !ok {
				continue
			}
			next := map[string]any{}
			copyIfExists(function, next, "name")
			copyIfExists(function, next, "description")
			if params, ok := function["parameters"]; ok {
				next["input_schema"] = params
			}
			convertedTools = append(convertedTools, next)
		}
		if len(convertedTools) > 0 {
			out["tools"] = convertedTools
		}
	}

	if toolChoice := in["tool_choice"]; toolChoice != nil {
		switch v := toolChoice.(type) {
		case string:
			s := strings.ToLower(strings.TrimSpace(v))
			switch s {
			case "auto":
				out["tool_choice"] = map[string]any{"type": "auto"}
			case "required":
				out["tool_choice"] = map[string]any{"type": "any"}
			case "none":
				out["tool_choice"] = map[string]any{"type": "auto"}
			}
		case map[string]any:
			if fn, ok := v["function"].(map[string]any); ok {
				name := strings.TrimSpace(toString(fn["name"]))
				if name != "" {
					out["tool_choice"] = map[string]any{"type": "tool", "name": name}
				}
			}
		}
	}

	return json.Marshal(out)
}

func toAnthropicContentBlocks(content any) []any {
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return []any{}
		}
		return []any{map[string]any{"type": "text", "text": v}}
	case []any:
		blocks := make([]any, 0, len(v))
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typeName := strings.ToLower(strings.TrimSpace(toString(part["type"])))
			switch typeName {
			case "text", "":
				text := toString(part["text"])
				if strings.TrimSpace(text) == "" {
					continue
				}
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			case "image_url":
				url := ""
				if imageMap, ok := part["image_url"].(map[string]any); ok {
					url = strings.TrimSpace(toString(imageMap["url"]))
				}
				if url == "" {
					url = strings.TrimSpace(toString(part["image_url"]))
				}
				if url == "" {
					continue
				}
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type": "url",
						"url":  url,
					},
				})
			}
		}
		return blocks
	default:
		return []any{}
	}
}

func parseJSONStringOrObject(v any) any {
	s := strings.TrimSpace(toString(v))
	if s == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err == nil {
		return parsed
	}
	return map[string]any{"raw": s}
}

func normalizeAnthropicSystem(system any) string {
	switch v := system.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, raw := range v {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if strings.ToLower(strings.TrimSpace(toString(block["type"]))) != "text" {
				continue
			}
			text := strings.TrimSpace(toString(block["text"]))
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func convertAnthropicMessage(role string, content any) ([]any, error) {
	switch v := content.(type) {
	case string:
		return []any{map[string]any{"role": role, "content": v}}, nil
	case []any:
		var textParts []any
		var messages []any
		var toolCalls []any

		for _, blockRaw := range v {
			block, ok := blockRaw.(map[string]any)
			if !ok {
				continue
			}
			typeName := strings.ToLower(strings.TrimSpace(toString(block["type"])))
			switch typeName {
			case "text", "":
				text := toString(block["text"])
				if strings.TrimSpace(text) == "" {
					continue
				}
				textParts = append(textParts, map[string]any{"type": "text", "text": text})
			case "image":
				source, ok := block["source"].(map[string]any)
				if !ok {
					continue
				}
				imageURL, ok := anthropicImageToURL(source)
				if !ok {
					continue
				}
				textParts = append(textParts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
			case "tool_use":
				call := map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":      toString(block["name"]),
						"arguments": mustJSONString(block["input"]),
					},
				}
				if id := strings.TrimSpace(toString(block["id"])); id != "" {
					call["id"] = id
				}
				toolCalls = append(toolCalls, call)
			case "tool_result":
				toolMessage := map[string]any{
					"role":    "tool",
					"content": normalizeToolResultContent(block["content"]),
				}
				if toolID := strings.TrimSpace(toString(block["tool_use_id"])); toolID != "" {
					toolMessage["tool_call_id"] = toolID
				}
				messages = append(messages, toolMessage)
			case "thinking":
				thinking := strings.TrimSpace(toString(block["thinking"]))
				if thinking != "" {
					textParts = append(textParts, map[string]any{"type": "text", "text": "[thinking] " + thinking})
				}
			}
		}

		if len(textParts) > 0 || len(toolCalls) > 0 {
			msg := map[string]any{"role": role}
			if len(textParts) > 0 {
				msg["content"] = textParts
			} else {
				msg["content"] = ""
			}
			if len(toolCalls) > 0 {
				msg["tool_calls"] = toolCalls
			}
			messages = append([]any{msg}, messages...)
		}
		return messages, nil
	default:
		return []any{map[string]any{"role": role, "content": ""}}, nil
	}
}

func anthropicImageToURL(source map[string]any) (string, bool) {
	sourceType := strings.ToLower(strings.TrimSpace(toString(source["type"])))
	switch sourceType {
	case "url":
		url := strings.TrimSpace(toString(source["url"]))
		if url == "" {
			return "", false
		}
		return url, true
	case "base64":
		mediaType := strings.TrimSpace(toString(source["media_type"]))
		if mediaType == "" {
			mediaType = "image/png"
		}
		data := strings.TrimSpace(toString(source["data"]))
		if data == "" {
			return "", false
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return "", false
		}
		return fmt.Sprintf("data:%s;base64,%s", mediaType, data), true
	default:
		return "", false
	}
}

func normalizeToolResultContent(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, itemRaw := range v {
			item, ok := itemRaw.(map[string]any)
			if !ok {
				continue
			}
			typeName := strings.ToLower(strings.TrimSpace(toString(item["type"])))
			if typeName == "text" || typeName == "" {
				text := strings.TrimSpace(toString(item["text"]))
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) == 0 {
			return mustJSONString(v)
		}
		return strings.Join(parts, "\n")
	default:
		return mustJSONString(v)
	}
}

func copyIfExists(src map[string]any, dst map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return mustJSONString(t)
	}
}

func mustJSONString(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
