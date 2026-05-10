package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/racio/orvion/consts"
	"github.com/tidwall/sjson"
)

// openai responses api
type OpenAIRes struct {
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	ProxyURL string `json:"-"`
}

func (o *OpenAIRes) SetProxyURL(proxyURL string) {
	o.ProxyURL = strings.TrimSpace(proxyURL)
}

func (o *OpenAIRes) BuildReq(ctx context.Context, header http.Header, model string, rawBody []byte) (*http.Request, error) {
	body, err := sjson.SetBytes(rawBody, "model", model)
	if err != nil {
		return nil, err
	}

	path := "responses"
	if endpoint, ok := ctx.Value(consts.ContextKeyOpenAIEndpoint).(string); ok {
		normalizedEndpoint := strings.ToLower(strings.TrimSpace(endpoint))
		if normalizedEndpoint != "" {
			path = normalizedEndpoint
		}
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
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.APIKey))

	return req, nil
}

func (o *OpenAIRes) Models(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/models", o.BaseURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.APIKey))
	client, err := GetClientWithProxy(modelsListTimeout, o.ProxyURL)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", res.StatusCode)
	}

	var modelList ModelList
	if err := json.NewDecoder(res.Body).Decode(&modelList); err != nil {
		return nil, err
	}
	return modelList.Data, nil
}
