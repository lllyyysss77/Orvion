package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/racio/orvion/consts"
)

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"` // 使用 int64 存储 Unix 时间戳
	OwnedBy string `json:"owned_by"`
}

type Provider interface {
	BuildReq(ctx context.Context, header http.Header, model string, rawData []byte) (*http.Request, error)
	Models(ctx context.Context) ([]Model, error)
}

func New(Type, providerConfig string) (Provider, error) {
	switch Type {
	case consts.StyleOpenAI:
		var openai OpenAI
		if err := json.Unmarshal([]byte(providerConfig), &openai); err != nil {
			return nil, errors.New("invalid openai config")
		}

		return &openai, nil
	case consts.StyleOpenAIRes:
		var openaiRes OpenAIRes
		if err := json.Unmarshal([]byte(providerConfig), &openaiRes); err != nil {
			return nil, errors.New("invalid codex config")
		}

		return &openaiRes, nil
	case consts.StyleCodexAuths:
		// codex-auths 与 codex 复用同一 Responses 协议，仅凭据来源不同。
		var openaiRes OpenAIRes
		if err := json.Unmarshal([]byte(providerConfig), &openaiRes); err != nil {
			return nil, errors.New("invalid codex-auths config")
		}

		return &openaiRes, nil
	case consts.StyleIFlow:
		// iflow 与 openai 复用 ChatCompletions 协议，凭据由订阅池自动注入。
		var openai OpenAI
		if err := json.Unmarshal([]byte(providerConfig), &openai); err != nil {
			return nil, errors.New("invalid iflow config")
		}
		return &openai, nil
	case consts.StyleIFlowAuths:
		// iflow-auths 与 openai 复用 ChatCompletions 协议，仅凭据来源为 auth_files。
		var openai OpenAI
		if err := json.Unmarshal([]byte(providerConfig), &openai); err != nil {
			return nil, errors.New("invalid iflow-auths config")
		}
		return &openai, nil
	case consts.StyleAnthropic:
		var anthropic Anthropic
		if err := json.Unmarshal([]byte(providerConfig), &anthropic); err != nil {
			return nil, errors.New("invalid anthropic config")
		}
		return &anthropic, nil
	case consts.StyleGemini:
		var gemini Gemini
		if err := json.Unmarshal([]byte(providerConfig), &gemini); err != nil {
			return nil, errors.New("invalid gemini config")
		}
		return &gemini, nil
	default:
		return nil, errors.New("unknown provider")
	}
}
