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

	agenttools "github.com/racio/orvion/agent/tools"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service/ifacebridge"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"github.com/tidwall/gjson"
)

const (
	telegramAgentToolLoopMaxRounds = 20
)

const (
	telegramAgentToolListModels             = agenttools.NameListModels
	telegramAgentToolListProviders          = agenttools.NameListProviders
	telegramAgentToolSetModelStatus         = agenttools.NameSetModelStatus
	telegramAgentToolSetModelsStatusBatch   = agenttools.NameSetModelsStatusBatch
	telegramAgentToolSetProviderStatus      = agenttools.NameSetProviderStatus
	telegramAgentToolGetProviderConfig      = agenttools.NameGetProviderConfig
	telegramAgentToolUpdateProviderConfig   = agenttools.NameUpdateProviderConfig
	telegramAgentToolReadSystemLogs         = agenttools.NameReadSystemLogs
	telegramAgentToolReadRequestLogs        = agenttools.NameReadRequestLogs
	telegramAgentToolGetSystemStatus        = agenttools.NameGetSystemStatus
	telegramAgentToolGetPerformanceStats    = agenttools.NameGetPerformanceStats
	telegramAgentToolListImageCache         = agenttools.NameListImageCache
	telegramAgentToolDeleteImageCache       = agenttools.NameDeleteImageCache
	telegramAgentToolRefreshImageCache      = agenttools.NameRefreshImageCache
	telegramAgentToolGetBackgroundTasks     = agenttools.NameGetBackgroundTasks
	telegramAgentToolTriggerBackgroundTask  = agenttools.NameTriggerBackgroundTask
	telegramAgentToolListAuthKeys           = agenttools.NameListAuthKeys
	telegramAgentToolCreateAuthKey          = agenttools.NameCreateAuthKey
	telegramAgentToolUpdateAuthKey          = agenttools.NameUpdateAuthKey
	telegramAgentToolListScheduledTasks     = agenttools.NameListScheduledTasks
	telegramAgentToolCreateScheduledTask    = agenttools.NameCreateScheduledTask
	telegramAgentToolUpdateScheduledTask    = agenttools.NameUpdateScheduledTask
	telegramAgentToolSetScheduledTaskStatus = agenttools.NameSetScheduledTaskStatus
	telegramAgentToolRunScheduledTask       = agenttools.NameRunScheduledTask
	telegramAgentToolListSkills             = agenttools.NameListSkills
	telegramAgentToolReadSkill              = agenttools.NameReadSkill
	telegramAgentToolRunTerminalCommand     = agenttools.NameRunTerminalCommand
)

type telegramAgentOpenAIMessage struct {
	Role       string                        `json:"role"`
	Content    any                           `json:"content,omitempty"`
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

type telegramAgentToolCallArgs = agenttools.CallArgs

type telegramAgentModelStatusBatchItem = agenttools.ModelStatusBatchItem

type telegramAgentOpenAIStreamReadResult struct {
	Usage            models.Usage
	FirstChunkTimeMs int
	ChunkTimeMs      int
	Size             int
	ToolCalls        []telegramAgentOpenAIToolCall
}

type telegramAgentToolResultPayload = agenttools.ResultPayload

func streamTelegramAgentReplyWithFunctionTools(ctx context.Context, cfg models.TelegramAgentConfig, pool telegramAgentModelProviderPool, history []chatMessage, prompt string, attachments []TelegramInputAttachment, chatID int64, onDelta streamDeltaHandler, onStatus streamStatusHandler) (telegramAgentReplyResult, error) {
	result := telegramAgentReplyResult{
		StartedAt: time.Now(),
	}
	if len(pool.Candidates) == 0 {
		return result, errors.New("当前 TG Agent 模型提供商暂不支持 function call 工具调用")
	}

	systemPrompt := appendTelegramAgentMemoryPrompt(ctx, cfg, cfg.SystemPrompt)
	if systemPrompt != "" {
		systemPrompt += "\n\n"
	}
	systemPrompt += telegramAgentFunctionToolSystemPrompt(ctx, cfg)

	messages := toTelegramAgentOpenAIMessages(systemPrompt, history, prompt, attachments)
	for round := 0; round < telegramAgentToolLoopMaxRounds; round++ {
		body, err := buildTelegramAgentOpenAIChatBody(ctx, cfg, messages, true, true)
		if err != nil {
			return result, err
		}

		attempt, err := performTelegramAgentProviderRequestWithRetry(ctx, pool, true, result.StartedAt, func(requestCtx context.Context, selected selectedModelProvider) ([]byte, context.Context, error) {
			return body, context.WithValue(requestCtx, consts.ContextKeyOpenAIEndpoint, "chat/completions"), nil
		})
		if err != nil {
			return result, err
		}
		selected := attempt.Selected
		res := attempt.Response
		result.Selected = selected
		result.RequestBody = append([]byte(nil), attempt.RequestBody...)
		if result.ProxyTimeMs == 0 {
			result.ProxyTimeMs = attempt.ProxyTimeMs
		}
		result.Retry = attempt.Retry

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
		messages, directFinalText, err = appendTelegramAgentToolResults(ctx, chatID, cfg, streamResult.ToolCalls, messages, onStatus)
		if err != nil {
			return result, err
		}
		if directFinalText != "" {
			if err := onDelta(directFinalText); err != nil {
				return result, err
			}
			normalizeTelegramAgentUsage(&result.Usage)
			return result, nil
		}
		if telegramAgentToolCallsNeedResultSummary(streamResult.ToolCalls) && onStatus != nil {
			if err := onStatus("脚本已执行完成，正在整理结果..."); err != nil {
				return result, err
			}
		}
	}

	return result, errors.New("工具调用轮次过多，请拆成更明确的指令")
}

func streamTelegramAgentPlainReplyWithPool(ctx context.Context, cfg models.TelegramAgentConfig, pool telegramAgentModelProviderPool, history []chatMessage, prompt string, onDelta streamDeltaHandler) (telegramAgentReplyResult, error) {
	result := telegramAgentReplyResult{
		StartedAt: time.Now(),
	}
	attempt, err := performTelegramAgentProviderRequestWithRetry(ctx, pool, true, result.StartedAt, func(requestCtx context.Context, selected selectedModelProvider) ([]byte, context.Context, error) {
		if selected.responseStyle() == consts.StyleOpenAI {
			messages := toTelegramAgentOpenAIMessages(appendTelegramAgentMemoryPrompt(ctx, cfg, cfg.SystemPrompt), history, prompt, nil)
			body, err := buildTelegramAgentOpenAIChatBody(ctx, cfg, messages, true, false)
			return body, context.WithValue(requestCtx, consts.ContextKeyOpenAIEndpoint, "chat/completions"), err
		}
		return buildTelegramAgentRequestBody(requestCtx, cfg, selected, history, prompt, nil)
	})
	if err != nil {
		return result, err
	}
	selected := attempt.Selected
	res := attempt.Response
	result.Selected = selected
	result.RequestBody = append([]byte(nil), attempt.RequestBody...)
	if result.ProxyTimeMs == 0 {
		result.ProxyTimeMs = attempt.ProxyTimeMs
	}
	result.Retry = attempt.Retry
	defer res.Body.Close()

	streamResult, err := readTelegramAgentStream(ctx, selected.responseStyle(), res.Body, result.StartedAt, onDelta)
	mergeTelegramAgentUsageAdd(&result.Usage, streamResult.Usage)
	if result.FirstChunkTimeMs == 0 {
		result.FirstChunkTimeMs = streamResult.FirstChunkTimeMs
	}
	result.ChunkTimeMs += streamResult.ChunkTimeMs
	result.Size += streamResult.Size
	normalizeTelegramAgentUsage(&result.Usage)
	return result, err
}

func appendTelegramAgentToolResults(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCalls []telegramAgentOpenAIToolCall, messages []telegramAgentOpenAIMessage, onStatus streamStatusHandler) ([]telegramAgentOpenAIMessage, string, error) {
	directFinalText := ""
	for _, toolCall := range toolCalls {
		if onStatus != nil {
			if err := onStatus(telegramAgentToolRunningStatus(toolCall)); err != nil {
				return messages, directFinalText, err
			}
		}
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
	return messages, directFinalText, nil
}

func telegramAgentToolRunningStatus(toolCall telegramAgentOpenAIToolCall) string {
	name := strings.TrimSpace(toolCall.Function.Name)
	if name == "" {
		name = "工具"
	}

	args := telegramAgentToolCallArgs{}
	if rawArgs := strings.TrimSpace(toolCall.Function.Arguments); rawArgs != "" {
		_ = json.Unmarshal([]byte(rawArgs), &args)
	}

	label := telegramAgentToolStatusLabel(name, args)
	action, detail := telegramAgentToolStatusAction(name, args)
	if detail != "" {
		return fmt.Sprintf("%s %s %s...", label, action, detail)
	}
	return fmt.Sprintf("%s %s...", label, action)
}

func telegramAgentToolStatusLabel(name string, args telegramAgentToolCallArgs) string {
	switch name {
	case telegramAgentToolRunTerminalCommand:
		if skill := strings.TrimSpace(args.Skill); skill != "" {
			return skill
		}
		if command := strings.TrimSpace(args.Command); command != "" {
			return command
		}
	case telegramAgentToolReadSkill:
		if skill := strings.TrimSpace(args.Skill); skill != "" {
			return skill
		}
	}
	return name
}

func telegramAgentToolStatusAction(name string, args telegramAgentToolCallArgs) (string, string) {
	if name == telegramAgentToolRunTerminalCommand {
		if telegramAgentToolLooksLikeVideoGeneration(args) {
			return "正在生成视频", telegramAgentToolGenerationPrompt(args)
		}
		if telegramAgentToolLooksLikeImageGeneration(args) {
			return "正在生成图片", telegramAgentToolGenerationPrompt(args)
		}
		if telegramAgentToolLooksLikeSearch(args) {
			return "正在搜索", telegramAgentToolSearchQuery(args)
		}
		return "正在运行", ""
	}

	switch name {
	case telegramAgentToolListSkills:
		if query := sanitizeTelegramAgentToolStatusValue(args.Query); query != "" {
			return "正在查找", query
		}
	case telegramAgentToolReadSkill:
		return "正在读取", ""
	case telegramAgentToolReadSystemLogs, telegramAgentToolReadRequestLogs:
		return "正在读取", telegramAgentToolStatusQuery(args)
	}

	if query := telegramAgentToolStatusQuery(args); query != "" {
		return "正在处理", query
	}
	return "正在运行", ""
}

func telegramAgentToolStatusQuery(args telegramAgentToolCallArgs) string {
	for _, value := range []string{
		args.Query,
		telegramAgentToolCommandQuery(args.CommandArgs),
		args.Target,
		args.Model,
		args.ProviderName,
		args.Skill,
		args.Task,
	} {
		value = sanitizeTelegramAgentToolStatusValue(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func telegramAgentToolCommandQuery(args []string) string {
	return telegramAgentToolCommandOptionValue(args, "--query", "-q")
}

func telegramAgentToolCommandPrompt(args []string) string {
	return telegramAgentToolCommandOptionValue(args, "--prompt", "-p")
}

func telegramAgentToolCommandOptionValue(args []string, names ...string) string {
	for index, arg := range args {
		trimmed := strings.TrimSpace(arg)
		for _, name := range names {
			if trimmed == name && index+1 < len(args) {
				return args[index+1]
			}
			if strings.HasPrefix(trimmed, name+"=") {
				return strings.TrimPrefix(trimmed, name+"=")
			}
		}
	}
	return ""
}

func telegramAgentToolGenerationPrompt(args telegramAgentToolCallArgs) string {
	for _, value := range []string{
		telegramAgentToolCommandPrompt(args.CommandArgs),
		args.Query,
		telegramAgentToolCommandQuery(args.CommandArgs),
	} {
		if value = sanitizeTelegramAgentToolStatusValue(value); value != "" {
			return value
		}
	}
	return ""
}

func telegramAgentToolSearchQuery(args telegramAgentToolCallArgs) string {
	for _, value := range []string{
		args.Query,
		telegramAgentToolCommandQuery(args.CommandArgs),
	} {
		if value = sanitizeTelegramAgentToolStatusValue(value); value != "" {
			return value
		}
	}
	return ""
}

func telegramAgentToolLooksLikeImageGeneration(args telegramAgentToolCallArgs) bool {
	return telegramAgentToolStatusTextContains(args,
		"image", "images", "img", "txt2img", "img2img", "z-image", "stable-diffusion", "sdxl", "flux",
		"图片", "图像", "生图", "绘图", "画图",
	)
}

func telegramAgentToolLooksLikeVideoGeneration(args telegramAgentToolCallArgs) bool {
	return telegramAgentToolStatusTextContains(args,
		"video", "vedio", "txt2vid", "img2vid", "text-to-video", "image-to-video", "generate_video", "agnes-video", "sora", "veo", "wan",
		"视频", "生视频", "生成视频",
	)
}

func telegramAgentToolLooksLikeSearch(args telegramAgentToolCallArgs) bool {
	return telegramAgentToolStatusTextContains(args,
		"search", "dual-search", "tavily", "google", "bing", "duckduckgo", "crawl", "weather",
		"搜索", "查询", "检索",
	)
}

func telegramAgentToolStatusTextContains(args telegramAgentToolCallArgs, keywords ...string) bool {
	text := strings.ToLower(strings.Join(append([]string{
		args.Skill,
		args.Command,
		args.WorkingDir,
	}, args.CommandArgs...), " "))
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func sanitizeTelegramAgentToolStatusValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) > 48 {
		runes := []rune(value)
		value = string(runes[:48]) + "..."
	}
	return value
}

func telegramAgentToolCallsNeedResultSummary(toolCalls []telegramAgentOpenAIToolCall) bool {
	for _, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.Function.Name) == telegramAgentToolRunTerminalCommand {
			return true
		}
	}
	return false
}

var telegramAgentProviderRequestExecutor = performTelegramAgentProviderRequestWithContext

func performTelegramAgentProviderRequestWithContext(ctx context.Context, selected selectedModelProvider, body []byte, stream bool, startedAt time.Time) (*http.Response, int, error) {
	upstreamBody := body
	upstreamCtx := ctx
	upstreamStyle := selected.ProviderStyle
	if selected.BridgePlan.Enabled {
		convertedBody, err := ifacebridge.ConvertRequestBody(selected.BridgePlan, body)
		if err != nil {
			return nil, 0, err
		}
		upstreamBody = convertedBody
		upstreamCtx = ifacebridge.ApplyUpstreamContext(ctx, selected.BridgePlan)
		upstreamStyle = selected.BridgePlan.UpstreamStyle()
	}

	provider, err := providers.NewForStyleWithProxy(upstreamStyle, selected.ProviderConfig, selected.ProviderProxy)
	if err != nil {
		return nil, 0, err
	}

	header := runtimesvc.BuildHeaders(nil, selected.WithHeader, selected.CustomerHeaders, stream)
	req, err := provider.BuildReq(upstreamCtx, header, selected.ProviderModel, upstreamBody)
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
	if res.StatusCode == http.StatusOK && selected.BridgePlan.Enabled {
		convertedRes, convertErr := ifacebridge.ConvertResponseBody(selected.BridgePlan, res, stream)
		if convertErr != nil {
			_ = res.Body.Close()
			return nil, 0, convertErr
		}
		res = convertedRes
	}
	return res, int(time.Since(startedAt).Milliseconds()), nil
}

func buildTelegramAgentOpenAIChatBody(ctx context.Context, cfg models.TelegramAgentConfig, messages []telegramAgentOpenAIMessage, stream bool, withTools bool) ([]byte, error) {
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
		payload["tools"] = telegramAgentFunctionTools(ctx, cfg)
		payload["tool_choice"] = "auto"
	}
	return json.Marshal(payload)
}

func toTelegramAgentOpenAIMessages(systemPrompt string, history []chatMessage, prompt string, attachments []TelegramInputAttachment) []telegramAgentOpenAIMessage {
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
	messages = append(messages, telegramAgentOpenAIMessage{Role: "user", Content: toOpenAIChatContent(prompt, attachments)})
	return messages
}

func readTelegramAgentOpenAIStreamWithTools(ctx context.Context, reader io.Reader, start time.Time, onDelta streamDeltaHandler) (telegramAgentOpenAIStreamReadResult, error) {
	var result telegramAgentOpenAIStreamReadResult
	var firstChunkSeen bool
	var contentBuffer strings.Builder
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

		gjson.Get(trimmed, "choices").ForEach(func(_, choice gjson.Result) bool {
			if text := choice.Get("delta.content").String(); text != "" {
				contentBuffer.WriteString(text)
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
		return nil
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
	if len(result.ToolCalls) == 0 && contentBuffer.Len() > 0 && onDelta != nil {
		if callbackErr := onDelta(contentBuffer.String()); callbackErr != nil {
			return result, callbackErr
		}
	}
	normalizeTelegramAgentUsage(&result.Usage)
	return result, err
}

func executeTelegramAgentFunctionToolCall(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCall telegramAgentOpenAIToolCall) string {
	name := strings.TrimSpace(toolCall.Function.Name)
	args := telegramAgentToolCallArgs{}
	rawArgs := strings.TrimSpace(toolCall.Function.Arguments)
	call := agenttools.FunctionCall{
		ID:   strings.TrimSpace(toolCall.ID),
		Name: name,
	}
	runtime := telegramAgentToolRuntime()
	logID := agenttools.RecordFunctionToolCallExecutingLog(ctx, runtime, chatID, call, rawArgs)
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			result := telegramAgentToolResult(false, "工具参数不是合法 JSON："+err.Error())
			agenttools.FinishFunctionToolCallLog(ctx, runtime, logID, chatID, call, rawArgs, result)
			return result
		}
	}

	definition, ok := findTelegramAgentFunctionToolDefinition(ctx, cfg, name)
	if !ok {
		result := telegramAgentToolResult(false, "未知工具："+name)
		agenttools.FinishFunctionToolCallLog(ctx, runtime, logID, chatID, call, rawArgs, result)
		return result
	}
	result := definition.Handler(ctx, chatID, cfg, args)
	agenttools.FinishFunctionToolCallLog(ctx, runtime, logID, chatID, call, rawArgs, result)
	return result
}

func telegramAgentToolResult(ok bool, text string) string {
	return agenttools.ToolResult(ok, text)
}

func telegramAgentToolFinalResult(ok bool, text string) string {
	return agenttools.ToolFinalResult(ok, text)
}

func telegramAgentToolDirectFinalText(raw string) (string, bool) {
	return agenttools.DirectFinalText(raw)
}

func telegramAgentFunctionToolSystemPrompt(ctx context.Context, cfg models.TelegramAgentConfig) string {
	lines := []string{
		"你可以通过 function call 调用 Orvion 项目管理工具。",
		"当用户想查看模型/提供商、读取系统日志/请求日志、新增或修改 API Key、修改模型/提供商状态、查看或修改提供商配置时，必须优先调用工具，不要自己猜测结果。",
		"当用户想查看系统状态、慢 SQL、请求成功率、图片缓存、后台任务状态，或要求手动触发价格同步/日志清理/token 回填/图片缓存补充时，必须调用对应系统管理工具。",
		"用户提到报错、错误日志、慢响应、请求失败、最近日志、timeout、panic、SQL 等排查诉求时，优先调用日志读取工具。",
		"修改提供商配置时，provider 的 config 字段使用 config_updates 做局部修改，例如 base_url、api_key；删除配置字段使用 remove_config_keys。",
		"新增或修改 API Key 时，allow_all=false 表示限制模型；如用户给的是模型关键词，请用 model_keywords 批量匹配模型，不要把“claude 的模型”当成单个模型名。",
		"用户要求查看、新增、修改、启用、禁用或立即执行 Agent 定时任务时，必须调用对应定时任务工具；不要只口头说明。",
		"不要在普通回复中泄露 api_key、token、secret、password 等敏感配置值。",
		"Skills 使用严格三段式渐进加载：系统提示只提供启用 Skill 的 name/description 元数据；需要某个 Skill 时必须先调用 read_skill 加载 SKILL.md 正文和资源路径；需要读取 references/ 或 scripts/ 中的文件、执行脚本或写入文件时，再调用 run_terminal_command。",
		"用户提到 skills、技能、脚本、自动化能力包、本地能力扩展，或请求匹配下方 Skill 元数据能力时，如果 Skills 工具可用，优先调用 list_skills/read_skill；不要在未 read_skill 前编造脚本参数或脚本结果。",
		"run_terminal_command 使用结构化参数 command + command_args + working_dir；不要把整段 shell 文本塞进 command，也不要使用 bash -c/sh -c/zsh -c。",
		"用户要求创建、写入、修改、删除文件时，必须使用 run_terminal_command 完成文件操作；不要只口头说明已生成。",
		"如果 run_terminal_command 生成了需要发给用户的图片或文件，请在最终回复中使用附件标记：[orvion:image:/绝对路径或URL|可选说明] 或 [orvion:file:/绝对路径或URL|可选说明]；不要把这个标记放进代码块。",
		"用户说“claude 的相关模型”“所有 claude 模型”“claude 那批模型”这类表达时，target 应为 claude，bulk 应为 true。",
		"用户一句话里包含多个模型启停动作时，例如“禁用 claude 并开启 deepseek”，必须调用 set_models_status_batch，并把每个动作分别放进 items；不要只处理其中一个动作。",
		"如果用户在同一句话中要求修改后继续检查某些模型或提供商状态，修改工具执行后还要继续调用查看工具，不要只总结修改结果。",
		"完成工具调用后，用简体中文简洁总结工具结果。",
	}
	if catalog := telegramAgentSkillMetadataPrompt(ctx, cfg); catalog != "" {
		lines = append(lines, "", catalog)
	}
	lines = append(lines, "修改类工具会直接执行数据库更新；工具返回执行结果后，请直接告知用户结果。")
	return strings.Join(lines, "\n")
}

func telegramAgentFunctionTools(ctx context.Context, cfg models.TelegramAgentConfig) []map[string]any {
	definitions := telegramAgentFunctionToolDefinitions(ctx, cfg)
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
	if required == nil {
		required = []string{}
	}
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
