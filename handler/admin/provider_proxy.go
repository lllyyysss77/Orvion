package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/providers"
)

const (
	providerProxyTestTimeout       = 60 * time.Second
	providerProxySampleTimeout     = 4 * time.Second
	providerProxyExitLookupTimeout = 6 * time.Second
	providerProxySamplesPerTarget  = 12
	providerProxyEventBuffer       = 32
	providerProxyTestUserAgent     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
)

type ProviderProxyTestRequest struct {
	ProxyURL string `json:"proxy_url"`
}

type ProviderProxyTargetTestResult struct {
	Key          string                            `json:"key"`
	Name         string                            `json:"name"`
	URL          string                            `json:"url"`
	OK           bool                              `json:"ok"`
	StatusCode   int                               `json:"status_code,omitempty"`
	LatencyMS    int64                             `json:"latency_ms,omitempty"`
	Error        string                            `json:"error,omitempty"`
	Completed    int                               `json:"completed"`
	Total        int                               `json:"total"`
	SuccessCount int                               `json:"success_count"`
	Samples      []ProviderProxyTargetSampleResult `json:"samples,omitempty"`
}

type ProviderProxyTargetSampleResult struct {
	Index      int    `json:"index"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ProviderProxyTestStreamEvent struct {
	Type            string                           `json:"type"`
	ExitIP          string                           `json:"exit_ip,omitempty"`
	ExitCountry     string                           `json:"exit_country,omitempty"`
	ExitCountryCode string                           `json:"exit_country_code,omitempty"`
	ExitRegion      string                           `json:"exit_region,omitempty"`
	ExitCity        string                           `json:"exit_city,omitempty"`
	ExitError       string                           `json:"exit_error,omitempty"`
	Targets         []ProviderProxyTargetTestResult  `json:"targets,omitempty"`
	TargetKey       string                           `json:"target_key,omitempty"`
	Target          *ProviderProxyTargetTestResult   `json:"target,omitempty"`
	Sample          *ProviderProxyTargetSampleResult `json:"sample,omitempty"`
}

type providerProxyTestTarget struct {
	Key  string
	Name string
	URL  string
}

type providerProxyExitInfo struct {
	IP          string
	Country     string
	CountryCode string
	Region      string
	City        string
}

var providerProxyTestTargets = []providerProxyTestTarget{
	{Key: "bytedance", Name: "字节跳动", URL: "https://www.bytedance.com/"},
	{Key: "taobao", Name: "淘宝", URL: "https://www.taobao.com/"},
	{Key: "wechat", Name: "微信", URL: "https://weixin.qq.com/"},
	{Key: "github", Name: "GitHub", URL: "https://github.com/"},
	{Key: "cloudflare", Name: "Cloudflare", URL: "https://www.cloudflare.com/cdn-cgi/trace"},
	{Key: "youtube", Name: "YouTube", URL: "https://www.youtube.com/generate_204"},
}

var providerProxyExitLookupURLs = []string{
	"https://ipapi.co/json/",
	"https://ipinfo.io/json",
	"https://api.ip.sb/geoip",
}

// ProviderProxyTestHandler 测试代理出口信息和到常用站点的延迟。
func ProviderProxyTestHandler(c *gin.Context) {
	var req ProviderProxyTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	proxyURL, err := sanitizeProviderProxyURL(req.ProxyURL)
	if err != nil {
		common.BadRequest(c, "Invalid proxy_url: "+err.Error())
		return
	}
	if proxyURL == "" {
		common.BadRequest(c, "代理地址不能为空")
		return
	}

	client, err := providers.GetClientWithProxy(providerProxySampleTimeout, proxyURL)
	if err != nil {
		common.BadRequest(c, "Invalid proxy_url: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), providerProxyTestTimeout)
	defer cancel()

	streamProviderProxyTest(c, ctx, cancel, client)
}

func streamProviderProxyTest(c *gin.Context, ctx context.Context, cancel context.CancelFunc, client *http.Client) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		common.InternalServerError(c, "当前响应不支持流式推送")
		return
	}

	c.Status(http.StatusOK)
	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")

	events := make(chan ProviderProxyTestStreamEvent, providerProxyEventBuffer)
	go func() {
		defer close(events)
		runProviderProxyTestStream(ctx, client, func(event ProviderProxyTestStreamEvent) bool {
			select {
			case events <- event:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()

	encoder := json.NewEncoder(c.Writer)
	for event := range events {
		if err := encoder.Encode(event); err != nil {
			cancel()
			return
		}
		flusher.Flush()
	}
}

func runProviderProxyTestStream(ctx context.Context, client *http.Client, send func(ProviderProxyTestStreamEvent) bool) {
	initialTargets := make([]ProviderProxyTargetTestResult, 0, len(providerProxyTestTargets))
	for _, target := range providerProxyTestTargets {
		initialTargets = append(initialTargets, newProviderProxyTargetResult(target))
	}
	if !send(ProviderProxyTestStreamEvent{Type: "init", Targets: initialTargets}) {
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		exitInfo, err := lookupProviderProxyExit(ctx, client)
		if err != nil {
			send(ProviderProxyTestStreamEvent{Type: "exit", ExitError: err.Error()})
			return
		}
		send(ProviderProxyTestStreamEvent{
			Type:            "exit",
			ExitIP:          exitInfo.IP,
			ExitCountry:     exitInfo.Country,
			ExitCountryCode: exitInfo.CountryCode,
			ExitRegion:      exitInfo.Region,
			ExitCity:        exitInfo.City,
		})
	}()

	for _, target := range providerProxyTestTargets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := newProviderProxyTargetResult(target)
			for index := 0; index < providerProxySamplesPerTarget; index++ {
				if ctx.Err() != nil {
					break
				}
				sample := testProviderProxyTargetSample(ctx, client, target, index)
				applyProviderProxySample(&result, sample)
				sampleCopy := sample
				if !send(ProviderProxyTestStreamEvent{
					Type:      "target_sample",
					TargetKey: target.Key,
					Sample:    &sampleCopy,
				}) {
					return
				}
			}
			resultCopy := result
			send(ProviderProxyTestStreamEvent{
				Type:      "target_done",
				TargetKey: target.Key,
				Target:    &resultCopy,
			})
		}()
	}
	wg.Wait()
	send(ProviderProxyTestStreamEvent{Type: "done"})
}

func newProviderProxyTargetResult(target providerProxyTestTarget) ProviderProxyTargetTestResult {
	return ProviderProxyTargetTestResult{
		Key:     target.Key,
		Name:    target.Name,
		URL:     target.URL,
		Total:   providerProxySamplesPerTarget,
		Samples: make([]ProviderProxyTargetSampleResult, 0, providerProxySamplesPerTarget),
	}
}

func lookupProviderProxyExit(ctx context.Context, client *http.Client) (providerProxyExitInfo, error) {
	var lastErr error
	for _, endpoint := range providerProxyExitLookupURLs {
		info, err := lookupProviderProxyExitOnce(ctx, client, endpoint)
		if err == nil && strings.TrimSpace(info.IP) != "" {
			return info, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return providerProxyExitInfo{}, lastErr
	}
	return providerProxyExitInfo{}, errors.New("未获取到出口地址")
}

func lookupProviderProxyExitOnce(ctx context.Context, client *http.Client, endpoint string) (providerProxyExitInfo, error) {
	reqCtx, cancel := context.WithTimeout(ctx, providerProxyExitLookupTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return providerProxyExitInfo{}, err
	}
	req.Header.Set("User-Agent", providerProxyTestUserAgent)
	res, err := client.Do(req)
	if err != nil {
		return providerProxyExitInfo{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 500 {
		return providerProxyExitInfo{}, fmt.Errorf("出口查询返回状态 %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 128*1024))
	if err != nil {
		return providerProxyExitInfo{}, err
	}
	return parseProviderProxyExitResponse(body)
}

func parseProviderProxyExitResponse(body []byte) (providerProxyExitInfo, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return providerProxyExitInfo{}, err
	}

	info := providerProxyExitInfo{
		IP:          firstProxyExitString(payload, "ip", "query"),
		Country:     firstProxyExitString(payload, "country_name", "country"),
		CountryCode: firstProxyExitString(payload, "country_code", "countryCode", "country"),
		Region:      firstProxyExitString(payload, "region", "region_name", "regionName"),
		City:        firstProxyExitString(payload, "city"),
	}
	if strings.EqualFold(info.Country, info.CountryCode) {
		info.Country = ""
	}
	if strings.TrimSpace(info.IP) == "" {
		return providerProxyExitInfo{}, errors.New("出口查询响应缺少 IP")
	}
	return info, nil
}

func firstProxyExitString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := payload[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func testProviderProxyTargetSample(ctx context.Context, client *http.Client, target providerProxyTestTarget, index int) ProviderProxyTargetSampleResult {
	result := ProviderProxyTargetSampleResult{Index: index}

	reqCtx, cancel := context.WithTimeout(ctx, providerProxySampleTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target.URL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", providerProxyTestUserAgent)
	req.Header.Set("Range", "bytes=0-0")

	start := time.Now()
	res, err := client.Do(req)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))

	result.StatusCode = res.StatusCode
	result.OK = res.StatusCode >= 200 && res.StatusCode < 500
	if !result.OK {
		result.Error = fmt.Sprintf("HTTP %d", res.StatusCode)
	}
	return result
}

func applyProviderProxySample(result *ProviderProxyTargetTestResult, sample ProviderProxyTargetSampleResult) {
	result.Samples = append(result.Samples, sample)
	result.Completed = len(result.Samples)
	result.StatusCode = sample.StatusCode

	var successCount int
	var successLatencyTotal int64
	var lastError string
	for _, item := range result.Samples {
		if item.OK {
			successCount++
			successLatencyTotal += item.LatencyMS
			continue
		}
		if item.Error != "" {
			lastError = item.Error
		}
	}

	result.SuccessCount = successCount
	result.OK = successCount > 0
	if successCount > 0 {
		result.LatencyMS = successLatencyTotal / int64(successCount)
		result.Error = ""
		return
	}
	result.LatencyMS = 0
	result.Error = lastError
}
