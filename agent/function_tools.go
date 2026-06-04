package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"github.com/tidwall/gjson"
)

const (
	telegramAgentToolLoopMaxRounds = 20

	telegramAgentToolListModels           = "list_models"
	telegramAgentToolListProviders        = "list_providers"
	telegramAgentToolSetModelStatus       = "set_model_status"
	telegramAgentToolSetModelsStatusBatch = "set_models_status_batch"
	telegramAgentToolSetProviderStatus    = "set_provider_status"
	telegramAgentToolGetProviderConfig    = "get_provider_config"
	telegramAgentToolUpdateProviderConfig = "update_provider_config"
	telegramAgentToolReadSystemLogs       = "read_system_logs"
	telegramAgentToolReadRequestLogs      = "read_request_logs"
	telegramAgentToolCreateAuthKey        = "create_auth_key"
	telegramAgentToolUpdateAuthKey        = "update_auth_key"
)

type telegramAgentOpenAIMessage struct {
	Role       string                        `json:"role"`
	Content    string                        `json:"content,omitempty"`
	ToolCallID string                        `json:"tool_call_id,omitempty"`
	ToolCalls  []telegramAgentOpenAIToolCall `json:"tool_calls,omitempty"`
}

type telegramAgentOpenAIToolCall struct {
	ID       string                          `json:"id,omitempty"`
	Type     string                          `json:"type"`
	Function telegramAgentOpenAIFunctionCall `json:"function"`
}

type telegramAgentOpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type telegramAgentToolCallArgs struct {
	Query                      string                              `json:"query"`
	Limit                      int                                 `json:"limit"`
	Level                      string                              `json:"level"`
	Status                     string                              `json:"status"`
	ProviderName               string                              `json:"provider_name"`
	Model                      string                              `json:"model"`
	RecentMinutes              int                                 `json:"recent_minutes"`
	StartAt                    string                              `json:"start_at"`
	EndAt                      string                              `json:"end_at"`
	KeySuffix                  *string                             `json:"key_suffix"`
	AllowAll                   *bool                               `json:"allow_all"`
	AuthModels                 []string                            `json:"models"`
	ModelKeywords              []string                            `json:"model_keywords"`
	ExpiresAt                  *string                             `json:"expires_at"`
	ClearExpiresAt             bool                                `json:"clear_expires_at"`
	RPMLimit                   *int                                `json:"rpm_limit"`
	Target                     string                              `json:"target"`
	Enabled                    *bool                               `json:"enabled"`
	Bulk                       bool                                `json:"bulk"`
	Items                      []telegramAgentModelStatusBatchItem `json:"items"`
	Name                       *string                             `json:"name"`
	Config                     *string                             `json:"config"`
	ConfigUpdates              map[string]any                      `json:"config_updates"`
	RemoveConfigKeys           []string                            `json:"remove_config_keys"`
	Console                    *string                             `json:"console"`
	ProxyURL                   *string                             `json:"proxy_url"`
	ModelsFetchMode            *string                             `json:"models_fetch_mode"`
	Capabilities               *[]string                           `json:"capabilities"`
	InterfaceConversionEnabled *bool                               `json:"interface_conversion_enabled"`
	InterfaceConversionTarget  *string                             `json:"interface_conversion_target"`
}

type telegramAgentModelStatusBatchItem struct {
	Target  string `json:"target"`
	Enabled *bool  `json:"enabled"`
	Bulk    bool   `json:"bulk"`
}

type telegramAgentOpenAIStreamReadResult struct {
	Usage            models.Usage
	FirstChunkTimeMs int
	ChunkTimeMs      int
	Size             int
	ToolCalls        []telegramAgentOpenAIToolCall
}

type telegramAgentToolResultPayload struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text"`
	Final bool   `json:"final,omitempty"`
}

func streamTelegramAgentReplyWithFunctionTools(ctx context.Context, cfg models.TelegramAgentConfig, selected selectedModelProvider, history []chatMessage, prompt string, chatID int64, onDelta streamDeltaHandler) (telegramAgentReplyResult, error) {
	result := telegramAgentReplyResult{
		Selected:  selected,
		StartedAt: time.Now(),
	}
	if selected.ProviderStyle != consts.StyleOpenAI {
		return result, errors.New("当前 TG Agent 模型提供商暂不支持 function call 工具调用")
	}

	systemPrompt := strings.TrimSpace(cfg.SystemPrompt)
	if systemPrompt != "" {
		systemPrompt += "\n\n"
	}
	systemPrompt += telegramAgentFunctionToolSystemPrompt(cfg)

	messages := toTelegramAgentOpenAIMessages(systemPrompt, history, prompt)
	for round := 0; round < telegramAgentToolLoopMaxRounds; round++ {
		body, err := buildTelegramAgentOpenAIChatBody(cfg, messages, true, true)
		if err != nil {
			return result, err
		}
		result.RequestBody = append([]byte(nil), body...)

		res, proxyMs, err := performTelegramAgentProviderRequest(ctx, selected, body, true, result.StartedAt)
		if err != nil {
			return result, err
		}
		if result.ProxyTimeMs == 0 {
			result.ProxyTimeMs = proxyMs
		}
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
			_ = res.Body.Close()
			result.Size += len(body)
			return result, fmt.Errorf("%s/%s 返回状态 %d: %s", selected.ProviderName, selected.ProviderModel, res.StatusCode, strings.TrimSpace(string(body)))
		}

		streamResult, err := readTelegramAgentOpenAIStreamWithTools(ctx, res.Body, result.StartedAt, onDelta)
		_ = res.Body.Close()
		mergeTelegramAgentUsageAdd(&result.Usage, streamResult.Usage)
		if result.FirstChunkTimeMs == 0 {
			result.FirstChunkTimeMs = streamResult.FirstChunkTimeMs
		}
		result.ChunkTimeMs += streamResult.ChunkTimeMs
		result.Size += streamResult.Size
		if err != nil {
			return result, err
		}
		if len(streamResult.ToolCalls) == 0 {
			normalizeTelegramAgentUsage(&result.Usage)
			return result, nil
		}

		messages = append(messages, telegramAgentOpenAIMessage{
			Role:      "assistant",
			ToolCalls: streamResult.ToolCalls,
		})
		var directFinalText string
		messages, directFinalText = appendTelegramAgentToolResults(ctx, chatID, cfg, streamResult.ToolCalls, messages)
		if directFinalText != "" {
			if err := onDelta(directFinalText); err != nil {
				return result, err
			}
			normalizeTelegramAgentUsage(&result.Usage)
			return result, nil
		}
	}

	return result, errors.New("工具调用轮次过多，请拆成更明确的指令")
}

func appendTelegramAgentToolResults(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCalls []telegramAgentOpenAIToolCall, messages []telegramAgentOpenAIMessage) ([]telegramAgentOpenAIMessage, string) {
	directFinalText := ""
	for _, toolCall := range toolCalls {
		toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, cfg, toolCall)
		messages = append(messages, telegramAgentOpenAIMessage{
			Role:       "tool",
			ToolCallID: toolCall.ID,
			Content:    toolResult,
		})
		if text, ok := telegramAgentToolDirectFinalText(toolResult); ok {
			directFinalText = text
		}
	}
	return messages, directFinalText
}

func performTelegramAgentProviderRequest(ctx context.Context, selected selectedModelProvider, body []byte, stream bool, startedAt time.Time) (*http.Response, int, error) {
	provider, err := providers.NewForStyleWithProxy(selected.ProviderStyle, selected.ProviderConfig, selected.ProviderProxy)
	if err != nil {
		return nil, 0, err
	}

	header := runtimesvc.BuildHeaders(nil, selected.WithHeader, selected.CustomerHeaders, stream)
	endpointCtx := context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, "chat/completions")
	req, err := provider.BuildReq(endpointCtx, header, selected.ProviderModel, body)
	if err != nil {
		return nil, 0, err
	}

	timeout := time.Duration(selected.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	client, err := providers.GetClientWithProxy(timeout, selected.ProviderProxy)
	if err != nil {
		return nil, 0, err
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	return res, int(time.Since(startedAt).Milliseconds()), nil
}

func buildTelegramAgentOpenAIChatBody(cfg models.TelegramAgentConfig, messages []telegramAgentOpenAIMessage, stream bool, withTools bool) ([]byte, error) {
	payload := map[string]any{
		"messages": messages,
		"stream":   stream,
	}
	if stream {
		payload["stream_options"] = map[string]bool{"include_usage": true}
	}
	if maxTokens := resolveTelegramAgentMaxTokens(cfg); maxTokens > 0 {
		payload["max_tokens"] = maxTokens
	}
	if cfg.Temperature != nil {
		payload["temperature"] = *cfg.Temperature
	}
	if withTools {
		payload["tools"] = telegramAgentFunctionTools(cfg)
		payload["tool_choice"] = "auto"
	}
	return json.Marshal(payload)
}

func toTelegramAgentOpenAIMessages(systemPrompt string, history []chatMessage, prompt string) []telegramAgentOpenAIMessage {
	messages := make([]telegramAgentOpenAIMessage, 0, len(history)+2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, telegramAgentOpenAIMessage{Role: "system", Content: systemPrompt})
	}
	for _, item := range history {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		messages = append(messages, telegramAgentOpenAIMessage{
			Role:    normalizeOpenAIRole(item.Role),
			Content: content,
		})
	}
	messages = append(messages, telegramAgentOpenAIMessage{Role: "user", Content: prompt})
	return messages
}

func readTelegramAgentOpenAIStreamWithTools(ctx context.Context, reader io.Reader, start time.Time, onDelta streamDeltaHandler) (telegramAgentOpenAIStreamReadResult, error) {
	var result telegramAgentOpenAIStreamReadResult
	var firstChunkSeen bool
	partials := map[int]*telegramAgentOpenAIToolCall{}

	err := readSSEData(ctx, reader, func(data string, size int) error {
		result.Size += size
		trimmed := strings.TrimSpace(data)
		if trimmed == "" || trimmed == "[DONE]" {
			return nil
		}
		if !firstChunkSeen {
			firstChunkSeen = true
			result.FirstChunkTimeMs = int(time.Since(start).Milliseconds())
		}
		usage := extractTelegramAgentOpenAIUsage(trimmed)
		mergeTelegramAgentUsageAdd(&result.Usage, usage)

		var callbackErr error
		gjson.Get(trimmed, "choices").ForEach(func(_, choice gjson.Result) bool {
			if text := choice.Get("delta.content").String(); text != "" {
				callbackErr = onDelta(text)
				if callbackErr != nil {
					return false
				}
			}
			choice.Get("delta.tool_calls").ForEach(func(_, rawCall gjson.Result) bool {
				index := int(rawCall.Get("index").Int())
				call := partials[index]
				if call == nil {
					call = &telegramAgentOpenAIToolCall{Type: "function"}
					partials[index] = call
				}
				if id := rawCall.Get("id").String(); id != "" {
					call.ID = id
				}
				if typ := rawCall.Get("type").String(); typ != "" {
					call.Type = typ
				}
				if name := rawCall.Get("function.name").String(); name != "" {
					call.Function.Name += name
				}
				if arguments := rawCall.Get("function.arguments").String(); arguments != "" {
					call.Function.Arguments += arguments
				}
				return true
			})
			return true
		})
		return callbackErr
	})
	if firstChunkSeen {
		elapsedMs := int(time.Since(start).Milliseconds())
		if elapsedMs > result.FirstChunkTimeMs {
			result.ChunkTimeMs = elapsedMs - result.FirstChunkTimeMs
		}
	}
	for index := 0; index < len(partials); index++ {
		call := partials[index]
		if call == nil || strings.TrimSpace(call.Function.Name) == "" {
			continue
		}
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_%d", index)
		}
		if call.Type == "" {
			call.Type = "function"
		}
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	normalizeTelegramAgentUsage(&result.Usage)
	return result, err
}

func executeTelegramAgentFunctionToolCall(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCall telegramAgentOpenAIToolCall) string {
	name := strings.TrimSpace(toolCall.Function.Name)
	args := telegramAgentToolCallArgs{}
	rawArgs := strings.TrimSpace(toolCall.Function.Arguments)
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			result := telegramAgentToolResult(false, "工具参数不是合法 JSON："+err.Error())
			recordTelegramAgentFunctionToolCallLog(ctx, chatID, cfg, toolCall, rawArgs, result)
			return result
		}
	}

	definition, ok := findTelegramAgentFunctionToolDefinition(cfg, name)
	if !ok {
		result := telegramAgentToolResult(false, "未知工具："+name)
		recordTelegramAgentFunctionToolCallLog(ctx, chatID, cfg, toolCall, rawArgs, result)
		return result
	}
	result := definition.Handler(ctx, chatID, cfg, args)
	recordTelegramAgentFunctionToolCallLog(ctx, chatID, cfg, toolCall, rawArgs, result)
	return result
}

func telegramAgentToolResult(ok bool, text string) string {
	return telegramAgentToolResultWithFinal(ok, text, false)
}

func telegramAgentToolFinalResult(ok bool, text string) string {
	return telegramAgentToolResultWithFinal(ok, text, true)
}

func telegramAgentToolResultWithFinal(ok bool, text string, final bool) string {
	payload := telegramAgentToolResultPayload{
		OK:    ok,
		Text:  text,
		Final: final,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return text
	}
	return string(raw)
}

func telegramAgentToolDirectFinalText(raw string) (string, bool) {
	var payload telegramAgentToolResultPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", false
	}
	text := strings.TrimSpace(payload.Text)
	return text, payload.Final && text != "" && strings.Contains(text, "待确认操作")
}

func telegramAgentFunctionToolSystemPrompt(cfg models.TelegramAgentConfig) string {
	lines := []string{
		"你可以通过 function call 调用 Orvion 项目管理工具。",
		"当用户想查看模型/提供商、读取系统日志/请求日志、新增或修改 API Key、修改模型/提供商状态、查看或修改提供商配置时，必须优先调用工具，不要自己猜测结果。",
		"用户提到报错、错误日志、慢响应、请求失败、最近日志、timeout、panic、SQL 等排查诉求时，优先调用日志读取工具。",
		"修改提供商配置时，provider 的 config 字段使用 config_updates 做局部修改，例如 base_url、api_key；删除配置字段使用 remove_config_keys。",
		"新增或修改 API Key 时，allow_all=false 表示限制模型；如用户给的是模型关键词，请用 model_keywords 批量匹配模型，不要把“claude 的模型”当成单个模型名。",
		"不要在普通回复中泄露 api_key、token、secret、password 等敏感配置值。",
		"用户说“claude 的相关模型”“所有 claude 模型”“claude 那批模型”这类表达时，target 应为 claude，bulk 应为 true。",
		"用户一句话里包含多个模型启停动作时，例如“禁用 claude 并开启 deepseek”，必须调用 set_models_status_batch，并把每个动作分别放进 items；不要只处理其中一个动作。",
		"如果用户在同一句话中要求修改后继续检查某些模型或提供商状态，修改工具执行后还要继续调用查看工具，不要只总结修改结果。",
		"完成工具调用后，用简体中文简洁总结工具结果。",
	}
	if telegramAgentRequiresToolConfirmation(cfg) {
		lines = append(lines, "修改类工具只会创建待确认操作；工具返回要求用户确认时，请明确告诉用户回复“确认”执行或“取消”放弃。")
	} else {
		lines = append(lines, "修改类工具会直接执行数据库更新；工具返回执行结果后，请直接告知用户结果，不要要求用户再次确认。")
	}
	return strings.Join(lines, "\n")
}

func telegramAgentFunctionTools(cfg models.TelegramAgentConfig) []map[string]any {
	definitions := telegramAgentFunctionToolDefinitions(cfg)
	tools := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, telegramAgentFunctionTool(
			definition.Name,
			definition.Description,
			definition.Properties,
			definition.Required,
		))
	}
	return tools
}

func telegramAgentFunctionTool(name string, description string, properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": description,
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           properties,
				"required":             required,
				"additionalProperties": false,
			},
		},
	}
}
