package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/racio/orvion/models"
	"github.com/tidwall/gjson"
)

const (
	InitScannerBufferSize = 1024 * 8         // 8KB
	MaxScannerBufferSize  = 1024 * 1024 * 64 // 64MB
)

type Processer func(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error)

func ProcesserOpenAI(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usage models.Usage
	var output models.OutputUnion
	var size int

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk, chunkSize := range ScannerToken(scanner) {
		size += chunkSize
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if !stream {
			output.OfString = chunk
			mergeOpenAIUsageMax(&usage, extractOpenAIUsageFromPayload(chunk))
			break
		}
		chunk = strings.TrimPrefix(chunk, "data: ")
		if chunk == "[DONE]" {
			break
		}
		// 流式过程中错误
		errStr := gjson.Get(chunk, "error")
		if errStr.Exists() {
			return nil, nil, errors.New(errStr.String())
		}
		output.OfStringArray = append(output.OfStringArray, chunk)
		mergeOpenAIUsageMax(&usage, extractOpenAIUsageFromPayload(chunk))
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	chunkTime := time.Since(start) - firstChunkTime
	usage.CacheHitRate = calculateCacheHitRate(usage.PromptTokens, usage.CachedTokens)

	return &models.ChatLog{
		FirstChunkTimeMs: int(firstChunkTime.Milliseconds()),
		ChunkTimeMs:      int(chunkTime.Milliseconds()),
		Usage:            usage,
		Tps:              float64(usage.TotalTokens) / chunkTime.Seconds(),
		Size:             size,
	}, &output, nil
}

type AnthropicUsage struct {
	InputTokens              int64  `json:"input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	ServiceTier              string `json:"service_tier"`
}

func ProcesserOpenAiRes(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usage models.Usage
	var output models.OutputUnion
	var size int

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk, chunkSize := range ScannerToken(scanner) {
		size += chunkSize
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if !stream {
			output.OfString = chunk
			mergeOpenAIUsageMax(&usage, extractOpenAIUsageFromPayload(chunk))
			break
		}

		if _, ok := strings.CutPrefix(chunk, "event: "); ok {
			continue
		}
		content := strings.TrimPrefix(chunk, "data: ")
		if content == "" {
			continue
		}
		if content == "[DONE]" {
			break
		}
		output.OfStringArray = append(output.OfStringArray, content)
		mergeOpenAIUsageMax(&usage, extractOpenAIUsageFromPayload(content))
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	chunkTime := time.Since(start) - firstChunkTime
	usage.CacheHitRate = calculateCacheHitRate(usage.PromptTokens, usage.CachedTokens)

	return &models.ChatLog{
		FirstChunkTimeMs: int(firstChunkTime.Milliseconds()),
		ChunkTimeMs:      int(chunkTime.Milliseconds()),
		Usage:            usage,
		Tps:              float64(usage.TotalTokens) / chunkTime.Seconds(),
		Size:             size,
	}, &output, nil
}

func parseOpenAIUsage(usageStr string) models.Usage {
	var usage models.Usage
	raw := []byte(usageStr)
	if !gjson.ValidBytes(raw) {
		return usage
	}

	usageNode := gjson.ParseBytes(raw)
	usage.PromptTokens = usageNode.Get("prompt_tokens").Int()
	if usage.PromptTokens == 0 {
		usage.PromptTokens = usageNode.Get("input_tokens").Int()
	}
	usage.CompletionTokens = usageNode.Get("completion_tokens").Int()
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = usageNode.Get("output_tokens").Int()
	}
	usage.TotalTokens = usageNode.Get("total_tokens").Int()
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	detailsNode := usageNode.Get("prompt_tokens_details")
	if !detailsNode.Exists() {
		detailsNode = usageNode.Get("input_tokens_details")
	}
	if detailsNode.Exists() {
		rawDetails := strings.TrimSpace(detailsNode.Raw)
		if rawDetails != "" && rawDetails != "null" {
			usage.PromptTokensDetails = rawDetails
		}
	}

	cachedTokens := usageNode.Get("prompt_tokens_details.cached_tokens").Int()
	if cachedTokens == 0 {
		cachedTokens = usageNode.Get("input_tokens_details.cached_tokens").Int()
	}
	usage.CachedTokens = normalizeCachedTokens(cachedTokens)
	if usage.PromptTokensDetails == "" {
		usage.PromptTokensDetails = buildPromptTokensDetailsJSON(usage.CachedTokens, 0, false)
	}
	return usage
}

func ProcesserAnthropic(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usage AnthropicUsage

	var output models.OutputUnion
	var size int

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk, chunkSize := range ScannerToken(scanner) {
		size += chunkSize
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if !stream {
			output.OfString = chunk
			mergeAnthropicUsageMax(&usage, gjson.Get(chunk, "usage"))
			mergeAnthropicUsageMax(&usage, gjson.Get(chunk, "message.usage"))
			break
		}

		if after, ok := strings.CutPrefix(chunk, "event: "); ok {
			_ = after
			continue
		}

		after, ok := strings.CutPrefix(chunk, "data: ")
		if !ok {
			continue
		}

		output.OfStringArray = append(output.OfStringArray, after)
		mergeAnthropicUsageMax(&usage, gjson.Get(after, "usage"))
		mergeAnthropicUsageMax(&usage, gjson.Get(after, "message.usage"))
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	chunkTime := time.Since(start) - firstChunkTime
	totalTokens := usage.InputTokens + usage.OutputTokens
	cacheReadTokens := normalizeCachedTokens(usage.CacheReadInputTokens)
	cacheWriteTokens := normalizeCachedTokens(usage.CacheCreationInputTokens)
	cacheHitRate := calculateCacheHitRate(usage.InputTokens, cacheReadTokens)

	return &models.ChatLog{
		FirstChunkTimeMs: int(firstChunkTime.Milliseconds()),
		ChunkTimeMs:      int(chunkTime.Milliseconds()),
		Usage: models.Usage{
			PromptTokens:        usage.InputTokens,
			CompletionTokens:    usage.OutputTokens,
			TotalTokens:         totalTokens,
			CachedTokens:        cacheReadTokens,
			CacheHitRate:        cacheHitRate,
			PromptTokensDetails: buildPromptTokensDetailsJSON(cacheReadTokens, cacheWriteTokens, true),
		},
		Tps:  float64(totalTokens) / chunkTime.Seconds(),
		Size: size,
	}, &output, nil
}

func ScannerToken(reader *bufio.Scanner) iter.Seq2[string, int] {
	return func(yield func(string, int) bool) {
		for reader.Scan() {
			chunk := reader.Text()
			if chunk == "" {
				continue
			}
			if !yield(chunk, len(reader.Bytes())) {
				return
			}
		}
	}
}

func normalizeCachedTokens(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func buildPromptTokensDetailsJSON(cachedTokens int64, cacheWriteTokens int64, promptExcludesCached bool) string {
	cachedTokens = normalizeCachedTokens(cachedTokens)
	cacheWriteTokens = normalizeCachedTokens(cacheWriteTokens)
	if cachedTokens <= 0 && cacheWriteTokens <= 0 && !promptExcludesCached {
		return ""
	}
	details := models.PromptTokensDetails{
		CachedTokens:              cachedTokens,
		CacheWriteTokens:          cacheWriteTokens,
		PromptExcludesCachedToken: promptExcludesCached,
	}
	content, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(content)
}

func extractOpenAIUsageFromPayload(payload string) models.Usage {
	var merged models.Usage

	// 兼容 payload 本身就是 usage 对象的情况。
	mergeOpenAIUsageMax(&merged, parseOpenAIUsage(payload))

	// 兼容响应对象中 usage 嵌套的不同路径。
	for _, path := range []string{"usage", "response.usage"} {
		node := gjson.Get(payload, path)
		if !node.Exists() {
			continue
		}
		mergeOpenAIUsageMax(&merged, parseOpenAIUsage(node.Raw))
	}

	if merged.TotalTokens == 0 {
		merged.TotalTokens = merged.PromptTokens + merged.CompletionTokens
	}
	if merged.PromptTokensDetails == "" {
		merged.PromptTokensDetails = buildPromptTokensDetailsJSON(merged.CachedTokens, 0, false)
	}

	return merged
}

func mergeOpenAIUsageMax(current *models.Usage, candidate models.Usage) {
	if candidate.PromptTokens > current.PromptTokens {
		current.PromptTokens = candidate.PromptTokens
	}
	if candidate.CompletionTokens > current.CompletionTokens {
		current.CompletionTokens = candidate.CompletionTokens
	}
	if candidate.CachedTokens > current.CachedTokens {
		current.CachedTokens = candidate.CachedTokens
	}
	if candidate.TotalTokens > current.TotalTokens {
		current.TotalTokens = candidate.TotalTokens
	}
	if current.TotalTokens == 0 {
		current.TotalTokens = current.PromptTokens + current.CompletionTokens
	}
	if candidate.PromptTokensDetails != "" {
		if current.PromptTokensDetails == "" || candidate.CachedTokens >= current.CachedTokens {
			current.PromptTokensDetails = candidate.PromptTokensDetails
		}
	}
	if current.PromptTokensDetails == "" {
		current.PromptTokensDetails = buildPromptTokensDetailsJSON(current.CachedTokens, 0, false)
	}
}

func calculateCacheHitRate(promptTokens, cachedTokens int64) float64 {
	if promptTokens <= 0 || cachedTokens <= 0 {
		return 0
	}
	if cachedTokens > promptTokens {
		cachedTokens = promptTokens
	}
	return float64(cachedTokens) / float64(promptTokens) * 100
}

func mergeAnthropicUsageMax(current *AnthropicUsage, usageNode gjson.Result) {
	if !usageNode.Exists() || !usageNode.IsObject() {
		return
	}

	if input := usageNode.Get("input_tokens").Int(); input > current.InputTokens {
		current.InputTokens = input
	}
	if output := usageNode.Get("output_tokens").Int(); output > current.OutputTokens {
		current.OutputTokens = output
	}
	if cacheRead := normalizeCachedTokens(usageNode.Get("cache_read_input_tokens").Int()); cacheRead > current.CacheReadInputTokens {
		current.CacheReadInputTokens = cacheRead
	}
	cacheCreation := normalizeCachedTokens(extractCacheCreationTokens(usageNode))
	if cacheCreation > current.CacheCreationInputTokens {
		current.CacheCreationInputTokens = cacheCreation
	}
}

func extractCacheCreationTokens(usageNode gjson.Result) int64 {
	// 1. 嵌套新格式：cache_creation.ephemeral_5m_input_tokens / ephemeral_1h_input_tokens
	cacheCreation := usageNode.Get("cache_creation")
	if cacheCreation.Exists() && cacheCreation.IsObject() {
		hasNested := cacheCreation.Get("ephemeral_5m_input_tokens").Exists() ||
			cacheCreation.Get("ephemeral_1h_input_tokens").Exists()
		if hasNested {
			return cacheCreation.Get("ephemeral_5m_input_tokens").Int() +
				cacheCreation.Get("ephemeral_1h_input_tokens").Int()
		}
	}

	// 2. 扁平新格式：claude_cache_creation_5_m_tokens / claude_cache_creation_1_h_tokens
	hasFlat := usageNode.Get("claude_cache_creation_5_m_tokens").Exists() ||
		usageNode.Get("claude_cache_creation_1_h_tokens").Exists()
	if hasFlat {
		return usageNode.Get("claude_cache_creation_5_m_tokens").Int() +
			usageNode.Get("claude_cache_creation_1_h_tokens").Int()
	}

	// 3. 旧格式
	return usageNode.Get("cache_creation_input_tokens").Int()
}
