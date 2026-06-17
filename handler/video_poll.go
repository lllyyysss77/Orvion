package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service"
	"github.com/tidwall/gjson"
)

var (
	videoTaskPollInterval = 3 * time.Second
	videoTaskPollTimeout  = 10 * time.Minute
)

func resolveVideoPollProxyURL(meta *service.ProvidersWithMeta, log *models.ChatLog) string {
	if meta == nil || log == nil {
		return ""
	}
	modelWithProvider, ok := meta.ModelWithProviderMap[log.ModelWithProviderID]
	if !ok {
		return ""
	}
	provider, ok := meta.ProviderMap[modelWithProvider.ProviderID]
	if !ok {
		return ""
	}
	return strings.TrimSpace(provider.ProxyURL)
}

func waitForOpenAIVideoCompletion(ctx context.Context, upstreamReq *http.Request, initialBody []byte, proxyURL string) ([]byte, error) {
	if !shouldPollOpenAIVideoTask(initialBody) {
		return initialBody, nil
	}
	if upstreamReq == nil || upstreamReq.URL == nil {
		return initialBody, errors.New("missing upstream request for video polling")
	}

	taskID := strings.TrimSpace(firstNonEmptyJSON(initialBody, "task_id", "id"))
	videoID := strings.TrimSpace(firstNonEmptyJSON(initialBody, "video_id"))
	if taskID == "" && videoID == "" {
		return initialBody, errors.New("missing task_id/video_id for video polling")
	}

	client, err := providers.GetClientWithProxy(45*time.Second, proxyURL)
	if err != nil {
		return initialBody, err
	}

	pollCtx := ctx
	if pollCtx == nil {
		pollCtx = context.Background()
	}
	pollCtx, cancel := context.WithTimeout(pollCtx, videoTaskPollTimeout)
	defer cancel()

	baseURL := *upstreamReq.URL
	baseURL.RawQuery = ""
	pollURLs := buildOpenAIVideoPollURLs(baseURL, taskID, videoID)
	if len(pollURLs) == 0 {
		return initialBody, errors.New("no candidate video poll url")
	}

	latestBody := append([]byte(nil), initialBody...)
	ticker := time.NewTicker(videoTaskPollInterval)
	defer ticker.Stop()

	for {
		for _, pollURL := range pollURLs {
			body, statusCode, pollErr := fetchOpenAIVideoPollOnce(pollCtx, client, upstreamReq, pollURL)
			if pollErr != nil {
				continue
			}
			if statusCode < 200 || statusCode >= 300 || len(body) == 0 {
				continue
			}
			latestBody = body
			if isOpenAIVideoTaskCompleted(body) {
				return latestBody, nil
			}
			if isOpenAIVideoTaskFailed(body) {
				return latestBody, fmt.Errorf("video task failed: %s", strings.TrimSpace(gjson.GetBytes(body, "status").String()))
			}
		}

		select {
		case <-pollCtx.Done():
			return latestBody, pollCtx.Err()
		case <-ticker.C:
		}
	}
}

func shouldPollOpenAIVideoTask(body []byte) bool {
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	switch status {
	case "queued", "pending", "in_progress", "processing", "running":
		return true
	default:
		return false
	}
}

func isOpenAIVideoTaskCompleted(body []byte) bool {
	if url := strings.TrimSpace(firstNonEmptyJSON(body, "data.0.url", "url", "output.0.url", "result.url")); url != "" {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	switch status {
	case "completed", "succeeded", "success", "done", "finished":
		return true
	default:
		return false
	}
}

func isOpenAIVideoTaskFailed(body []byte) bool {
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	switch status {
	case "failed", "error", "cancelled", "canceled", "expired":
		return true
	default:
		return false
	}
}

func buildOpenAIVideoPollURLs(base url.URL, taskID string, videoID string) []string {
	path := strings.TrimRight(base.Path, "/")
	paths := make([]string, 0, 4)
	if taskID != "" {
		paths = append(paths,
			path+"/"+taskID,
			path+"/"+taskID+"/status",
		)
	}
	if videoID != "" {
		paths = append(paths,
			path+"/"+videoID,
			path+"/"+videoID+"/status",
		)
	}

	seen := make(map[string]struct{}, len(paths))
	urls := make([]string, 0, len(paths))
	for _, item := range paths {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		next := base
		next.Path = item
		key := next.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		urls = append(urls, key)
	}
	return urls
}

func fetchOpenAIVideoPollOnce(ctx context.Context, client *http.Client, upstreamReq *http.Request, pollURL string) ([]byte, int, error) {
	if client == nil {
		return nil, 0, errors.New("http client is nil")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
	if err != nil {
		return nil, 0, err
	}
	for key, values := range upstreamReq.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Del("Content-Length")
	req.Header.Del("Content-Type")
	req.Header.Set("Accept", "application/json")

	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return nil, res.StatusCode, readErr
	}
	return body, res.StatusCode, nil
}

func firstNonEmptyJSON(body []byte, paths ...string) string {
	for _, path := range paths {
		value := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if value != "" {
			return value
		}
	}
	return ""
}
