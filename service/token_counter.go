package service

import (
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

type modelTokenizer struct {
	codec            tokenizer.Codec
	adjustmentFactor float64
}

var modelTokenizerCache sync.Map

func countTokensWithModel(model string, text string) (int64, error) {
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		return 0, nil
	}

	tk, err := getModelTokenizer(model)
	if err != nil {
		return 0, err
	}

	count, err := tk.codec.Count(trimmedText)
	if err != nil {
		return 0, err
	}
	if tk.adjustmentFactor > 0 && tk.adjustmentFactor != 1.0 {
		count = int(float64(count) * tk.adjustmentFactor)
	}
	if count < 0 {
		count = 0
	}
	return int64(count), nil
}

func getModelTokenizer(model string) (*modelTokenizer, error) {
	sanitized := strings.ToLower(strings.TrimSpace(model))
	if cached, ok := modelTokenizerCache.Load(sanitized); ok {
		return cached.(*modelTokenizer), nil
	}

	tk, err := buildModelTokenizer(sanitized)
	if err != nil {
		return nil, err
	}

	actual, _ := modelTokenizerCache.LoadOrStore(sanitized, tk)
	return actual.(*modelTokenizer), nil
}

func buildModelTokenizer(model string) (*modelTokenizer, error) {
	if strings.Contains(model, "claude") || strings.HasPrefix(model, "kiro-") || strings.HasPrefix(model, "amazonq-") {
		enc, err := tokenizer.Get(tokenizer.Cl100kBase)
		if err != nil {
			return nil, err
		}
		return &modelTokenizer{
			codec:            enc,
			adjustmentFactor: 1.1,
		}, nil
	}

	var (
		enc tokenizer.Codec
		err error
	)

	switch {
	case model == "":
		enc, err = tokenizer.Get(tokenizer.Cl100kBase)
	case strings.HasPrefix(model, "gpt-5.2"):
		enc, err = tokenizer.ForModel(tokenizer.GPT5)
	case strings.HasPrefix(model, "gpt-5.1"):
		enc, err = tokenizer.ForModel(tokenizer.GPT5)
	case strings.HasPrefix(model, "gpt-5"):
		enc, err = tokenizer.ForModel(tokenizer.GPT5)
	case strings.HasPrefix(model, "gpt-4.1"):
		enc, err = tokenizer.ForModel(tokenizer.GPT41)
	case strings.HasPrefix(model, "gpt-4o"):
		enc, err = tokenizer.ForModel(tokenizer.GPT4o)
	case strings.HasPrefix(model, "gpt-4"):
		enc, err = tokenizer.ForModel(tokenizer.GPT4)
	case strings.HasPrefix(model, "gpt-3.5"), strings.HasPrefix(model, "gpt-3"):
		enc, err = tokenizer.ForModel(tokenizer.GPT35Turbo)
	case strings.HasPrefix(model, "o1"):
		enc, err = tokenizer.ForModel(tokenizer.O1)
	case strings.HasPrefix(model, "o3"):
		enc, err = tokenizer.ForModel(tokenizer.O3)
	case strings.HasPrefix(model, "o4"):
		enc, err = tokenizer.ForModel(tokenizer.O4Mini)
	default:
		enc, err = tokenizer.Get(tokenizer.O200kBase)
	}
	if err != nil {
		return nil, err
	}

	return &modelTokenizer{
		codec:            enc,
		adjustmentFactor: 1.0,
	}, nil
}
