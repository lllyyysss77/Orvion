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

	var usageStr string
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
			usageStr = gjson.Get(chunk, "usage").String()
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

		// 部分厂商openai格式中 每段sse响应都会返回usage 兼容性考虑
		// if usageStr != "" {
		// 	break
		// }

		usage := gjson.Get(chunk, "usage")
		if usage.Exists() {
			if usage.Get("total_tokens").Int() != 0 ||
				usage.Get("prompt_tokens").Int() != 0 ||
				usage.Get("completion_tokens").Int() != 0 ||
				usage.Get("input_tokens").Int() != 0 ||
				usage.Get("output_tokens").Int() != 0 {
				usageStr = usage.String()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	// token用量
	openaiUsage := parseOpenAIUsage(usageStr)

	chunkTime := time.Since(start) - firstChunkTime

	return &models.ChatLog{
		FirstChunkTimeMs: int(firstChunkTime.Milliseconds()),
		ChunkTimeMs:      int(chunkTime.Milliseconds()),
		Usage:            openaiUsage,
		Tps:              float64(openaiUsage.TotalTokens) / chunkTime.Seconds(),
		Size:             size,
	}, &output, nil
}

type OpenAIResUsage struct {
	InputTokens        int64              `json:"input_tokens"`
	OutputTokens       int64              `json:"output_tokens"`
	TotalTokens        int64              `json:"total_tokens"`
	InputTokensDetails InputTokensDetails `json:"input_tokens_details"`
}

type InputTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
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

	var usageStr string
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
			usageNode := gjson.Get(chunk, "usage")
			if !usageNode.Exists() {
				usageNode = gjson.Get(chunk, "response.usage")
			}
			usageStr = usageNode.String()
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

		usageNode := gjson.Get(content, "usage")
		if !usageNode.Exists() {
			usageNode = gjson.Get(content, "response.usage")
		}
		if usageNode.Exists() {
			usageCandidate := usageNode.String()
			if usageStr == "" || responseUsageHasTokens(usageNode) {
				usageStr = usageCandidate
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	var openAIResUsage OpenAIResUsage
	usage := []byte(usageStr)
	if json.Valid(usage) {
		if err := json.Unmarshal(usage, &openAIResUsage); err != nil {
			return nil, nil, err
		}
	}
	if openAIResUsage.TotalTokens == 0 {
		openAIResUsage.TotalTokens = openAIResUsage.InputTokens + openAIResUsage.OutputTokens
	}
	cachedTokens := normalizeCachedTokens(openAIResUsage.InputTokensDetails.CachedTokens)

	chunkTime := time.Since(start) - firstChunkTime

	return &models.ChatLog{
		FirstChunkTimeMs: int(firstChunkTime.Milliseconds()),
		ChunkTimeMs:      int(chunkTime.Milliseconds()),
		Usage: models.Usage{
			PromptTokens:        openAIResUsage.InputTokens,
			CompletionTokens:    openAIResUsage.OutputTokens,
			TotalTokens:         openAIResUsage.TotalTokens,
			CachedTokens:        cachedTokens,
			PromptTokensDetails: buildPromptTokensDetailsJSON(cachedTokens, 0, false),
		},
		Tps:  float64(openAIResUsage.TotalTokens) / chunkTime.Seconds(),
		Size: size,
	}, &output, nil
}

func responseUsageHasTokens(node gjson.Result) bool {
	if !node.Exists() {
		return false
	}
	if node.Get("total_tokens").Int() > 0 {
		return true
	}
	if node.Get("prompt_tokens").Int() > 0 || node.Get("completion_tokens").Int() > 0 {
		return true
	}
	if node.Get("input_tokens").Int() > 0 || node.Get("output_tokens").Int() > 0 {
		return true
	}
	if node.Get("prompt_tokens_details.cached_tokens").Int() > 0 {
		return true
	}
	if node.Get("input_tokens_details.cached_tokens").Int() > 0 {
		return true
	}
	return false
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

	var usageStr string

	var output models.OutputUnion
	var size int

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	var event string
	for chunk, chunkSize := range ScannerToken(scanner) {
		size += chunkSize
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if !stream {
			output.OfString = chunk
			usageStr = gjson.Get(chunk, "usage").String()
			break
		}

		if after, ok := strings.CutPrefix(chunk, "event: "); ok {
			event = after
			continue
		}

		after, ok := strings.CutPrefix(chunk, "data: ")
		if !ok {
			continue
		}

		output.OfStringArray = append(output.OfStringArray, after)
		if event == "message_delta" {
			usageStr = gjson.Get(after, "usage").String()
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	var athropicUsage AnthropicUsage
	usage := []byte(usageStr)
	if json.Valid(usage) {
		if err := json.Unmarshal(usage, &athropicUsage); err != nil {
			return nil, nil, err
		}
	}

	chunkTime := time.Since(start) - firstChunkTime
	totalTokens := athropicUsage.InputTokens + athropicUsage.OutputTokens
	cacheReadTokens := normalizeCachedTokens(athropicUsage.CacheReadInputTokens)
	cacheWriteTokens := normalizeCachedTokens(athropicUsage.CacheCreationInputTokens)

	return &models.ChatLog{
		FirstChunkTimeMs: int(firstChunkTime.Milliseconds()),
		ChunkTimeMs:      int(chunkTime.Milliseconds()),
		Usage: models.Usage{
			PromptTokens:        athropicUsage.InputTokens,
			CompletionTokens:    athropicUsage.OutputTokens,
			TotalTokens:         totalTokens,
			CachedTokens:        cacheReadTokens,
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
