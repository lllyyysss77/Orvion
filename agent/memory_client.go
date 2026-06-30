package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	agentmemory "github.com/racio/orvion/agent/memory"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/tidwall/gjson"
)

const (
	telegramAgentMemoryProcessTimeout = 2 * time.Minute
	telegramAgentMemoryMaxTokens      = 1200
)

type telegramAgentMemoryLLM struct {
	cfg models.TelegramAgentConfig
}

func scheduleTelegramAgentMemoryExtraction(cfg models.TelegramAgentConfig, prompt string, answer string, occurredAt time.Time) {
	if !agentmemory.Enabled(cfg) {
		return
	}
	prompt = strings.TrimSpace(prompt)
	answer = strings.TrimSpace(answer)
	if prompt == "" || answer == "" {
		return
	}

	pkg.GoSafe("agent.telegram_memory_extract", func() {
		ctx, cancel := context.WithTimeout(context.Background(), telegramAgentMemoryProcessTimeout)
		defer cancel()
		err := agentmemory.ProcessTurn(ctx, cfg, telegramAgentMemoryLLM{cfg: cfg}, agentmemory.Turn{
			User:       prompt,
			Assistant:  answer,
			OccurredAt: occurredAt,
		})
		if err != nil {
			slogWarn("提炼 TG Agent 长期记忆失败", "error", err)
		}
	})
}

func RollupTelegramAgentMemories(ctx context.Context, now time.Time) (agentmemory.RollupResult, error) {
	cfg, err := loadTelegramAgentConfig(ctx)
	if err != nil {
		return agentmemory.RollupResult{}, err
	}
	if !isTelegramAgentEnabled(cfg) || !agentmemory.Enabled(cfg) || !telegramAgentMemoryModelReady(cfg) {
		return agentmemory.RollupResult{}, nil
	}
	return agentmemory.RollupCompleted(ctx, cfg, telegramAgentMemoryLLM{cfg: cfg}, now)
}

func telegramAgentMemoryModelReady(cfg models.TelegramAgentConfig) bool {
	return strings.TrimSpace(cfg.BaseURL) != "" &&
		strings.TrimSpace(cfg.APIKey) != "" &&
		strings.TrimSpace(cfg.Model) != ""
}

func (client telegramAgentMemoryLLM) Complete(ctx context.Context, req agentmemory.CompleteRequest) (string, error) {
	pool, err := buildTelegramAgentDirectProviderPool(client.cfg, false)
	if err != nil {
		return "", err
	}
	startedAt := time.Now()
	attempt, err := performTelegramAgentProviderRequestWithRetry(ctx, pool, false, startedAt, func(requestCtx context.Context, selected selectedModelProvider) ([]byte, context.Context, error) {
		return buildTelegramAgentMemoryRequestBody(requestCtx, client.cfg, selected, req)
	})
	if err != nil {
		return "", err
	}
	res := attempt.Response
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	text := extractTelegramAgentNonStreamText(attempt.Selected.responseStyle(), string(raw))
	if strings.TrimSpace(text) == "" {
		return "", errors.New("长期记忆模型返回空内容")
	}
	return strings.TrimSpace(text), nil
}

func buildTelegramAgentMemoryRequestBody(ctx context.Context, cfg models.TelegramAgentConfig, selected selectedModelProvider, req agentmemory.CompleteRequest) ([]byte, context.Context, error) {
	maxTokens := resolveTelegramAgentMemoryMaxTokens(cfg)
	temperature := cfg.Temperature
	messages := []chatMessage{{Role: "user", Content: req.UserPrompt}}
	systemPrompt := strings.TrimSpace(req.SystemPrompt)

	switch selected.ProviderStyle {
	case consts.StyleAnthropic:
		payload := map[string]any{
			"messages":   toAnthropicMessages(messages),
			"max_tokens": maxTokens,
			"stream":     false,
		}
		if systemPrompt != "" {
			payload["system"] = systemPrompt
		}
		if temperature != nil {
			payload["temperature"] = *temperature
		}
		body, err := jsonMarshal(payload)
		return body, ctx, err
	case consts.StyleOpenAIRes:
		payload := map[string]any{
			"input":  toOpenAIResponsesInput(messages),
			"stream": false,
		}
		if systemPrompt != "" {
			payload["instructions"] = systemPrompt
		}
		if maxTokens > 0 {
			payload["max_output_tokens"] = maxTokens
		}
		if temperature != nil {
			payload["temperature"] = *temperature
		}
		body, err := jsonMarshal(payload)
		return body, context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, "responses"), err
	case consts.StyleGemini:
		payload := map[string]any{
			"contents": toGeminiContents(messages),
		}
		if systemPrompt != "" {
			payload["system_instruction"] = map[string]any{
				"parts": []map[string]string{{"text": systemPrompt}},
			}
		}
		body, err := jsonMarshal(payload)
		return body, context.WithValue(ctx, consts.ContextKeyGeminiStream, false), err
	default:
		payload := map[string]any{
			"messages": toOpenAIChatMessages(systemPrompt, messages),
			"stream":   false,
		}
		if maxTokens > 0 {
			payload["max_tokens"] = maxTokens
		}
		if temperature != nil {
			payload["temperature"] = *temperature
		}
		body, err := jsonMarshal(payload)
		return body, context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, "chat/completions"), err
	}
}

func resolveTelegramAgentMemoryMaxTokens(cfg models.TelegramAgentConfig) int {
	maxTokens := resolveTelegramAgentMaxTokens(cfg)
	if maxTokens <= 0 || maxTokens > telegramAgentMemoryMaxTokens {
		return telegramAgentMemoryMaxTokens
	}
	return maxTokens
}

func extractTelegramAgentNonStreamText(style string, raw string) string {
	switch style {
	case consts.StyleAnthropic:
		parts := make([]string, 0)
		gjson.Get(raw, "content").ForEach(func(_, item gjson.Result) bool {
			if text := item.Get("text").String(); text != "" {
				parts = append(parts, text)
			}
			return true
		})
		return strings.Join(parts, "")
	case consts.StyleOpenAIRes:
		if text := gjson.Get(raw, "output_text").String(); text != "" {
			return text
		}
		parts := make([]string, 0)
		gjson.Get(raw, "output").ForEach(func(_, item gjson.Result) bool {
			item.Get("content").ForEach(func(_, content gjson.Result) bool {
				if text := content.Get("text").String(); text != "" {
					parts = append(parts, text)
				}
				return true
			})
			return true
		})
		return strings.Join(parts, "")
	case consts.StyleGemini:
		parts := make([]string, 0)
		gjson.Get(raw, "candidates").ForEach(func(_, candidate gjson.Result) bool {
			candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
				if text := part.Get("text").String(); text != "" {
					parts = append(parts, text)
				}
				return true
			})
			return true
		})
		return strings.Join(parts, "")
	default:
		content := gjson.Get(raw, "choices.0.message.content")
		if content.IsArray() {
			parts := make([]string, 0)
			content.ForEach(func(_, item gjson.Result) bool {
				if text := item.Get("text").String(); text != "" {
					parts = append(parts, text)
				}
				return true
			})
			return strings.Join(parts, "")
		}
		return content.String()
	}
}

func appendTelegramAgentMemoryPrompt(ctx context.Context, cfg models.TelegramAgentConfig, systemPrompt string) string {
	base := strings.TrimSpace(systemPrompt)
	memoryPrompt, err := agentmemory.BuildContextPrompt(ctx, cfg)
	if err != nil {
		slogWarn("加载 TG Agent 长期记忆失败", "error", err)
		return base
	}
	if strings.TrimSpace(memoryPrompt) == "" {
		return base
	}
	if base == "" {
		return memoryPrompt
	}
	return base + "\n\n" + memoryPrompt
}

func slogWarn(msg string, args ...any) {
	// 单独封装是为了让 memory 相关文件不影响其他文件的导入结构。
	slog.Warn(msg, args...)
}

func jsonMarshal(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("构建长期记忆请求失败: %w", err)
	}
	return body, nil
}
