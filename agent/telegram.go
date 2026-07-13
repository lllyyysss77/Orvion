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
	"net/url"
	"strings"
	"sync"
	"time"

	agentintent "github.com/racio/orvion/agent/intent"
	agenttools "github.com/racio/orvion/agent/tools"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/service/ifacebridge"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultTelegramAgentMaxHistoryMessages = 20
	defaultTelegramAgentMaxTokens          = 2048
	defaultTelegramAgentEditInterval       = 1200 * time.Millisecond
	telegramAgentTypingInterval            = 4 * time.Second
	telegramAgentMessageSoftLimit          = 3600
	telegramAgentSSEMaxLineBytes           = 1024 * 1024
	telegramAgentAPIErrorMessage           = "ai接口调用出错，请到https://mork.de5.net中查看问题"
)

// TelegramClient 是 TG Agent 需要的最小 Telegram 能力。
// service 层负责把具体 bot API 适配进来，避免 agent 依赖 service 包。
type TelegramClient interface {
	SendMessage(ctx context.Context, chatID int64, text string) (int64, error)
	EditMessage(ctx context.Context, chatID int64, messageID int64, text string) error
	SendTyping(ctx context.Context, chatID int64) error
}

type TelegramAttachmentClient interface {
	SendPhoto(ctx context.Context, chatID int64, source string, caption string) error
	SendDocument(ctx context.Context, chatID int64, source string, caption string) error
}

type TelegramMessageDeleter interface {
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
}

type TelegramMessage struct {
	ChatID      int64
	MessageID   int64
	Text        string
	Attachments []TelegramInputAttachment
}

type TelegramRuntimeResetResult struct {
	ConversationID  string
	ClearedSessions int
}

type TelegramInputAttachment struct {
	FileName string
	MIMEType string
	Data     []byte
}

type chatMessage struct {
	Role        string
	Content     string
	Attachments []TelegramInputAttachment
}

type chatSession struct {
	mu             sync.Mutex
	controlMu      sync.Mutex
	running        bool
	stopRequested  bool
	conversationID string
	messages       []chatMessage
}

type selectedModelProvider struct {
	ModelName       string
	ProviderModel   string
	ProviderName    string
	ProviderConfig  string
	ProviderProxy   string
	ProviderStyle   string
	ClientStyle     string
	BridgePlan      ifacebridge.Plan
	WithHeader      bool
	CustomerHeaders map[string]string
	TimeoutSeconds  int
}

type telegramAgentModelProviderPool struct {
	ModelName   string
	Candidates  map[uint]selectedModelProvider
	WeightItems map[uint]int
	MaxRetry    int
	Strategy    string
	Breaker     bool
}

type telegramAgentProviderAttempt struct {
	Selected    selectedModelProvider
	RequestBody []byte
	Response    *http.Response
	ProxyTimeMs int
	Retry       int
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
type telegramAgentStopChecker func() bool

func telegramAgentNeverStop() bool {
	return false
}

type telegramAgentReplyResult struct {
	Selected         selectedModelProvider
	RequestBody      []byte
	Usage            models.Usage
	StartedAt        time.Time
	ProxyTimeMs      int
	FirstChunkTimeMs int
	ChunkTimeMs      int
	Size             int
	Retry            int
}

var telegramSessions sync.Map

var errTelegramAgentReplyStopped = errors.New("TG Agent 当前回复已停止")

// HandleTelegramMessage 处理普通 TG 文本消息，并用消息编辑模拟流式回复。
func HandleTelegramMessage(ctx context.Context, client TelegramClient, message TelegramMessage) (bool, error) {
	if client == nil {
		return false, errors.New("telegram agent client is nil")
	}

	attachments := normalizeTelegramAgentInputAttachments(message.Attachments)
	raw := strings.TrimSpace(message.Text)
	if raw == "" && len(attachments) == 0 {
		return false, nil
	}
	if raw == "" {
		raw = defaultTelegramAgentImagePrompt
	}

	cfg, err := loadTelegramAgentConfig(ctx)
	if err != nil {
		return false, err
	}
	if !isTelegramAgentEnabled(cfg) {
		return false, nil
	}

	command, commandText := parseTelegramAgentCommand(raw)
	switch command {
	case "/new", "/reset":
		_, err := startNewTelegramConversation(ctx, message.ChatID)
		if err != nil {
			return true, err
		}
		_, err = client.SendMessage(ctx, message.ChatID, "已开启新的对话。")
		return true, err
	case "/img":
		imagePrompt := strings.TrimSpace(commandText)
		if imagePrompt == "" {
			_, err := client.SendMessage(ctx, message.ChatID, "请在 /img 后输入生图提示词，例如：/img 一只晒太阳的小猫")
			return true, err
		}
		return true, runTelegramAgentImageGenerationFunc(ctx, client, message.ChatID, imagePrompt, cfg, true)
	case "":
		if shouldBypassTelegramAgent(raw) {
			return false, nil
		}
		return true, runTelegramAgentConversation(ctx, client, message.ChatID, raw, attachments, cfg)
	default:
		return false, nil
	}
}

func runTelegramAgentConversation(ctx context.Context, client TelegramClient, chatID int64, prompt string, attachments []TelegramInputAttachment, cfg models.TelegramAgentConfig) error {
	return runTelegramAgentConversationWithHistoryMode(ctx, client, chatID, prompt, attachments, cfg, true)
}

func runTelegramAgentConversationWithHistoryMode(ctx context.Context, client TelegramClient, chatID int64, prompt string, attachments []TelegramInputAttachment, cfg models.TelegramAgentConfig, loadHistory bool) error {
	session := getTelegramSession(chatID)
	session.mu.Lock()
	defer session.mu.Unlock()
	beginTelegramSessionReply(session)
	defer finishTelegramSessionReply(session)

	attachments = normalizeTelegramAgentInputAttachments(attachments)
	if shouldHandleTelegramAgentImageIntent(ctx, cfg, prompt, len(attachments) > 0) {
		return runTelegramAgentImageGeneration(ctx, client, chatID, prompt, cfg, loadHistory)
	}

	placeholderID, err := client.SendMessage(ctx, chatID, "正在思考...")
	if err != nil {
		return err
	}
	stopTyping := startTelegramTypingLoop(ctx, client, chatID)
	defer stopTyping()

	history := make([]chatMessage, 0)
	if loadHistory {
		history, err = loadTelegramSessionMessages(ctx, chatID, cfg)
		if err != nil {
			return err
		}
	}
	var answer strings.Builder
	statusText := ""
	lastEditedAt := time.Time{}
	lastEditedText := ""

	edit := func(force bool) error {
		content := telegramAgentEditableMessageContent(answer.String(), statusText)
		content = trimTelegramMessage(content)
		if content == lastEditedText {
			return nil
		}
		if !force {
			if time.Since(lastEditedAt) < resolveTelegramAgentEditInterval(cfg) {
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

	_, err = streamTelegramAgentReply(ctx, cfg, history, prompt, attachments, chatID, func() bool {
		return telegramSessionReplyStopped(session)
	}, func(delta string) error {
		if delta == "" {
			return nil
		}
		statusText = ""
		answer.WriteString(delta)
		return edit(false)
	}, updateStatus)
	if err != nil {
		if errors.Is(err, errTelegramAgentReplyStopped) {
			statusText = ""
			if editErr := client.EditMessage(ctx, chatID, placeholderID, "已停止当前回复。"); editErr != nil {
				return editErr
			}
			return nil
		}
		slog.Error("TG Agent 对话的所有 API 调用均失败", "chat_id", chatID, "error", err)
		if editErr := client.EditMessage(ctx, chatID, placeholderID, telegramAgentAPIErrorMessage); editErr != nil {
			return fmt.Errorf("%v; 编辑失败消息也失败: %w", err, editErr)
		}
		return err
	}

	rawFinalAnswer := strings.TrimSpace(answer.String())
	finalAnswer, outputAttachments := extractTelegramAgentAttachments(rawFinalAnswer)
	if finalAnswer == "" {
		if len(outputAttachments) > 0 {
			finalAnswer = "已生成附件。"
		} else {
			finalAnswer = "上游返回了空响应。"
		}
	}
	answer.Reset()
	answer.WriteString(finalAnswer)
	statusText = ""
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
	if err := sendTelegramAgentAttachments(ctx, client, chatID, outputAttachments); err != nil {
		return err
	}

	historyPrompt := buildTelegramAgentHistoryPrompt(prompt, attachments)
	nextHistory := trimTelegramHistory(append(history,
		chatMessage{Role: "user", Content: historyPrompt},
		chatMessage{Role: "assistant", Content: finalAnswer},
	), cfg)
	session.messages = nextHistory
	if err := saveTelegramSessionMessages(ctx, chatID, nextHistory); err != nil {
		slog.Warn("保存 TG Agent 上下文失败", "chat_id", chatID, "error", err)
	}
	if loadHistory {
		scheduleTelegramAgentMemoryExtraction(cfg, historyPrompt, finalAnswer, time.Now())
	}
	return nil
}

func shouldHandleTelegramAgentImageIntent(ctx context.Context, cfg models.TelegramAgentConfig, prompt string, hasAttachment bool) bool {
	if cfg.IntentRulesEnabled == nil || !*cfg.IntentRulesEnabled {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(cfg.IntentEngine), "ai") && strings.TrimSpace(cfg.IntentModel) != "" {
		intent, err := classifyTelegramAgentImageIntentWithAI(ctx, cfg, prompt, hasAttachment)
		if err == nil {
			return intent
		}
		slog.Warn("TG Agent AI 规则引擎调用失败，回退本地规则", "model", cfg.IntentModel, "error", err)
	}
	result := agentintent.DetectTextToImage(prompt, hasAttachment)
	return result.Intent == agentintent.IntentTextToImage
}

func classifyTelegramAgentImageIntentWithAI(ctx context.Context, cfg models.TelegramAgentConfig, prompt string, hasAttachment bool) (bool, error) {
	if hasAttachment {
		return false, nil
	}
	classifierCfg := cfg
	classifierCfg.Model = strings.TrimSpace(cfg.IntentModel)
	classifierCfg.SystemPrompt = "你是意图分类器。判断用户是否明确要求立即生成一张新图片。只输出 txt2img 或 chat；能力咨询、规则讨论、搜索图片、识图、否定请求均输出 chat。"
	classifierCfg.MaxTokens = 8
	temperature := float64(0)
	classifierCfg.Temperature = &temperature
	classifierCfg.MemoryEnabled = boolPtr(false)
	pool, err := buildTelegramAgentDirectProviderPool(classifierCfg, false)
	if err != nil {
		return false, err
	}
	var answer strings.Builder
	_, err = streamTelegramAgentPlainReplyWithPool(ctx, classifierCfg, pool, nil, prompt, func(delta string) error {
		answer.WriteString(delta)
		return nil
	})
	if err != nil {
		return false, err
	}
	result := strings.ToLower(strings.TrimSpace(answer.String()))
	if strings.Contains(result, agentintent.IntentTextToImage) {
		return true, nil
	}
	if strings.Contains(result, agentintent.IntentChat) {
		return false, nil
	}
	return false, fmt.Errorf("AI 规则引擎返回无法识别的结果: %q", result)
}

func telegramAgentEditableMessageContent(answer string, status string) string {
	if status = strings.TrimSpace(status); status != "" {
		return status
	}
	if answer = strings.TrimSpace(answer); answer != "" {
		return answer
	}
	return "正在思考..."
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

func streamTelegramAgentReply(ctx context.Context, cfg models.TelegramAgentConfig, history []chatMessage, prompt string, attachments []TelegramInputAttachment, chatID int64, shouldStop telegramAgentStopChecker, onDelta streamDeltaHandler, onStatus streamStatusHandler) (telegramAgentReplyResult, error) {
	result := telegramAgentReplyResult{
		StartedAt: time.Now(),
	}
	if shouldStop == nil {
		shouldStop = telegramAgentNeverStop
	}

	pool, err := buildTelegramAgentDirectProviderPool(cfg, false)
	if err != nil {
		return result, err
	}

	if toolPool, ok := pool.functionToolPool(); ok {
		return streamTelegramAgentReplyWithFunctionTools(ctx, cfg, toolPool, history, prompt, attachments, chatID, shouldStop, onDelta, onStatus)
	}
	if shouldStop() {
		return result, errTelegramAgentReplyStopped
	}

	attempt, err := performTelegramAgentProviderRequestWithRetry(ctx, pool, true, result.StartedAt, func(requestCtx context.Context, selected selectedModelProvider) ([]byte, context.Context, error) {
		return buildTelegramAgentRequestBody(requestCtx, cfg, selected, history, prompt, attachments)
	})
	if err != nil {
		return result, err
	}
	selected := attempt.Selected
	res := attempt.Response
	defer res.Body.Close()
	result.Selected = selected
	result.RequestBody = append([]byte(nil), attempt.RequestBody...)
	result.ProxyTimeMs = attempt.ProxyTimeMs
	result.Retry = attempt.Retry

	streamResult, err := readTelegramAgentStream(ctx, selected.responseStyle(), res.Body, result.StartedAt, onDelta)
	result.Usage = streamResult.Usage
	result.FirstChunkTimeMs = streamResult.FirstChunkTimeMs
	result.ChunkTimeMs = streamResult.ChunkTimeMs
	result.Size = streamResult.Size
	return result, err
}

func buildTelegramAgentDirectProviderPool(cfg models.TelegramAgentConfig, requireFunctionTools bool) (telegramAgentModelProviderPool, error) {
	baseURL, err := normalizeTelegramAgentDirectBaseURL(cfg.BaseURL)
	if err != nil {
		return telegramAgentModelProviderPool{}, err
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	modelName := strings.TrimSpace(cfg.Model)
	if apiKey == "" {
		return telegramAgentModelProviderPool{}, errors.New("TG Agent 直连 API Key 不能为空")
	}
	if modelName == "" {
		return telegramAgentModelProviderPool{}, errors.New("TG Agent 直连模型名不能为空")
	}

	providerConfig, err := json.Marshal(map[string]string{
		"base_url": baseURL,
		"api_key":  apiKey,
	})
	if err != nil {
		return telegramAgentModelProviderPool{}, err
	}

	selected := selectedModelProvider{
		ModelName:       modelName,
		ProviderModel:   modelName,
		ProviderName:    "TG Agent 直连",
		ProviderConfig:  string(providerConfig),
		ProviderStyle:   consts.StyleOpenAI,
		ClientStyle:     consts.StyleOpenAI,
		TimeoutSeconds:  90,
		CustomerHeaders: map[string]string{},
	}
	if requireFunctionTools && !selected.supportsFunctionTools() {
		return telegramAgentModelProviderPool{}, errors.New("TG Agent 直连模型不支持 function call")
	}

	const directProviderID uint = 1
	return telegramAgentModelProviderPool{
		ModelName:   modelName,
		Candidates:  map[uint]selectedModelProvider{directProviderID: selected},
		WeightItems: map[uint]int{directProviderID: 1},
		MaxRetry:    3,
		Strategy:    consts.BalancerRotor,
		Breaker:     false,
	}, nil
}

func normalizeTelegramAgentDirectBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("TG Agent 直连请求 URL 不能为空")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("TG Agent 直连请求 URL 无效: %w", err)
	}
	if strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", errors.New("TG Agent 直连请求 URL 必须包含 scheme 和 host")
	}

	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/chat/completions") {
		path = strings.TrimSuffix(path, "/chat/completions")
	}
	if strings.HasSuffix(path, "/images/generations") {
		path = strings.TrimSuffix(path, "/images/generations")
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (pool telegramAgentModelProviderPool) functionToolPool() (telegramAgentModelProviderPool, bool) {
	candidates := make(map[uint]selectedModelProvider)
	weightItems := make(map[uint]int)
	for id, selected := range pool.Candidates {
		if !selected.supportsFunctionTools() {
			continue
		}
		candidates[id] = selected
		weightItems[id] = pool.WeightItems[id]
	}
	if len(candidates) == 0 {
		return telegramAgentModelProviderPool{}, false
	}
	next := pool
	next.Candidates = candidates
	next.WeightItems = weightItems
	return next, true
}

func cloneTelegramAgentWeightItems(items map[uint]int) map[uint]int {
	cloned := make(map[uint]int, len(items))
	for key, value := range items {
		cloned[key] = value
	}
	return cloned
}

func performTelegramAgentProviderRequestWithRetry(
	ctx context.Context,
	pool telegramAgentModelProviderPool,
	stream bool,
	startedAt time.Time,
	buildBody func(context.Context, selectedModelProvider) ([]byte, context.Context, error),
) (telegramAgentProviderAttempt, error) {
	maxRetry := pool.MaxRetry
	if maxRetry <= 0 {
		maxRetry = 1
	}
	balancer := runtimesvc.NewBalancer(pool.Strategy, pool.Breaker, cloneTelegramAgentWeightItems(pool.WeightItems))
	const perProviderMaxAttempts = 2

	var lastErr error
	attemptIndex := 0
	for attemptIndex < maxRetry {
		select {
		case <-ctx.Done():
			return telegramAgentProviderAttempt{}, ctx.Err()
		default:
		}

		id, err := balancer.Pop()
		if err != nil {
			if lastErr != nil {
				return telegramAgentProviderAttempt{}, fmt.Errorf("TG Agent 模型 %s 所有可用提供商重试失败: %w", pool.ModelName, lastErr)
			}
			return telegramAgentProviderAttempt{}, err
		}
		selected, ok := pool.Candidates[id]
		if !ok {
			balancer.Delete(id)
			continue
		}

		lastStatus := 0
		lastWas429 := false
		for providerAttempt := 0; providerAttempt < perProviderMaxAttempts && attemptIndex < maxRetry; providerAttempt++ {
			retry := attemptIndex
			attemptIndex++

			body, endpointCtx, err := buildBody(ctx, selected)
			if err != nil {
				lastErr = fmt.Errorf("%s/%s 构建请求失败: %w", selected.ProviderName, selected.ProviderModel, err)
				lastStatus = 0
				lastWas429 = false
				break
			}

			res, proxyMs, err := telegramAgentProviderRequestExecutor(endpointCtx, selected, body, stream, startedAt)
			if err != nil {
				lastErr = fmt.Errorf("%s/%s 请求失败: %w", selected.ProviderName, selected.ProviderModel, err)
				lastStatus = 0
				lastWas429 = false
				continue
			}

			if res.StatusCode != http.StatusOK {
				lastStatus = res.StatusCode
				lastWas429 = res.StatusCode == http.StatusTooManyRequests
				limitedBody, _ := io.ReadAll(io.LimitReader(res.Body, runtimesvc.MaxLogBodyBytes))
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
				lastErr = fmt.Errorf("%s/%s 返回状态 %d: %s", selected.ProviderName, selected.ProviderModel, res.StatusCode, strings.TrimSpace(runtimesvc.SafeBodyTextForLog(res, limitedBody)))
				continue
			}

			if err := runtimesvc.ValidateSuccessfulResponseBody(res, stream); err != nil {
				_ = res.Body.Close()
				lastErr = fmt.Errorf("%s/%s 响应无效: %w", selected.ProviderName, selected.ProviderModel, err)
				lastStatus = 0
				lastWas429 = false
				continue
			}

			balancer.Success(id)
			return telegramAgentProviderAttempt{
				Selected:    selected,
				RequestBody: append([]byte(nil), body...),
				Response:    res,
				ProxyTimeMs: proxyMs,
				Retry:       retry,
			}, nil
		}

		if lastWas429 {
			balancer.Reduce(id)
		} else {
			_ = lastStatus
			balancer.Delete(id)
		}
	}

	if lastErr != nil {
		return telegramAgentProviderAttempt{}, fmt.Errorf("TG Agent 模型 %s 所有可用提供商重试失败: %w", pool.ModelName, lastErr)
	}
	return telegramAgentProviderAttempt{}, fmt.Errorf("TG Agent 模型 %s 没有可用提供商完成请求", pool.ModelName)
}

func buildTelegramAgentRequestBody(ctx context.Context, cfg models.TelegramAgentConfig, selected selectedModelProvider, history []chatMessage, prompt string, attachments []TelegramInputAttachment) ([]byte, context.Context, error) {
	systemPrompt := appendTelegramAgentMemoryPrompt(ctx, cfg, cfg.SystemPrompt)
	messages := append([]chatMessage(nil), history...)
	messages = append(messages, chatMessage{Role: "user", Content: prompt, Attachments: normalizeTelegramAgentInputAttachments(attachments)})
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
		SkillsDir:          agenttools.DefaultSkillsDir,
		MemoryEnabled:      boolPtr(true),
		IntentRulesEnabled: boolPtr(false),
		IntentEngine:       "local",
	}

	if models.DB == nil {
		return cfg, nil
	}

	var config models.Config
	err := models.DB.WithContext(ctx).Where(models.ColumnEquals("key"), models.KeyTelegramAgent).First(&config).Error
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
	if strings.TrimSpace(override.BaseURL) != "" {
		base.BaseURL = strings.TrimSpace(override.BaseURL)
	}
	if strings.TrimSpace(override.APIKey) != "" {
		base.APIKey = strings.TrimSpace(override.APIKey)
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
	if override.SkillsEnabled != nil {
		base.SkillsEnabled = override.SkillsEnabled
	}
	if override.MemoryEnabled != nil {
		base.MemoryEnabled = override.MemoryEnabled
	}
	if override.IntentRulesEnabled != nil {
		base.IntentRulesEnabled = override.IntentRulesEnabled
	}
	if engine := strings.ToLower(strings.TrimSpace(override.IntentEngine)); engine == "local" || engine == "ai" {
		base.IntentEngine = engine
	}
	if strings.TrimSpace(override.IntentModel) != "" {
		base.IntentModel = strings.TrimSpace(override.IntentModel)
	}
	if strings.TrimSpace(override.ImageModel) != "" {
		base.ImageModel = strings.TrimSpace(override.ImageModel)
	}
	return base
}

func isTelegramAgentEnabled(cfg models.TelegramAgentConfig) bool {
	return cfg.Enabled == nil || *cfg.Enabled
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
	switch agenttools.NormalizeToolControl(raw) {
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

func beginTelegramSessionReply(session *chatSession) {
	session.controlMu.Lock()
	session.running = true
	session.stopRequested = false
	session.controlMu.Unlock()
}

func finishTelegramSessionReply(session *chatSession) {
	session.controlMu.Lock()
	session.running = false
	session.stopRequested = false
	session.controlMu.Unlock()
}

func telegramSessionReplyStopped(session *chatSession) bool {
	session.controlMu.Lock()
	defer session.controlMu.Unlock()
	return session.stopRequested
}

func requestTelegramSessionReplyStop(session *chatSession) bool {
	session.controlMu.Lock()
	defer session.controlMu.Unlock()
	if !session.running {
		return false
	}
	session.stopRequested = true
	return true
}

func StopTelegramReply(chatID int64) bool {
	value, ok := telegramSessions.Load(chatID)
	if !ok {
		return false
	}
	session, ok := value.(*chatSession)
	if !ok {
		telegramSessions.Delete(chatID)
		return false
	}
	return requestTelegramSessionReplyStop(session)
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
	}
}

func ResetTelegramRuntime(ctx context.Context, chatID int64) (TelegramRuntimeResetResult, error) {
	clearedSessions := 0
	telegramSessions.Range(func(key any, value any) bool {
		session, ok := value.(*chatSession)
		if ok {
			requestTelegramSessionReplyStop(session)
			session.mu.Lock()
			session.conversationID = ""
			session.messages = nil
			session.mu.Unlock()
		}
		telegramSessions.Delete(key)
		clearedSessions++
		return true
	})

	conversationID, err := startNewTelegramConversation(ctx, chatID)
	if err != nil {
		return TelegramRuntimeResetResult{}, err
	}
	return TelegramRuntimeResetResult{
		ConversationID:  conversationID,
		ClearedSessions: clearedSessions,
	}, nil
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

	if models.DB == nil {
		return conversationID, nil
	}
	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "chat_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"conversation_id", "updated_at"}),
		}).Create(&models.TelegramAgentSession{
			ChatID:         chatID,
			ConversationID: conversationID,
		}).Error
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

func toOpenAIChatMessages(systemPrompt string, messages []chatMessage) []map[string]any {
	result := make([]map[string]any, 0, len(messages)+1)
	if strings.TrimSpace(systemPrompt) != "" {
		result = append(result, map[string]any{"role": "system", "content": systemPrompt})
	}
	for _, message := range messages {
		result = append(result, map[string]any{
			"role":    normalizeOpenAIRole(message.Role),
			"content": toOpenAIChatContent(message.Content, message.Attachments),
		})
	}
	return result
}

func toOpenAIResponsesInput(messages []chatMessage) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		result = append(result, map[string]any{
			"role":    normalizeOpenAIRole(message.Role),
			"content": toOpenAIResponsesContent(message.Content, message.Attachments),
		})
	}
	return result
}

func toAnthropicMessages(messages []chatMessage) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		role := "user"
		if strings.TrimSpace(strings.ToLower(message.Role)) == "assistant" {
			role = "assistant"
		}
		result = append(result, map[string]any{
			"role":    role,
			"content": toAnthropicMessageContent(message.Content, message.Attachments),
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
			"role":  role,
			"parts": toGeminiParts(message.Content, message.Attachments),
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

func boolPtr(value bool) *bool {
	return &value
}
