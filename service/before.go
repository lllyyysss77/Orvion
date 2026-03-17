package service

import (
	"encoding/json"
	"errors"
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
	// Gemini 兼容层：Gemini 对 tool schema 中的 patternProperties 支持不稳定，转发前递归移除该字段。
	if strings.HasPrefix(strings.ToLower(model), "gemini") {
		next, err := removePatternPropertiesDeep(data)
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
