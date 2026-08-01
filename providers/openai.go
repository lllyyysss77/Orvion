package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/racio/orvion/consts"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// modelsListTimeout 列模型接口给一个相对较短的超时,避免某些上游长挂。
const modelsListTimeout = 30 * time.Second

type OpenAI struct {
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	ProxyURL string `json:"-"`
}

func (o *OpenAI) SetProxyURL(proxyURL string) {
	o.ProxyURL = strings.TrimSpace(proxyURL)
}

func (o *OpenAI) BuildReq(ctx context.Context, header http.Header, model string, rawBody []byte) (*http.Request, error) {
	body, err := sjson.SetBytes(rawBody, "model", model)
	if err != nil {
		return nil, err
	}

	endpoint, _ := ctx.Value(consts.ContextKeyOpenAIEndpoint).(string)
	path := "chat/completions"
	normalizedEndpoint := strings.ToLower(strings.TrimSpace(endpoint))
	if normalizedEndpoint == "embeddings" {
		path = "embeddings"
		// 兼容部分上游（如 ModelScope）要求显式传 encoding_format。
		if !gjson.GetBytes(body, "encoding_format").Exists() {
			body, err = sjson.SetBytes(body, "encoding_format", "float")
			if err != nil {
				return nil, err
			}
		}
	} else if normalizedEndpoint != "" {
		// 允许通过上下文覆盖 OpenAI 目标路径，如 images/generations、images/edits
		path = normalizedEndpoint
	}
	base := strings.TrimRight(o.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/%s", base, path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if header != nil {
		req.Header = header
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", nextProviderAPIKey(o.BaseURL, o.APIKey)))

	return req, nil
}

func (o *OpenAI) Models(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsListTimeout)
	defer cancel()

	models, err := o.models(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(models) > 0 || strings.TrimSpace(o.APIKey) == "" {
		return models, nil
	}
	fallbackModels, err := o.models(ctx, false)
	if err != nil {
		return models, nil
	}
	return fallbackModels, nil
}

func (o *OpenAI) models(ctx context.Context, withAPIKey bool) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/models", o.BaseURL), nil)
	if err != nil {
		return nil, err
	}
	if withAPIKey {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", nextProviderAPIKey(o.BaseURL, o.APIKey)))
	}
	res, err := GetClientWithProxy(modelsListTimeout, o.ProxyURL)
	if err != nil {
		return nil, err
	}
	resp, err := res.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var modelList ModelList
	if err := json.NewDecoder(resp.Body).Decode(&modelList); err != nil {
		return nil, err
	}
	return modelList.Data, nil
}
