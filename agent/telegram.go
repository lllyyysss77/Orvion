package agent

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service/ifacebridge"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	envTelegramAgentEnabled      = "TG_AGENT_ENABLED"
	envTelegramAgentModel        = "TG_AGENT_MODEL"
	envTelegramAgentSystemPrompt = "TG_AGENT_SYSTEM_PROMPT"
	envTelegramAgentMaxHistory   = "TG_AGENT_MAX_HISTORY"
	envTelegramAgentMaxTokens    = "TG_AGENT_MAX_TOKENS"
	envTelegramAgentEditInterval = "TG_AGENT_EDIT_INTERVAL_MS"
	envTelegramAgentSkillsDir    = "TG_AGENT_SKILLS_DIR"

	defaultTelegramAgentMaxHistoryMessages  = 20
	defaultTelegramAgentMaxTokens           = 2048
	defaultTelegramAgentEditInterval        = 1200 * time.Millisecond
	telegramAgentTypingInterval             = 4 * time.Second
	telegramAgentMessageSoftLimit           = 3600
	telegramAgentSSEMaxLineBytes            = 1024 * 1024
	telegramAgentToolResultFollowupMaxBytes = 30 * 1024
	telegramAgentAuthKeyName                = "telegarm"
	telegramAgentRequestPath                = "/telegram/agent"
)

// TelegramClient 是 TG Agent 需要的最小 Telegram 能力。
// service 层负责把具体 bot API 适配进来，避免 agent 依赖 service 包。
type TelegramClient interface {
	SendMessage(ctx context.Context, chatID int64, text string) (int64, error)
	EditMessage(ctx context.Context, chatID int64, messageID int64, text string) error
	SendTyping(ctx context.Context, chatID int64) error
}

type TelegramMessage struct {
	ChatID    int64
	MessageID int64
	Text      string
}

type chatMessage struct {
	Role    string
	Content string
}

type chatSession struct {
	mu             sync.Mutex
	conversationID string
	messages       []chatMessage
}

type selectedModelProvider struct {
	ModelName       string
	ProviderModel   string
	ProviderName    string
	ModelProviderID uint
	ProviderConfig  string
	ProviderProxy   string
	ProviderStyle   string
	ClientStyle     string
	BridgePlan      ifacebridge.Plan
	WithHeader      bool
	CustomerHeaders map[string]string
	TimeoutSeconds  int
	IOLog           bool
}

func (selected selectedModelProvider) responseStyle() string {
	if strings.TrimSpace(selected.ClientStyle) != "" {
		return selected.ClientStyle
	}
	return selected.ProviderStyle
}

func (selected selectedModelProvider) supportsFunctionTools() bool {
	return selected.responseStyle() == consts.StyleOpenAI
}

type streamDeltaHandler func(delta string) error
type streamStatusHandler func(status string) error

type telegramAgentReplyResult struct {
	Selected         selectedModelProvider
	RequestBody      []byte
	RequestPath      string
	Usage            models.Usage
	StartedAt        time.Time
	ProxyTimeMs      int
	FirstChunkTimeMs int
	ChunkTimeMs      int
	Size             int
}

var (
	telegramSessions  sync.Map
	telegramAuthKeyMu sync.Mutex
)

// HandleTelegramMessage 处理普通 TG 文本消息，并用消息编辑模拟流式回复。
func HandleTelegramMessage(ctx context.Context, client TelegramClient, message TelegramMessage) (bool, error) {
	if client == nil {
		return false, errors.New("telegram agent client is nil")
	}

	raw := strings.TrimSpace(message.Text)
	if raw == "" {
		return false, nil
	}

	cfg, err := loadTelegramAgentConfig(ctx)
	if err != nil {
		return false, err
	}
	if !isTelegramAgentEnabled(cfg) {
		return false, nil
	}

	if handled, err := handleTelegramAgentToolMessage(ctx, client, message.ChatID, raw, cfg); handled {
		return true, err
	}

	command, commandText := parseTelegramAgentCommand(raw)
	switch command {
	case "/new", "/reset":
		conversationID, err := startNewTelegramConversation(ctx, message.ChatID)
		if err != nil {
			return true, err
		}
		_, err = client.SendMessage(ctx, message.ChatID, "已开启新的对话。\n会话 ID："+conversationID)
		return true, err
	case "/chat":
		if strings.TrimSpace(commandText) == "" {
			_, err := client.SendMessage(ctx, message.ChatID, "请在 /chat 后面输入要对话的内容。")
			return true, err
		}
		return true, runTelegramAgentConversation(ctx, client, message.ChatID, commandText, cfg)
	case "":
		if shouldBypassTelegramAgent(raw) {
			return false, nil
		}
		return true, runTelegramAgentConversation(ctx, client, message.ChatID, raw, cfg)
	default:
		return false, nil
	}
}

func runTelegramAgentConversation(ctx context.Context, client TelegramClient, chatID int64, prompt string, cfg models.TelegramAgentConfig) error {
	session := getTelegramSession(chatID)
	session.mu.Lock()
	defer session.mu.Unlock()

	placeholderID, err := client.SendMessage(ctx, chatID, "正在思考...")
	if err != nil {
		return err
	}
	stopTyping := startTelegramTypingLoop(ctx, client, chatID)
	defer stopTyping()

	history, err := loadTelegramSessionMessages(ctx, chatID, cfg)
	if err != nil {
		return err
	}
	var answer strings.Builder
	statusText := ""
	lastEditedAt := time.Time{}
	lastEditedText := ""

	edit := func(force bool) error {
		content := strings.TrimSpace(answer.String())
		if content == "" {
			content = strings.TrimSpace(statusText)
			if content == "" {
				content = "正在思考..."
			}
		}
		content = trimTelegramMessage(content)
		if !force {
			if time.Since(lastEditedAt) < resolveTelegramAgentEditInterval(cfg) {
				return nil
			}
			if content == lastEditedText {
				return nil
			}
		}
		if err := client.EditMessage(ctx, chatID, placeholderID, content); err != nil {
			return err
		}
		lastEditedAt = time.Now()
		lastEditedText = content
		return nil
	}
	updateStatus := func(status string) error {
		status = strings.TrimSpace(status)
		if status == "" {
			return nil
		}
		statusText = status
		return edit(true)
	}

	result, err := streamTelegramAgentReply(ctx, cfg, history, prompt, chatID, func(delta string) error {
		if delta == "" {
			return nil
		}
		answer.WriteString(delta)
		return edit(false)
	}, updateStatus)
	if err != nil {
		recordTelegramAgentLog(ctx, result, "", err)
		errorText := "对话失败：" + err.Error()
		if editErr := client.EditMessage(ctx, chatID, placeholderID, trimTelegramMessage(errorText)); editErr != nil {
			return fmt.Errorf("%v; 编辑失败消息也失败: %w", err, editErr)
		}
		return err
	}

	finalAnswer := strings.TrimSpace(answer.String())
	if finalAnswer == "" {
		finalAnswer = "上游返回了空响应。"
		answer.Reset()
		answer.WriteString(finalAnswer)
	}
	if err := edit(true); err != nil {
		return err
	}
	parts := splitTelegramMessage(finalAnswer)
	if len(parts) > 1 {
		for _, part := range parts[1:] {
			if strings.TrimSpace(part) == "" {
				continue
			}
			if _, sendErr := client.SendMessage(ctx, chatID, strings.TrimSpace(part)); sendErr != nil {
				return sendErr
			}
		}
	}

	nextHistory := trimTelegramHistory(append(history,
		chatMessage{Role: "user", Content: prompt},
		chatMessage{Role: "assistant", Content: finalAnswer},
	), cfg)
	session.messages = nextHistory
	if err := saveTelegramSessionMessages(ctx, chatID, nextHistory); err != nil {
		slog.Warn("保存 TG Agent 上下文失败", "chat_id", chatID, "error", err)
	}
	recordTelegramAgentLog(ctx, result, finalAnswer, nil)
	return nil
}

func runTelegramAgentToolResultFollowup(ctx context.Context, client TelegramClient, chatID int64, cfg models.TelegramAgentConfig, action telegramToolAction, toolResult string) error {
	session := getTelegramSession(chatID)
	session.mu.Lock()
	defer session.mu.Unlock()

	history, err := loadTelegramSessionMessages(ctx, chatID, cfg)
	if err != nil {
		return err
	}
	selected, err := selectTelegramAgentModelProvider(ctx, cfg)
	if err != nil {
		return err
	}

	placeholderID, err := client.SendMessage(ctx, chatID, "正在整理结果...")
	if err != nil {
		return err
	}
	stopTyping := startTelegramTypingLoop(ctx, client, chatID)
	defer stopTyping()

	prompt := buildTelegramAgentToolResultFollowupPrompt(action, toolResult)
	var answer strings.Builder
	lastEditedAt := time.Now()
	lastEditedText := "正在整理结果..."

	edit := func(force bool) error {
		content := strings.TrimSpace(answer.String())
		if content == "" {
			content = "正在整理结果..."
		}
		content = trimTelegramMessage(content)
		if !force {
			if time.Since(lastEditedAt) < resolveTelegramAgentEditInterval(cfg) {
				return nil
			}
			if content == lastEditedText {
				return nil
			}
		}
		if err := client.EditMessage(ctx, chatID, placeholderID, content); err != nil {
			return err
		}
		lastEditedAt = time.Now()
		lastEditedText = content
		return nil
	}

	replyResult, err := streamTelegramAgentPlainReplyWithSelected(ctx, cfg, selected, history, prompt, func(delta string) error {
		if delta == "" {
			return nil
		}
		answer.WriteString(delta)
		return edit(false)
	})
	if err != nil {
		recordTelegramAgentLog(ctx, replyResult, "", err)
		if fallbackErr := editTelegramAgentToolResultFallback(ctx, client, chatID, placeholderID, toolResult); fallbackErr != nil {
			return fmt.Errorf("%w；回退发送原始结果失败: %v", err, fallbackErr)
		}
		return nil
	}

	finalAnswer := strings.TrimSpace(answer.String())
	if finalAnswer == "" {
		finalAnswer = "工具已执行完成，但模型没有生成整理内容。"
		answer.Reset()
		answer.WriteString(finalAnswer)
	}
	if err := edit(true); err != nil {
		return err
	}
	parts := splitTelegramMessage(finalAnswer)
	if len(parts) > 1 {
		for _, part := range parts[1:] {
			if strings.TrimSpace(part) == "" {
				continue
			}
			if _, sendErr := client.SendMessage(ctx, chatID, strings.TrimSpace(part)); sendErr != nil {
				return sendErr
			}
		}
	}

	nextHistory := trimTelegramHistory(append(history,
		chatMessage{Role: "user", Content: "确认"},
		chatMessage{Role: "assistant", Content: finalAnswer},
	), cfg)
	if err := saveTelegramSessionMessages(ctx, chatID, nextHistory); err != nil {
		slog.Warn("保存 TG Agent 工具整理上下文失败", "chat_id", chatID, "error", err)
	}
	recordTelegramAgentLog(ctx, replyResult, finalAnswer, nil)
	return nil
}

func editTelegramAgentToolResultFallback(ctx context.Context, client TelegramClient, chatID int64, messageID int64, toolResult string) error {
	text := strings.TrimSpace(toolResult)
	if text == "" {
		text = "工具已执行完成，但没有输出。"
	}
	parts := splitTelegramMessage(text)
	if len(parts) == 0 {
		return nil
	}
	if err := client.EditMessage(ctx, chatID, messageID, trimTelegramMessage(parts[0])); err != nil {
		return err
	}
	for _, part := range parts[1:] {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if _, err := client.SendMessage(ctx, chatID, strings.TrimSpace(part)); err != nil {
			return err
		}
	}
	return nil
}

func buildTelegramAgentToolResultFollowupPrompt(action telegramToolAction, toolResult string) string {
	summary := strings.TrimSpace(action.Summary)
	if summary == "" {
		summary = string(action.Kind)
	}
	result := truncateTelegramSkillText(toolResult, telegramAgentToolResultFollowupMaxBytes)
	return strings.Join([]string{
		"用户刚刚确认执行本地工具操作。请基于工具执行结果，回答用户原始需求。",
		"要求：",
		"- 使用简体中文。",
		"- 不要原样粘贴 JSON，也不要输出内部字段名清单。",
		"- 如果结果包含搜索、天气、查询或脚本输出，请提炼关键信息，整理成易读摘要。",
		"- 可以保留重要数值、时间、来源名称和必要链接。",
		"- 如果结果表示失败或信息不足，请说明原因和下一步建议。",
		"",
		"已确认操作：",
		summary,
		"",
		"工具执行结果：",
		result,
	}, "\n")
}

func telegramAgentToolResultFollowupSystemPrompt() string {
	return strings.Join([]string{
		"你正在整理 Orvion TG Agent 的本地工具执行结果。",
		"你的任务是把工具输出转化为用户真正需要的自然语言答案。",
		"不要暴露内部工具调用细节、审计字段或无关 JSON 结构。",
	}, "\n")
}

func withTelegramAgentSystemPromptSuffix(cfg models.TelegramAgentConfig, suffix string) models.TelegramAgentConfig {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return cfg
	}
	base := strings.TrimSpace(cfg.SystemPrompt)
	if base == "" {
		cfg.SystemPrompt = suffix
		return cfg
	}
	cfg.SystemPrompt = base + "\n\n" + suffix
	return cfg
}

func startTelegramTypingLoop(ctx context.Context, client TelegramClient, chatID int64) func() {
	typingCtx, cancel := context.WithCancel(ctx)
	sendTyping := func() {
		if err := client.SendTyping(typingCtx, chatID); err != nil {
			slog.Debug("发送 TG 输入中状态失败", "chat_id", chatID, "error", err)
		}
	}

	done := make(chan struct{})
	pkg.GoSafe("agent.telegram_typing_loop", func() {
		defer close(done)
		sendTyping()
		ticker := time.NewTicker(telegramAgentTypingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				sendTyping()
			}
		}
	})

	return func() {
		cancel()
		<-done
	}
}

func streamTelegramAgentReply(ctx context.Context, cfg models.TelegramAgentConfig, history []chatMessage, prompt string, chatID int64, onDelta streamDeltaHandler, onStatus streamStatusHandler) (telegramAgentReplyResult, error) {
	result := telegramAgentReplyResult{
		StartedAt: time.Now(),
	}

	selected, err := selectTelegramAgentModelProvider(ctx, cfg)
	if err != nil {
		return result, err
	}
	result.Selected = selected

	if selected.supportsFunctionTools() {
		return streamTelegramAgentReplyWithFunctionTools(ctx, cfg, selected, history, prompt, chatID, onDelta, onStatus)
	}

	body, endpointCtx, err := buildTelegramAgentRequestBody(ctx, cfg, selected, history, prompt)
	if err != nil {
		return result, err
	}
	result.RequestBody = append([]byte(nil), body...)

	res, proxyMs, err := performTelegramAgentProviderRequestWithContext(endpointCtx, selected, body, true, result.StartedAt)
	if err != nil {
		return result, err
	}
	defer res.Body.Close()
	result.ProxyTimeMs = proxyMs

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		result.Size = len(body)
		return result, fmt.Errorf("%s/%s 返回状态 %d: %s", selected.ProviderName, selected.ProviderModel, res.StatusCode, strings.TrimSpace(string(body)))
	}

	streamResult, err := readTelegramAgentStream(ctx, selected.responseStyle(), res.Body, result.StartedAt, onDelta)
	result.Usage = streamResult.Usage
	result.FirstChunkTimeMs = streamResult.FirstChunkTimeMs
	result.ChunkTimeMs = streamResult.ChunkTimeMs
	result.Size = streamResult.Size
	return result, err
}

func selectTelegramAgentModelProvider(ctx context.Context, cfg models.TelegramAgentConfig) (selectedModelProvider, error) {
	if models.DB == nil {
		return selectedModelProvider{}, errors.New("数据库未初始化")
	}

	modelQuery := models.DB.WithContext(ctx).Where("status = ?", 1)
	if modelName := strings.TrimSpace(cfg.Model); modelName != "" {
		modelQuery = modelQuery.Where("name = ?", modelName)
	}

	var model models.Model
	if err := modelQuery.Order("id ASC").First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if strings.TrimSpace(cfg.Model) != "" {
				return selectedModelProvider{}, fmt.Errorf("未找到可用 TG Agent 模型：%s", cfg.Model)
			}
			return selectedModelProvider{}, errors.New("未找到可用 TG Agent 模型")
		}
		return selectedModelProvider{}, err
	}

	var associations []models.ModelWithProvider
	now := time.Now()
	if err := models.DB.WithContext(ctx).
		Where("model_id = ? AND status = ?", model.ID, 1).
		Where("(auto_disabled_until IS NULL OR auto_disabled_until <= ?)", now).
		Order("weight DESC, id ASC").
		Find(&associations).Error; err != nil {
		return selectedModelProvider{}, err
	}
	if len(associations) == 0 {
		return selectedModelProvider{}, fmt.Errorf("模型 %s 没有可用提供商", model.Name)
	}

	providerIDs := make([]uint, 0, len(associations))
	for _, item := range associations {
		providerIDs = append(providerIDs, item.ProviderID)
	}

	var providerList []models.Provider
	if err := models.DB.WithContext(ctx).Where("id IN ?", providerIDs).Find(&providerList).Error; err != nil {
		return selectedModelProvider{}, err
	}
	providerByID := make(map[uint]models.Provider, len(providerList))
	for _, provider := range providerList {
		providerByID[provider.ID] = provider
	}

	for _, association := range associations {
		provider, ok := providerByID[association.ProviderID]
		if !ok {
			continue
		}
		providerStyle, clientStyle, bridgePlan, ok := resolveTelegramAgentProviderInterface(provider)
		if !ok {
			continue
		}

		customHeaders := map[string]string{}
		if strings.TrimSpace(association.CustomerHeaders) != "" {
			if err := json.Unmarshal([]byte(association.CustomerHeaders), &customHeaders); err != nil {
				slog.Warn("解析 TG Agent 自定义请求头失败", "model_with_provider_id", association.ID, "error", err)
				customHeaders = map[string]string{}
			}
		}

		timeoutSeconds := model.TimeOut
		if timeoutSeconds <= 0 {
			timeoutSeconds = 90
		}

		return selectedModelProvider{
			ModelName:       model.Name,
			ProviderModel:   association.ProviderModel,
			ProviderName:    provider.Name,
			ModelProviderID: association.ID,
			ProviderConfig:  provider.Config,
			ProviderProxy:   provider.ProxyURL,
			ProviderStyle:   providerStyle,
			ClientStyle:     clientStyle,
			BridgePlan:      bridgePlan,
			WithHeader:      association.WithHeader == 1,
			CustomerHeaders: customHeaders,
			TimeoutSeconds:  timeoutSeconds,
			IOLog:           model.IOLog == 1,
		}, nil
	}

	return selectedModelProvider{}, fmt.Errorf("模型 %s 没有支持对话接口的可用提供商", model.Name)
}

func resolveTelegramAgentProviderInterface(provider models.Provider) (string, string, ifacebridge.Plan, bool) {
	providerStyle := providers.ResolveStyle("", provider.Config)
	if providerStyle == "" {
		providerStyle = consts.StyleOpenAI
	}

	if models.ProviderSupportsEndpoint(provider.Capabilities, "chat") {
		return providerStyle, providerStyle, ifacebridge.Plan{}, true
	}

	if plan, ok := ifacebridge.ResolvePlan(provider, "chat"); ok {
		upstreamStyle := plan.UpstreamStyle()
		if strings.TrimSpace(upstreamStyle) == "" {
			return "", "", ifacebridge.Plan{}, false
		}
		return upstreamStyle, consts.StyleOpenAI, plan, true
	}

	nativeEndpoint := telegramAgentNativeEndpointForStyle(providerStyle)
	if nativeEndpoint == "" || !models.ProviderSupportsEndpoint(provider.Capabilities, nativeEndpoint) {
		return "", "", ifacebridge.Plan{}, false
	}
	return providerStyle, providerStyle, ifacebridge.Plan{}, true
}

func telegramAgentNativeEndpointForStyle(style string) string {
	switch style {
	case consts.StyleOpenAI:
		return "chat"
	case consts.StyleOpenAIRes:
		return "responses"
	case consts.StyleAnthropic:
		return "messages"
	case consts.StyleGemini:
		return "chat"
	default:
		return ""
	}
}

func buildTelegramAgentRequestBody(ctx context.Context, cfg models.TelegramAgentConfig, selected selectedModelProvider, history []chatMessage, prompt string) ([]byte, context.Context, error) {
	systemPrompt := strings.TrimSpace(cfg.SystemPrompt)
	messages := append([]chatMessage(nil), history...)
	messages = append(messages, chatMessage{Role: "user", Content: prompt})
	maxTokens := resolveTelegramAgentMaxTokens(cfg)
	temperature := cfg.Temperature

	switch selected.ProviderStyle {
	case consts.StyleAnthropic:
		payload := map[string]any{
			"messages":   toAnthropicMessages(messages),
			"max_tokens": maxTokens,
			"stream":     true,
		}
		if systemPrompt != "" {
			payload["system"] = systemPrompt
		}
		if temperature != nil {
			payload["temperature"] = *temperature
		}
		body, err := json.Marshal(payload)
		return body, ctx, err
	case consts.StyleOpenAIRes:
		payload := map[string]any{
			"input":  toOpenAIResponsesInput(messages),
			"stream": true,
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
		body, err := json.Marshal(payload)
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
		body, err := json.Marshal(payload)
		return body, context.WithValue(ctx, consts.ContextKeyGeminiStream, true), err
	default:
		payload := map[string]any{
			"messages": toOpenAIChatMessages(systemPrompt, messages),
			"stream":   true,
			"stream_options": map[string]bool{
				"include_usage": true,
			},
		}
		if maxTokens > 0 {
			payload["max_tokens"] = maxTokens
		}
		if temperature != nil {
			payload["temperature"] = *temperature
		}
		body, err := json.Marshal(payload)
		return body, context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, "chat/completions"), err
	}
}

func readTelegramAgentStream(ctx context.Context, style string, reader io.Reader, start time.Time, onDelta streamDeltaHandler) (telegramAgentReplyResult, error) {
	var result telegramAgentReplyResult
	var firstChunkOnce sync.Once

	err := readSSEData(ctx, reader, func(data string, size int) error {
		result.Size += size
		if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
			return nil
		}
		firstChunkOnce.Do(func() {
			result.FirstChunkTimeMs = int(time.Since(start).Milliseconds())
		})
		mergeTelegramAgentUsage(style, &result.Usage, data)
		for _, delta := range extractTelegramAgentDelta(style, data) {
			if err := onDelta(delta); err != nil {
				return err
			}
		}
		return nil
	})
	if result.FirstChunkTimeMs > 0 {
		elapsedMs := int(time.Since(start).Milliseconds())
		if elapsedMs > result.FirstChunkTimeMs {
			result.ChunkTimeMs = elapsedMs - result.FirstChunkTimeMs
		}
	}
	normalizeTelegramAgentUsage(&result.Usage)
	return result, err
}

func readSSEData(ctx context.Context, reader io.Reader, onData func(data string, size int) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), telegramAgentSSEMaxLineBytes)

	dataLines := make([]string, 0, 4)
	dataSize := 0
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		size := dataSize
		dataSize = 0
		return onData(data, size)
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimRight(scanner.Text(), "\r")
		dataSize += len(scanner.Bytes())
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

func extractTelegramAgentDelta(style string, raw string) []string {
	switch style {
	case consts.StyleAnthropic:
		return compactStrings([]string{
			gjson.Get(raw, "delta.text").String(),
			gjson.Get(raw, "content_block.text").String(),
		})
	case consts.StyleOpenAIRes:
		return compactStrings([]string{
			gjson.Get(raw, "delta").String(),
			gjson.Get(raw, "output_text").String(),
			gjson.Get(raw, "response.output_text.delta").String(),
		})
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
		return compactStrings(parts)
	default:
		parts := make([]string, 0)
		gjson.Get(raw, "choices").ForEach(func(_, choice gjson.Result) bool {
			if text := choice.Get("delta.content").String(); text != "" {
				parts = append(parts, text)
			}
			if text := choice.Get("message.content").String(); text != "" {
				parts = append(parts, text)
			}
			return true
		})
		return compactStrings(parts)
	}
}

func recordTelegramAgentLog(ctx context.Context, result telegramAgentReplyResult, answer string, logErr error) {
	if models.DB == nil {
		return
	}
	if result.Selected.ModelName == "" && len(result.RequestBody) == 0 {
		return
	}

	authKeyID, err := ensureTelegramAgentAuthKey(ctx)
	if err != nil {
		slog.Warn("准备 TG Agent 内部 AuthKey 失败", "error", err)
	}

	usage := result.Usage
	if logErr == nil && usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		usage = estimateTelegramAgentUsage(result.Selected, result.RequestBody, answer)
	}
	normalizeTelegramAgentUsage(&usage)

	totalCost := 0.0
	if logErr == nil {
		totalCost = runtimesvc.CalculateTotalCost(ctx, result.Selected.ModelName, usage)
	}

	status := "success"
	errorText := ""
	if logErr != nil {
		status = "error"
		errorText = logErr.Error()
	}
	createdAt := result.StartedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	tps := 0.0
	if result.ChunkTimeMs > 0 && usage.TotalTokens > 0 {
		tps = float64(usage.TotalTokens) / (float64(result.ChunkTimeMs) / 1000)
	}

	chatIO := 0
	if result.Selected.IOLog {
		chatIO = 1
	}
	size := result.Size
	if size <= 0 {
		size = len(answer)
	}

	log := models.ChatLog{
		CreatedAt:           createdAt,
		Name:                result.Selected.ModelName,
		ProviderModel:       result.Selected.ProviderModel,
		ProviderName:        result.Selected.ProviderName,
		ModelWithProviderID: result.Selected.ModelProviderID,
		Status:              status,
		Style:               result.Selected.ProviderStyle,
		RequestPath:         resolveTelegramAgentRequestPath(result),
		UserAgent:           "telegram-agent",
		RemoteIP:            "telegram",
		AuthKeyID:           authKeyID,
		ChatIO:              chatIO,
		Error:               errorText,
		ProxyTimeMs:         result.ProxyTimeMs,
		FirstChunkTimeMs:    result.FirstChunkTimeMs,
		ChunkTimeMs:         result.ChunkTimeMs,
		Tps:                 tps,
		Size:                size,
		Usage:               usage,
	}

	ref, err := saveTelegramAgentChatLog(ctx, log)
	if err != nil {
		slog.Warn("保存 TG Agent 请求日志失败", "error", err)
		return
	}
	if chatIO == 1 {
		output := answer
		if output == "" && logErr != nil {
			output = logErr.Error()
		}
		if err := models.DB.WithContext(ctx).Create(&models.ChatIO{
			Input:        string(result.RequestBody),
			OutputString: output,
			LogId:        ref.ID,
			LogUUID:      ref.UUID,
		}).Error; err != nil {
			slog.Warn("保存 TG Agent IO 日志失败", "error", err, "log_uuid", ref.UUID)
		}
	}
	updateTelegramAgentAuthKeyStats(ctx, authKeyID, totalCost, time.Now())
	addTelegramAgentTotalConsumedAmount(ctx, totalCost)
}

func resolveTelegramAgentRequestPath(result telegramAgentReplyResult) string {
	if strings.TrimSpace(result.RequestPath) != "" {
		return strings.TrimSpace(result.RequestPath)
	}
	return telegramAgentRequestPath
}

func saveTelegramAgentChatLog(ctx context.Context, log models.ChatLog) (models.ChatLogRef, error) {
	if log.UUID == "" {
		uuid, err := pkg.GenerateRandomCharsKey(36)
		if err != nil {
			return models.ChatLogRef{}, err
		}
		log.UUID = uuid
	}

	for attempt := 0; attempt < 3; attempt++ {
		ref, err := models.CreateMonthlyChatLog(ctx, log)
		if err != nil {
			if strings.Contains(err.Error(), "SQLSTATE 23505") || strings.Contains(err.Error(), "UNIQUE constraint failed") {
				uuid, genErr := pkg.GenerateRandomCharsKey(36)
				if genErr != nil {
					return models.ChatLogRef{}, genErr
				}
				log.UUID = uuid
				continue
			}
			return models.ChatLogRef{}, err
		}
		return ref, nil
	}
	return models.ChatLogRef{}, errors.New("failed to generate unique telegram agent chat log uuid")
}

func ensureTelegramAgentAuthKey(ctx context.Context) (uint, error) {
	telegramAuthKeyMu.Lock()
	defer telegramAuthKeyMu.Unlock()

	var existing models.AuthKey
	err := models.DB.WithContext(ctx).
		Where("name = ?", telegramAgentAuthKeyName).
		Order("id ASC").
		First(&existing).Error
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}

	randomKey, err := pkg.GenerateRandomCharsKey(36)
	if err != nil {
		return 0, err
	}
	authKey := models.AuthKey{
		Name:     telegramAgentAuthKeyName,
		Key:      consts.KeyPrefix + randomKey,
		Status:   1,
		AllowAll: 1,
		Models:   "[]",
	}
	if err := models.DB.WithContext(ctx).Create(&authKey).Error; err != nil {
		return 0, err
	}
	return authKey.ID, nil
}

func updateTelegramAgentAuthKeyStats(ctx context.Context, authKeyID uint, cost float64, usedAt time.Time) {
	if authKeyID == 0 {
		return
	}
	updates := map[string]any{
		"usage_count":  gorm.Expr("COALESCE(usage_count, 0) + ?", 1),
		"last_used_at": usedAt,
	}
	if cost > 0 {
		updates["total_cost"] = gorm.Expr("COALESCE(total_cost, 0) + ?", cost)
	}
	if err := models.DB.WithContext(ctx).Model(&models.AuthKey{}).Where("id = ?", authKeyID).Updates(updates).Error; err != nil {
		slog.Warn("更新 TG Agent 内部 AuthKey 统计失败", "error", err, "auth_key_id", authKeyID)
	}
}

func addTelegramAgentTotalConsumedAmount(ctx context.Context, delta float64) {
	if delta <= 0 || models.DB == nil {
		return
	}

	var cfg models.Config
	err := models.DB.WithContext(ctx).Where("key = ?", models.KeyTotalConsumedAmount).First(&cfg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if createErr := models.DB.WithContext(ctx).Create(&models.Config{
				Key:   models.KeyTotalConsumedAmount,
				Value: strconv.FormatFloat(delta, 'f', 8, 64),
			}).Error; createErr != nil {
				slog.Warn("创建 TG Agent 总消耗配置失败", "error", createErr)
			}
			return
		}
		slog.Warn("读取 TG Agent 总消耗配置失败", "error", err)
		return
	}

	current, parseErr := strconv.ParseFloat(strings.TrimSpace(cfg.Value), 64)
	if parseErr != nil || current < 0 {
		current = 0
	}
	next := current + delta
	if err := models.DB.WithContext(ctx).
		Model(&models.Config{}).
		Where("id = ?", cfg.ID).
		Update("value", strconv.FormatFloat(next, 'f', 8, 64)).Error; err != nil {
		slog.Warn("更新 TG Agent 总消耗配置失败", "error", err)
	}
}

func mergeTelegramAgentUsage(style string, current *models.Usage, raw string) {
	switch style {
	case consts.StyleAnthropic:
		mergeTelegramAgentUsageMax(current, extractTelegramAgentAnthropicUsage(raw))
	case consts.StyleGemini:
		mergeTelegramAgentUsageMax(current, extractTelegramAgentGeminiUsage(raw))
	default:
		mergeTelegramAgentUsageMax(current, extractTelegramAgentOpenAIUsage(raw))
	}
}

func extractTelegramAgentOpenAIUsage(raw string) models.Usage {
	var usage models.Usage
	mergeTelegramAgentUsageMax(&usage, parseTelegramAgentOpenAIUsageNode(gjson.Parse(raw)))
	for _, path := range []string{"usage", "response.usage"} {
		node := gjson.Get(raw, path)
		if node.Exists() {
			mergeTelegramAgentUsageMax(&usage, parseTelegramAgentOpenAIUsageNode(node))
		}
	}
	normalizeTelegramAgentUsage(&usage)
	return usage
}

func parseTelegramAgentOpenAIUsageNode(node gjson.Result) models.Usage {
	if !node.Exists() || !node.IsObject() {
		return models.Usage{}
	}
	usage := models.Usage{
		PromptTokens:     node.Get("prompt_tokens").Int(),
		CompletionTokens: node.Get("completion_tokens").Int(),
		TotalTokens:      node.Get("total_tokens").Int(),
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = node.Get("input_tokens").Int()
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = node.Get("output_tokens").Int()
	}

	detailsNode := node.Get("prompt_tokens_details")
	if !detailsNode.Exists() {
		detailsNode = node.Get("input_tokens_details")
	}
	if detailsNode.Exists() && strings.TrimSpace(detailsNode.Raw) != "null" {
		usage.PromptTokensDetails = detailsNode.Raw
	}

	cachedTokens := node.Get("prompt_tokens_details.cached_tokens").Int()
	if cachedTokens == 0 {
		cachedTokens = node.Get("input_tokens_details.cached_tokens").Int()
	}
	usage.CachedTokens = normalizeTelegramAgentCachedTokens(cachedTokens)
	if usage.PromptTokensDetails == "" {
		usage.PromptTokensDetails = buildTelegramAgentPromptTokensDetailsJSON(usage.CachedTokens, 0, false)
	}
	normalizeTelegramAgentUsage(&usage)
	return usage
}

type telegramAgentAnthropicUsage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

func extractTelegramAgentAnthropicUsage(raw string) models.Usage {
	var usage telegramAgentAnthropicUsage
	for _, path := range []string{"usage", "message.usage"} {
		node := gjson.Get(raw, path)
		mergeTelegramAgentAnthropicUsage(&usage, node)
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheCreationTokens == 0 {
		mergeTelegramAgentAnthropicUsage(&usage, gjson.Parse(raw))
	}
	cacheRead := normalizeTelegramAgentCachedTokens(usage.CacheReadTokens)
	cacheWrite := normalizeTelegramAgentCachedTokens(usage.CacheCreationTokens)
	result := models.Usage{
		PromptTokens:        usage.InputTokens,
		CompletionTokens:    usage.OutputTokens,
		TotalTokens:         usage.InputTokens + usage.OutputTokens,
		CachedTokens:        cacheRead,
		PromptTokensDetails: buildTelegramAgentPromptTokensDetailsJSON(cacheRead, cacheWrite, true),
	}
	normalizeTelegramAgentUsage(&result)
	return result
}

func mergeTelegramAgentAnthropicUsage(current *telegramAgentAnthropicUsage, node gjson.Result) {
	if !node.Exists() || !node.IsObject() {
		return
	}
	if input := node.Get("input_tokens").Int(); input > current.InputTokens {
		current.InputTokens = input
	}
	if output := node.Get("output_tokens").Int(); output > current.OutputTokens {
		current.OutputTokens = output
	}
	if cacheRead := normalizeTelegramAgentCachedTokens(node.Get("cache_read_input_tokens").Int()); cacheRead > current.CacheReadTokens {
		current.CacheReadTokens = cacheRead
	}
	cacheCreation := extractTelegramAgentAnthropicCacheCreationTokens(node)
	if cacheCreation > current.CacheCreationTokens {
		current.CacheCreationTokens = cacheCreation
	}
}

func extractTelegramAgentAnthropicCacheCreationTokens(node gjson.Result) int64 {
	cacheCreation := node.Get("cache_creation")
	if cacheCreation.Exists() && cacheCreation.IsObject() {
		return normalizeTelegramAgentCachedTokens(cacheCreation.Get("ephemeral_5m_input_tokens").Int() +
			cacheCreation.Get("ephemeral_1h_input_tokens").Int())
	}
	flat := node.Get("claude_cache_creation_5_m_tokens").Int() + node.Get("claude_cache_creation_1_h_tokens").Int()
	if flat > 0 {
		return normalizeTelegramAgentCachedTokens(flat)
	}
	return normalizeTelegramAgentCachedTokens(node.Get("cache_creation_input_tokens").Int())
}

func extractTelegramAgentGeminiUsage(raw string) models.Usage {
	node := gjson.Get(raw, "usageMetadata")
	if !node.Exists() {
		node = gjson.Get(raw, "usage_metadata")
	}
	if !node.Exists() || !node.IsObject() {
		return models.Usage{}
	}
	usage := models.Usage{
		PromptTokens:     node.Get("promptTokenCount").Int(),
		CompletionTokens: node.Get("candidatesTokenCount").Int(),
		TotalTokens:      node.Get("totalTokenCount").Int(),
		CachedTokens:     normalizeTelegramAgentCachedTokens(node.Get("cachedContentTokenCount").Int()),
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = node.Get("prompt_token_count").Int()
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = node.Get("candidates_token_count").Int()
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = node.Get("total_token_count").Int()
	}
	if usage.CachedTokens == 0 {
		usage.CachedTokens = normalizeTelegramAgentCachedTokens(node.Get("cached_content_token_count").Int())
	}
	usage.PromptTokensDetails = buildTelegramAgentPromptTokensDetailsJSON(usage.CachedTokens, 0, false)
	normalizeTelegramAgentUsage(&usage)
	return usage
}

func mergeTelegramAgentUsageMax(current *models.Usage, candidate models.Usage) {
	if candidate.PromptTokens > current.PromptTokens {
		current.PromptTokens = candidate.PromptTokens
	}
	if candidate.CompletionTokens > current.CompletionTokens {
		current.CompletionTokens = candidate.CompletionTokens
	}
	if candidate.TotalTokens > current.TotalTokens {
		current.TotalTokens = candidate.TotalTokens
	}
	if candidate.CachedTokens > current.CachedTokens {
		current.CachedTokens = candidate.CachedTokens
	}
	if candidate.CacheHitRate > current.CacheHitRate {
		current.CacheHitRate = candidate.CacheHitRate
	}
	if candidate.PromptTokensDetails != "" {
		if current.PromptTokensDetails == "" || candidate.CachedTokens >= current.CachedTokens {
			current.PromptTokensDetails = candidate.PromptTokensDetails
		}
	}
	normalizeTelegramAgentUsage(current)
}

func mergeTelegramAgentUsageAdd(current *models.Usage, candidate models.Usage) {
	if current == nil {
		return
	}
	current.PromptTokens += candidate.PromptTokens
	current.CompletionTokens += candidate.CompletionTokens
	current.TotalTokens += candidate.TotalTokens
	current.CachedTokens += candidate.CachedTokens
	if candidate.PromptTokensDetails != "" {
		current.PromptTokensDetails = candidate.PromptTokensDetails
	}
	current.TotalCost += candidate.TotalCost
	current.CacheHitRate = calculateTelegramAgentCacheHitRate(current.PromptTokens, current.CachedTokens)
	normalizeTelegramAgentUsage(current)
}

func normalizeTelegramAgentUsage(usage *models.Usage) {
	if usage == nil {
		return
	}
	if usage.PromptTokens < 0 {
		usage.PromptTokens = 0
	}
	if usage.CompletionTokens < 0 {
		usage.CompletionTokens = 0
	}
	if usage.CachedTokens < 0 {
		usage.CachedTokens = 0
	}
	computedTotal := usage.PromptTokens + usage.CompletionTokens
	if usage.TotalTokens < computedTotal {
		usage.TotalTokens = computedTotal
	}
	if usage.CacheHitRate == 0 {
		usage.CacheHitRate = calculateTelegramAgentCacheHitRate(usage.PromptTokens, usage.CachedTokens)
	}
	if usage.PromptTokensDetails == "" {
		usage.PromptTokensDetails = buildTelegramAgentPromptTokensDetailsJSON(usage.CachedTokens, 0, false)
	}
}

func estimateTelegramAgentUsage(selected selectedModelProvider, requestBody []byte, answer string) models.Usage {
	inputText := extractTelegramAgentInputText(selected.responseStyle(), requestBody)
	if strings.TrimSpace(inputText) == "" {
		inputText = string(requestBody)
	}
	usage := models.Usage{
		PromptTokens:     estimateTelegramAgentTokens(inputText),
		CompletionTokens: estimateTelegramAgentTokens(answer),
	}
	normalizeTelegramAgentUsage(&usage)
	return usage
}

func extractTelegramAgentInputText(style string, raw []byte) string {
	var sb strings.Builder
	switch style {
	case consts.StyleOpenAIRes:
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "instructions"))
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "input"))
	case consts.StyleAnthropic:
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "system"))
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "messages"))
	case consts.StyleGemini:
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "system_instruction"))
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "contents"))
	default:
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "messages"))
		appendTelegramAgentTextFromAny(&sb, gjson.GetBytes(raw, "input"))
	}
	return sb.String()
}

func appendTelegramAgentTextFromAny(sb *strings.Builder, value gjson.Result) {
	if !value.Exists() {
		return
	}
	if value.Type == gjson.String {
		appendTelegramAgentText(sb, value.String())
		return
	}
	if value.IsArray() || value.IsObject() {
		value.ForEach(func(_, item gjson.Result) bool {
			appendTelegramAgentTextFromAny(sb, item)
			return true
		})
	}
}

func appendTelegramAgentText(sb *strings.Builder, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	sb.WriteString(text)
}

func estimateTelegramAgentTokens(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	asciiBytes := 0
	nonASCII := 0
	for _, r := range text {
		if r <= 0x7f {
			asciiBytes++
		} else {
			nonASCII++
		}
	}
	return int64((asciiBytes+3)/4 + nonASCII)
}

func normalizeTelegramAgentCachedTokens(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func calculateTelegramAgentCacheHitRate(promptTokens int64, cachedTokens int64) float64 {
	if promptTokens <= 0 || cachedTokens <= 0 {
		return 0
	}
	if cachedTokens > promptTokens {
		cachedTokens = promptTokens
	}
	return float64(cachedTokens) / float64(promptTokens) * 100
}

func buildTelegramAgentPromptTokensDetailsJSON(cachedTokens int64, cacheWriteTokens int64, promptExcludesCached bool) string {
	cachedTokens = normalizeTelegramAgentCachedTokens(cachedTokens)
	cacheWriteTokens = normalizeTelegramAgentCachedTokens(cacheWriteTokens)
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

func loadTelegramAgentConfig(ctx context.Context) (models.TelegramAgentConfig, error) {
	cfg := models.TelegramAgentConfig{
		Enabled:            boolPtr(true),
		MaxHistoryMessages: defaultTelegramAgentMaxHistoryMessages,
		MaxTokens:          defaultTelegramAgentMaxTokens,
		EditIntervalMs:     int(defaultTelegramAgentEditInterval / time.Millisecond),
		SystemPrompt:       "你是 Orvion 的 Telegram 对话助手。请用简体中文回答，保持简洁、准确、友好。",
		SkillsEnabled:      boolPtr(false),
		SkillsDir:          telegramAgentDefaultSkillsDir,
	}

	if modelName := strings.TrimSpace(os.Getenv(envTelegramAgentModel)); modelName != "" {
		cfg.Model = modelName
	}
	if prompt := strings.TrimSpace(os.Getenv(envTelegramAgentSystemPrompt)); prompt != "" {
		cfg.SystemPrompt = prompt
	}
	if enabledRaw := strings.TrimSpace(os.Getenv(envTelegramAgentEnabled)); enabledRaw != "" {
		cfg.Enabled = boolPtr(parseBoolDefault(enabledRaw, true))
	}
	if maxHistory := parsePositiveEnvInt(envTelegramAgentMaxHistory); maxHistory > 0 {
		cfg.MaxHistoryMessages = maxHistory
	}
	if maxTokens := parsePositiveEnvInt(envTelegramAgentMaxTokens); maxTokens > 0 {
		cfg.MaxTokens = maxTokens
	}
	if editInterval := parsePositiveEnvInt(envTelegramAgentEditInterval); editInterval > 0 {
		cfg.EditIntervalMs = editInterval
	}
	if skillsDir := strings.TrimSpace(os.Getenv(envTelegramAgentSkillsDir)); skillsDir != "" {
		cfg.SkillsDir = skillsDir
	}

	if models.DB == nil {
		return cfg, nil
	}

	var config models.Config
	err := models.DB.WithContext(ctx).Where("key = ?", models.KeyTelegramAgent).First(&config).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cfg, nil
		}
		return cfg, err
	}

	raw := strings.TrimSpace(config.Value)
	if raw == "" {
		return cfg, nil
	}

	var stored models.TelegramAgentConfig
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return cfg, fmt.Errorf("解析 TG Agent 配置失败: %w", err)
	}
	return mergeTelegramAgentConfig(cfg, stored), nil
}

func LoadTelegramAgentConfig(ctx context.Context) (models.TelegramAgentConfig, error) {
	return loadTelegramAgentConfig(ctx)
}

func mergeTelegramAgentConfig(base models.TelegramAgentConfig, override models.TelegramAgentConfig) models.TelegramAgentConfig {
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if strings.TrimSpace(override.Model) != "" {
		base.Model = strings.TrimSpace(override.Model)
	}
	if strings.TrimSpace(override.SystemPrompt) != "" {
		base.SystemPrompt = strings.TrimSpace(override.SystemPrompt)
	}
	if override.MaxHistoryMessages > 0 {
		base.MaxHistoryMessages = override.MaxHistoryMessages
	}
	if override.MaxTokens > 0 {
		base.MaxTokens = override.MaxTokens
	}
	if override.Temperature != nil {
		base.Temperature = override.Temperature
	}
	if override.EditIntervalMs > 0 {
		base.EditIntervalMs = override.EditIntervalMs
	}
	if override.ToolConfirmationRequired != nil {
		base.ToolConfirmationRequired = override.ToolConfirmationRequired
	}
	if override.SkillsEnabled != nil {
		base.SkillsEnabled = override.SkillsEnabled
	}
	if strings.TrimSpace(override.SkillsEmbeddingModel) != "" {
		base.SkillsEmbeddingModel = strings.TrimSpace(override.SkillsEmbeddingModel)
	}
	return base
}

func isTelegramAgentEnabled(cfg models.TelegramAgentConfig) bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func telegramAgentRequiresToolConfirmation(cfg models.TelegramAgentConfig) bool {
	return cfg.ToolConfirmationRequired == nil || *cfg.ToolConfirmationRequired
}

func parseTelegramAgentCommand(raw string) (command string, text string) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return "", ""
	}
	first := strings.ToLower(fields[0])
	if !strings.HasPrefix(first, "/") {
		return "", raw
	}
	if idx := strings.Index(first, "@"); idx > 0 {
		first = first[:idx]
	}
	remaining := strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))
	return first, remaining
}

func shouldBypassTelegramAgent(raw string) bool {
	switch normalizeTelegramToolControl(raw) {
	case "/status", "状态", "系统状态", "查看状态", "查看系统状态":
		return true
	default:
		return false
	}
}

func getTelegramSession(chatID int64) *chatSession {
	value, _ := telegramSessions.LoadOrStore(chatID, &chatSession{})
	return value.(*chatSession)
}

func ForgetTelegramConversation(chatID int64, conversationID string) {
	conversationID = strings.TrimSpace(conversationID)
	value, ok := telegramSessions.Load(chatID)
	if !ok {
		return
	}
	session, ok := value.(*chatSession)
	if !ok {
		telegramSessions.Delete(chatID)
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.conversationID == conversationID {
		session.conversationID = ""
		session.messages = nil
		telegramSessions.Delete(chatID)
		telegramPendingToolActions.Delete(chatID)
	}
}

func loadTelegramSessionMessages(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig) ([]chatMessage, error) {
	session := getTelegramSession(chatID)
	if models.DB == nil {
		return append([]chatMessage(nil), session.messages...), nil
	}

	conversationID, err := resolveTelegramActiveConversationID(ctx, chatID, session)
	if err != nil {
		return nil, err
	}

	limit := resolveTelegramAgentHistoryLimit(cfg)
	rows := make([]models.TelegramAgentMessage, 0, limit)
	err = models.DB.WithContext(ctx).
		Where("chat_id = ? AND conversation_id = ?", chatID, conversationID).
		Order("message_order DESC, id DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		session.messages = nil
		return nil, nil
	}

	messages := make([]chatMessage, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		role := normalizeTelegramAgentHistoryRole(rows[i].Role)
		content := strings.TrimSpace(rows[i].Content)
		if content == "" {
			continue
		}
		messages = append(messages, chatMessage{Role: role, Content: content})
	}
	session.messages = append([]chatMessage(nil), messages...)
	return messages, nil
}

func saveTelegramSessionMessages(ctx context.Context, chatID int64, messages []chatMessage) error {
	session := getTelegramSession(chatID)
	if models.DB == nil {
		session.messages = append([]chatMessage(nil), messages...)
		return nil
	}

	conversationID, err := resolveTelegramActiveConversationID(ctx, chatID, session)
	if err != nil {
		return err
	}

	trimmed := append([]chatMessage(nil), messages...)
	return models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("chat_id = ? AND conversation_id = ?", chatID, conversationID).Delete(&models.TelegramAgentMessage{}).Error; err != nil {
			return err
		}
		rows := make([]models.TelegramAgentMessage, 0, len(trimmed))
		for index, message := range trimmed {
			content := strings.TrimSpace(message.Content)
			if content == "" {
				continue
			}
			rows = append(rows, models.TelegramAgentMessage{
				ChatID:         chatID,
				ConversationID: conversationID,
				MessageOrder:   index,
				Role:           normalizeTelegramAgentHistoryRole(message.Role),
				Content:        content,
			})
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

func startNewTelegramConversation(ctx context.Context, chatID int64) (string, error) {
	session := getTelegramSession(chatID)
	session.mu.Lock()
	defer session.mu.Unlock()

	conversationID := newTelegramConversationID(chatID)
	session.conversationID = conversationID
	session.messages = nil
	telegramPendingToolActions.Delete(chatID)

	if models.DB == nil {
		return conversationID, nil
	}
	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "chat_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"conversation_id", "updated_at"}),
		}).Create(&models.TelegramAgentSession{
			ChatID:         chatID,
			ConversationID: conversationID,
		}).Error; err != nil {
			return err
		}
		return tx.Where("chat_id = ?", chatID).Delete(&models.TelegramAgentPendingAction{}).Error
	})
	return conversationID, err
}

func resolveTelegramActiveConversationID(ctx context.Context, chatID int64, session *chatSession) (string, error) {
	if session != nil && strings.TrimSpace(session.conversationID) != "" {
		return session.conversationID, nil
	}

	if models.DB == nil {
		conversationID := newTelegramConversationID(chatID)
		if session != nil {
			session.conversationID = conversationID
		}
		return conversationID, nil
	}

	var row models.TelegramAgentSession
	err := models.DB.WithContext(ctx).Where("chat_id = ?", chatID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = models.TelegramAgentSession{
			ChatID:         chatID,
			ConversationID: newTelegramConversationID(chatID),
		}
		if createErr := models.DB.WithContext(ctx).Create(&row).Error; createErr != nil {
			if retryErr := models.DB.WithContext(ctx).Where("chat_id = ?", chatID).First(&row).Error; retryErr != nil {
				return "", createErr
			}
		}
	} else if err != nil {
		return "", err
	}

	if strings.TrimSpace(row.ConversationID) == "" {
		row.ConversationID = newTelegramConversationID(chatID)
		if err := models.DB.WithContext(ctx).Model(&models.TelegramAgentSession{}).Where("chat_id = ?", chatID).Update("conversation_id", row.ConversationID).Error; err != nil {
			return "", err
		}
	}
	if session != nil {
		session.conversationID = row.ConversationID
	}
	return row.ConversationID, nil
}

func newTelegramConversationID(chatID int64) string {
	randomBytes := make([]byte, 8)
	if _, err := cryptorand.Read(randomBytes); err == nil {
		return fmt.Sprintf("tg-%d-%s", chatID, hex.EncodeToString(randomBytes))
	}
	return fmt.Sprintf("tg-%d-%d", chatID, time.Now().UnixNano())
}

func resolveTelegramAgentHistoryLimit(cfg models.TelegramAgentConfig) int {
	limit := cfg.MaxHistoryMessages
	if limit <= 0 {
		limit = defaultTelegramAgentMaxHistoryMessages
	}
	return limit
}

func normalizeTelegramAgentHistoryRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}

func trimTelegramHistory(messages []chatMessage, cfg models.TelegramAgentConfig) []chatMessage {
	limit := resolveTelegramAgentHistoryLimit(cfg)
	if len(messages) <= limit {
		return messages
	}
	return append([]chatMessage(nil), messages[len(messages)-limit:]...)
}

func resolveTelegramAgentMaxTokens(cfg models.TelegramAgentConfig) int {
	if cfg.MaxTokens > 0 {
		return cfg.MaxTokens
	}
	return defaultTelegramAgentMaxTokens
}

func resolveTelegramAgentEditInterval(cfg models.TelegramAgentConfig) time.Duration {
	if cfg.EditIntervalMs > 0 {
		return time.Duration(cfg.EditIntervalMs) * time.Millisecond
	}
	return defaultTelegramAgentEditInterval
}

func toOpenAIChatMessages(systemPrompt string, messages []chatMessage) []map[string]string {
	result := make([]map[string]string, 0, len(messages)+1)
	if strings.TrimSpace(systemPrompt) != "" {
		result = append(result, map[string]string{"role": "system", "content": systemPrompt})
	}
	for _, message := range messages {
		result = append(result, map[string]string{
			"role":    normalizeOpenAIRole(message.Role),
			"content": message.Content,
		})
	}
	return result
}

func toOpenAIResponsesInput(messages []chatMessage) []map[string]string {
	result := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		result = append(result, map[string]string{
			"role":    normalizeOpenAIRole(message.Role),
			"content": message.Content,
		})
	}
	return result
}

func toAnthropicMessages(messages []chatMessage) []map[string]string {
	result := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		role := "user"
		if strings.TrimSpace(strings.ToLower(message.Role)) == "assistant" {
			role = "assistant"
		}
		result = append(result, map[string]string{
			"role":    role,
			"content": message.Content,
		})
	}
	return result
}

func toGeminiContents(messages []chatMessage) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		role := "user"
		if strings.TrimSpace(strings.ToLower(message.Role)) == "assistant" {
			role = "model"
		}
		result = append(result, map[string]any{
			"role": role,
			"parts": []map[string]string{
				{"text": message.Content},
			},
		})
	}
	return result
}

func normalizeOpenAIRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}

func splitTelegramMessage(raw string) []string {
	runes := []rune(raw)
	if len(runes) <= telegramAgentMessageSoftLimit {
		return []string{raw}
	}

	parts := make([]string, 0, len(runes)/telegramAgentMessageSoftLimit+1)
	for len(runes) > 0 {
		size := telegramAgentMessageSoftLimit
		if len(runes) < size {
			size = len(runes)
		}
		parts = append(parts, string(runes[:size]))
		runes = runes[size:]
	}
	return parts
}

func trimTelegramMessage(raw string) string {
	runes := []rune(raw)
	if len(runes) <= telegramAgentMessageSoftLimit {
		return raw
	}
	return string(runes[:telegramAgentMessageSoftLimit]) + "\n\n（后续内容将在生成完成后继续发送）"
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func parsePositiveEnvInt(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func parseBoolDefault(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes", "y":
		return true
	case "0", "false", "off", "no", "n":
		return false
	default:
		return fallback
	}
}

func boolPtr(value bool) *bool {
	return &value
}
