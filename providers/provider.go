package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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

type proxyAwareProvider interface {
	SetProxyURL(proxyURL string)
}

type providerConfigBase struct {
	BaseURL string `json:"base_url"`
}

func normalizeStyle(style string) string {
	switch strings.TrimSpace(strings.ToLower(style)) {
	case consts.StyleOpenAIEmbeddings:
		return consts.StyleOpenAI
	case consts.StyleGeminiEmbeddings:
		return consts.StyleGemini
	default:
		return strings.TrimSpace(strings.ToLower(style))
	}
}

func inferStyleFromConfig(providerConfig string) string {
	var cfg providerConfigBase
	if err := json.Unmarshal([]byte(providerConfig), &cfg); err != nil {
		return consts.StyleOpenAI
	}

	baseURL := strings.ToLower(strings.TrimSpace(cfg.BaseURL))
	switch {
	case strings.Contains(baseURL, "anthropic"):
		return consts.StyleAnthropic
	case strings.Contains(baseURL, "generativelanguage.googleapis.com"),
		strings.Contains(baseURL, "googleapis.com/v1beta"),
		strings.Contains(baseURL, "googleapis.com/v1alpha"):
		return consts.StyleGemini
	default:
		return consts.StyleOpenAI
	}
}

func ResolveStyle(preferredStyle string, providerConfig string) string {
	if style := normalizeStyle(preferredStyle); style != "" {
		return style
	}
	return inferStyleFromConfig(providerConfig)
}

func NewForStyleWithProxy(preferredStyle, providerConfig, proxyURL string) (Provider, error) {
	proxyURL = strings.TrimSpace(proxyURL)

	switch ResolveStyle(preferredStyle, providerConfig) {
	case consts.StyleOpenAI:
		var openai OpenAI
		if err := json.Unmarshal([]byte(providerConfig), &openai); err != nil {
			return nil, errors.New("invalid openai config")
		}
		openai.SetProxyURL(proxyURL)
		return &openai, nil
	case consts.StyleOpenAIRes:
		var openaiRes OpenAIRes
		if err := json.Unmarshal([]byte(providerConfig), &openaiRes); err != nil {
			return nil, errors.New("invalid codex config")
		}
		openaiRes.SetProxyURL(proxyURL)
		return &openaiRes, nil
	case consts.StyleAnthropic:
		var anthropic Anthropic
		if err := json.Unmarshal([]byte(providerConfig), &anthropic); err != nil {
			return nil, errors.New("invalid anthropic config")
		}
		anthropic.SetProxyURL(proxyURL)
		return &anthropic, nil
	case consts.StyleGemini:
		var gemini Gemini
		if err := json.Unmarshal([]byte(providerConfig), &gemini); err != nil {
			return nil, errors.New("invalid gemini config")
		}
		gemini.SetProxyURL(proxyURL)
		return &gemini, nil
	default:
		return nil, errors.New("unknown provider")
	}
}

func NewForStyle(preferredStyle, providerConfig string) (Provider, error) {
	return NewForStyleWithProxy(preferredStyle, providerConfig, "")
}

func NewWithProxy(providerConfig, proxyURL string) (Provider, error) {
	return NewForStyleWithProxy("", providerConfig, proxyURL)
}
