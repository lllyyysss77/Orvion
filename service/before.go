package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type Before struct {
	Model            string
	Stream           bool
	toolCall         bool
	structuredOutput bool
	image            bool
	raw              []byte
}

type Beforer func(data []byte) (*Before, error)

// BeforerOpenAIMedia 仅用于图像/视频类接口，要求 model 字段存在。
// 这些接口不使用 stream、tools 等字段，直接透传原始请求体。
func BeforerOpenAIMedia(data []byte) (*Before, error) {
	model := gjson.GetBytes(data, "model").String()
	if model == "" {
		return nil, errors.New("model is empty")
	}
	stream := gjson.GetBytes(data, "stream").Bool()
	return &Before{
		Model:  model,
		Stream: stream,
		raw:    data,
	}, nil
}

func BeforerOpenAI(data []byte) (*Before, error) {
	model := gjson.GetBytes(data, "model").String()
	if model == "" {
		return nil, errors.New("model is empty")
	}
	// Gemini 兼容层（可通过 GEMINI_COMPAT_ENABLED 开关）：
	// 1) 移除 patternProperties
	// 2) 降级 tool/function 历史，规避部分网关的 function_response.name 空值错误
	if strings.HasPrefix(strings.ToLower(model), "gemini") && isGeminiCompatEnabled() {
		next, err := removePatternPropertiesDeep(data)
		if err != nil {
			return nil, err
		}
		next, err = normalizeGeminiToolHistory(next)
		if err != nil {
			return nil, err
		}
		data = next
	}
	stream := gjson.GetBytes(data, "stream").Bool()
	if stream {
		// 为processTee记录usage添加选项 PS:很多客户端只会开启stream 而不会开启include_usage
		newData, err := sjson.SetBytes(data, "stream_options", struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true})
		if err != nil {
			return nil, err
		}
		data = newData
	}
	var toolCall bool
	tools := gjson.GetBytes(data, "tools")
	if tools.Exists() && len(tools.Array()) != 0 {
		toolCall = true
	}
	var structuredOutput bool
	if gjson.GetBytes(data, "response_format").Exists() {
		structuredOutput = true
	}
	var image bool
	gjson.GetBytes(data, "messages").ForEach(func(_, value gjson.Result) bool {
		if image {
			return false
		}
		if value.Get("role").String() == "user" {
			value.Get("content").ForEach(func(_, value gjson.Result) bool {
				if value.Get("type").String() == "image_url" {
					image = true
					return false
				}
				return true
			})
		}
		return true
	})
	return &Before{
		Model:            model,
		Stream:           stream,
		toolCall:         toolCall,
		structuredOutput: structuredOutput,
		image:            image,
		raw:              data,
	}, nil
}

func isGeminiCompatEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("GEMINI_COMPAT_ENABLED")))
	switch raw {
	case "", "1", "true", "on", "yes", "y":
		return true
	case "0", "false", "off", "no", "n":
		return false
	default:
		// 非法值时默认开启，避免兼容回退。
		return true
	}
}

func removePatternPropertiesDeep(data []byte) ([]byte, error) {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	prunePatternProperties(&payload)
	return json.Marshal(payload)
}

func prunePatternProperties(node *any) {
	switch v := (*node).(type) {
	case map[string]any:
		delete(v, "patternProperties")
		for key := range v {
			child := v[key]
			prunePatternProperties(&child)
			v[key] = child
		}
	case []any:
		for i := range v {
			child := v[i]
			prunePatternProperties(&child)
			v[i] = child
		}
	}
}

// normalizeGeminiToolHistory 将 OpenAI 工具历史降级为普通文本消息，规避部分 Gemini 网关
// 在 function_response 映射阶段丢失 name 导致的 400 错误。
func normalizeGeminiToolHistory(data []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}

	rawMessages, ok := payload["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return data, nil
	}

	normalized := make([]any, 0, len(rawMessages))
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			normalized = append(normalized, raw)
			continue
		}

		role := strings.ToLower(strings.TrimSpace(asString(msg["role"])))
		switch role {
		case "tool", "function":
			name := strings.TrimSpace(asString(msg["name"]))
			toolCallID := strings.TrimSpace(asString(msg["tool_call_id"]))
			contentText := normalizeContentToText(msg["content"])

			prefix := "工具返回"
			if name != "" {
				prefix += fmt.Sprintf("(%s)", name)
			}
			if toolCallID != "" {
				prefix += fmt.Sprintf("[tool_call_id=%s]", toolCallID)
			}
			normalized = append(normalized, map[string]any{
				"role":    "user",
				"content": fmt.Sprintf("%s: %s", prefix, contentText),
			})
			continue
		}

		normalized = append(normalized, msg)
	}

	payload["messages"] = normalized
	return json.Marshal(payload)
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func normalizeContentToText(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func BeforerOpenAIRes(data []byte) (*Before, error) {
	model := gjson.GetBytes(data, "model").String()
	if model == "" {
		return nil, errors.New("model is empty")
	}
	stream := gjson.GetBytes(data, "stream").Bool()
	var toolCall bool
	tools := gjson.GetBytes(data, "tools")
	if tools.Exists() && len(tools.Array()) != 0 {
		toolCall = true
	}
	var structuredOutput bool
	if gjson.GetBytes(data, "text.format.type").String() == "json_schema" {
		structuredOutput = true
	}
	var image bool
	gjson.GetBytes(data, "input").ForEach(func(_, value gjson.Result) bool {
		if image {
			return false
		}
		if value.Get("role").String() == "user" {
			value.Get("content").ForEach(func(_, value gjson.Result) bool {
				if value.Get("type").String() == "input_image" {
					image = true
					return false
				}
				return true
			})
		}
		return true
	})
	return &Before{
		Model:            model,
		Stream:           stream,
		toolCall:         toolCall,
		structuredOutput: structuredOutput,
		image:            image,
		raw:              data,
	}, nil
}

func BeforerAnthropic(data []byte) (*Before, error) {
	model := gjson.GetBytes(data, "model").String()
	if model == "" {
		return nil, errors.New("model is empty")
	}
	stream := gjson.GetBytes(data, "stream").Bool()
	var toolCall bool
	tools := gjson.GetBytes(data, "tools")
	if tools.Exists() && len(tools.Array()) != 0 {
		toolCall = true
	}
	var image bool
	gjson.GetBytes(data, "messages").ForEach(func(_, value gjson.Result) bool {
		if image {
			return false
		}
		if value.Get("role").String() == "user" {
			value.Get("content").ForEach(func(_, value gjson.Result) bool {
				if value.Get("type").String() == "image" {
					image = true
					return false
				}
				return true
			})
		}
		return true
	})
	return &Before{
		Model:            model,
		Stream:           stream,
		toolCall:         toolCall,
		structuredOutput: toolCall,
		image:            image,
		raw:              data,
	}, nil
}
