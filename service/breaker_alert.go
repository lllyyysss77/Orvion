package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/racio/orvion/balancers"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	envTelegramBotToken = "BREAKER_ALERT_TG_BOT_TOKEN"
	envTelegramChatID   = "BREAKER_ALERT_TG_CHAT_ID"
	envTelegramAPIBase  = "BREAKER_ALERT_TG_API_BASE"
	envTelegramProxyURL = "BREAKER_ALERT_TG_PROXY_URL"
	// TG /status 图片拉取地址（可选，支持通过系统配置 breaker_alert_tg.status_image_url 覆盖）。
	envTelegramStatusImageURL = "BREAKER_ALERT_TG_STATUS_IMAGE_URL"

	telegramDefaultAPIBase         = "https://api.telegram.org"
	telegramHTTPTimeout            = 25 * time.Second
	telegramPhotoHTTPTimeout       = 35 * time.Second
	telegramRequestRetryMaxAttempt = 3
	telegramRequestRetryDelay      = 800 * time.Millisecond
)

var ErrTelegramNotifierNotConfigured = errors.New("telegram 告警未启用或配置不完整")

type telegramNotifier struct {
	endpoint string
	chatID   string
	client   *http.Client
	apiBase  string
	botToken string
}

type ModelProviderAutoDisableAlertEvent struct {
	ModelWithProviderID uint
	ResumeAt            time.Time
	Threshold           int
	Window              time.Duration
}

type modelProviderAutoDisableAlertDetail struct {
	ModelName     string `gorm:"column:model_name"`
	ProviderName  string `gorm:"column:provider_name"`
	ProviderModel string `gorm:"column:provider_model"`
}

type telegramSendMessageRequest struct {
	ChatID      string `json:"chat_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type telegramSendPhotoRequest struct {
	ChatID  string `json:"chat_id"`
	Photo   string `json:"photo"`
	Caption string `json:"caption,omitempty"`
}

type telegramSendDocumentRequest struct {
	ChatID   string `json:"chat_id"`
	Document string `json:"document"`
	Caption  string `json:"caption,omitempty"`
}

type telegramEditMessageTextRequest struct {
	ChatID      int64  `json:"chat_id"`
	MessageID   int64  `json:"message_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type telegramSendChatActionRequest struct {
	ChatID string `json:"chat_id"`
	Action string `json:"action"`
}

type telegramAnswerCallbackQueryRequest struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

type telegramSetMyCommandsRequest struct {
	Commands []telegramBotCommand `json:"commands"`
}

type telegramBotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type telegramSendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

type telegramSendMessageWithResultResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

// InitBreakerAlertNotifier 初始化熔断告警通知（Telegram）。
// 优先读取系统配置（configs.key=breaker_alert_tg），未配置时回退环境变量。
func InitBreakerAlertNotifier() {
	balancers.SetBreakerOpenHook(func(event balancers.BreakerOpenEvent) {
		notifier, ok, err := resolveTelegramNotifier(context.Background())
		if err != nil {
			slog.Warn("加载 Telegram 告警配置失败", "error", err)
			return
		}
		if !ok {
			return
		}
		if err := notifier.SendBreakerOpen(event); err != nil {
			slog.Warn("发送熔断告警失败", "error", err, "model_with_provider_id", event.Key)
		}
	})

	notifier, ok, err := resolveTelegramNotifier(context.Background())
	if err != nil {
		slog.Warn("初始化 Telegram 告警失败", "error", err)
		return
	}
	if ok && notifier != nil {
		slog.Info("已启用熔断 Telegram 告警")
	}
}

func resolveTelegramNotifier(ctx context.Context) (*telegramNotifier, bool, error) {
	cfg, found, err := loadTelegramBreakerAlertConfig(ctx)
	if err != nil {
		return nil, false, err
	}
	if found {
		return buildTelegramNotifier(cfg.BotToken, cfg.ChatID, cfg.APIBase, cfg.ProxyURL, cfg.Enabled)
	}
	return buildTelegramNotifierFromEnv()
}

func loadTelegramBreakerAlertConfig(ctx context.Context) (models.TelegramBreakerAlertConfig, bool, error) {
	config, err := gorm.G[models.Config](models.DB).Where("key = ?", models.KeyTelegramBreakerAlert).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.TelegramBreakerAlertConfig{}, false, nil
		}
		return models.TelegramBreakerAlertConfig{}, false, err
	}

	raw := strings.TrimSpace(config.Value)
	if raw == "" {
		return models.TelegramBreakerAlertConfig{}, true, nil
	}

	var cfg models.TelegramBreakerAlertConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return models.TelegramBreakerAlertConfig{}, true, fmt.Errorf("解析 TG 告警配置失败: %w", err)
	}
	return cfg, true, nil
}

func buildTelegramNotifierFromEnv() (*telegramNotifier, bool, error) {
	botToken := strings.TrimSpace(os.Getenv(envTelegramBotToken))
	chatID := strings.TrimSpace(os.Getenv(envTelegramChatID))
	apiBase := strings.TrimSpace(os.Getenv(envTelegramAPIBase))
	proxyURL := strings.TrimSpace(os.Getenv(envTelegramProxyURL))
	return buildTelegramNotifier(botToken, chatID, apiBase, proxyURL, true)
}

func buildTelegramNotifier(botToken string, chatID string, apiBase string, proxyURL string, enabled bool) (*telegramNotifier, bool, error) {
	if !enabled {
		return nil, false, nil
	}
	botToken = strings.TrimSpace(botToken)
	chatID = strings.TrimSpace(chatID)
	if botToken == "" || chatID == "" {
		return nil, false, nil
	}

	apiBase = strings.TrimSpace(apiBase)
	if apiBase == "" {
		apiBase = telegramDefaultAPIBase
	}
	apiBase = strings.TrimRight(apiBase, "/")

	httpClient, err := buildTelegramHTTPClient(proxyURL)
	if err != nil {
		return nil, false, err
	}
	return &telegramNotifier{
		endpoint: fmt.Sprintf("%s/bot%s/sendMessage", apiBase, botToken),
		chatID:   chatID,
		client:   httpClient,
		apiBase:  apiBase,
		botToken: botToken,
	}, true, nil
}

func buildTelegramHTTPClient(proxyURL string) (*http.Client, error) {
	return buildTelegramHTTPClientWithTimeout(proxyURL, telegramHTTPTimeout)
}

func buildTelegramHTTPClientWithTimeout(proxyURL string, timeout time.Duration) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if timeout <= 0 {
		timeout = telegramHTTPTimeout
	}
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	if err := validateTelegramProxyURL(proxyURL); err != nil {
		return nil, err
	}
	parsedProxyURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理 URL 无效: %w", err)
	}

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("默认 HTTP transport 类型不受支持")
	}
	transport := defaultTransport.Clone()
	transport.Proxy = http.ProxyURL(parsedProxyURL)

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}, nil
}

func validateTelegramProxyURL(proxyURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil {
		return fmt.Errorf("代理 URL 无效: %w", err)
	}
	if strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("代理 URL 缺少 scheme 或 host")
	}
	return nil
}

// SendTelegramBreakerAlertTest 发送 TG 熔断告警测试消息。
func SendTelegramBreakerAlertTest(ctx context.Context) error {
	notifier, ok, err := resolveTelegramNotifier(ctx)
	if err != nil {
		return err
	}
	if !ok || notifier == nil {
		return ErrTelegramNotifierNotConfigured
	}

	testContent := strings.Join([]string{
		"【Orvion TG 告警测试】",
		fmt.Sprintf("时间：%s", time.Now().Format("2006-01-02 15:04:05")),
		"状态：测试消息发送成功",
		"说明：这是一条来自系统配置页面的测试消息。",
	}, "\n")

	return notifier.sendText(testContent)
}

func (n *telegramNotifier) SendBreakerOpen(event balancers.BreakerOpenEvent) error {
	content := strings.Join([]string{
		"【Orvion 熔断告警】",
		fmt.Sprintf("时间：%s", event.At.Format("2006-01-02 15:04:05")),
		fmt.Sprintf("模型关联ID：%d", event.Key),
		fmt.Sprintf("前置状态：%s", event.PrevState.String()),
		fmt.Sprintf("触发原因：%s", event.Reason),
		fmt.Sprintf("失败计数：%d", event.FailCount),
		fmt.Sprintf("冷却结束：%s", event.OpenUntil.Format("2006-01-02 15:04:05")),
	}, "\n")
	return n.sendText(content)
}

func SendModelProviderAutoDisableAlert(ctx context.Context, event ModelProviderAutoDisableAlertEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	notifier, ok, err := resolveTelegramNotifier(ctx)
	if err != nil {
		return err
	}
	if !ok || notifier == nil {
		return ErrTelegramNotifierNotConfigured
	}

	detail := loadModelProviderAutoDisableAlertDetail(ctx, event.ModelWithProviderID)
	lines := []string{
		"【Orvion 模型提供商熔断】",
		fmt.Sprintf("时间：%s", time.Now().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("模型关联ID：%d", event.ModelWithProviderID),
	}
	if strings.TrimSpace(detail.ModelName) != "" {
		lines = append(lines, fmt.Sprintf("模型：%s", detail.ModelName))
	}
	if strings.TrimSpace(detail.ProviderName) != "" || strings.TrimSpace(detail.ProviderModel) != "" {
		providerText := strings.TrimSpace(detail.ProviderName)
		if providerText == "" {
			providerText = "未知提供商"
		}
		if providerModel := strings.TrimSpace(detail.ProviderModel); providerModel != "" {
			providerText += " / " + providerModel
		}
		lines = append(lines, fmt.Sprintf("提供商：%s", providerText))
	}
	lines = append(lines,
		fmt.Sprintf("触发原因：检测窗口内错误达到 %d 次", event.Threshold),
		fmt.Sprintf("检测窗口：%s", event.Window.String()),
		fmt.Sprintf("恢复时间：%s", event.ResumeAt.Format("2006-01-02 15:04:05")),
	)

	return sendTelegramCaptionWithStatusImage(ctx, notifier, notifier.chatID, strings.Join(lines, "\n"))
}

func loadModelProviderAutoDisableAlertDetail(ctx context.Context, modelWithProviderID uint) modelProviderAutoDisableAlertDetail {
	if modelWithProviderID == 0 || models.DB == nil {
		return modelProviderAutoDisableAlertDetail{}
	}

	var detail modelProviderAutoDisableAlertDetail
	if err := models.DB.WithContext(ctx).
		Table("model_with_providers").
		Select("models.name AS model_name, providers.name AS provider_name, model_with_providers.provider_model AS provider_model").
		Joins("LEFT JOIN models ON models.id = model_with_providers.model_id").
		Joins("LEFT JOIN providers ON providers.id = model_with_providers.provider_id").
		Where("model_with_providers.id = ?", modelWithProviderID).
		Scan(&detail).Error; err != nil {
		slog.Warn("读取模型提供商熔断告警详情失败", "error", err, "model_with_provider_id", modelWithProviderID)
		return modelProviderAutoDisableAlertDetail{}
	}
	return detail
}

func (n *telegramNotifier) sendText(content string) error {
	return n.sendTextWithMarkupToChat(n.chatID, content, nil)
}

func (n *telegramNotifier) sendTextWithMarkupToChat(chatID string, content string, replyMarkup any) error {
	payload := telegramSendMessageRequest{
		ChatID:      chatID,
		Text:        content,
		ReplyMarkup: replyMarkup,
	}
	if err := n.postTelegramMethod(context.Background(), "sendMessage", payload); err != nil {
		slog.Warn("发送 TG 文本消息失败", "chat_id", strings.TrimSpace(chatID), "text_bytes", len(content), "error", err)
		return err
	}
	slog.Info("已发送 TG 文本消息", "chat_id", strings.TrimSpace(chatID), "text_bytes", len(content))
	return nil
}

func (n *telegramNotifier) sendTextToChatAndReturnMessageID(ctx context.Context, chatID string, content string) (int64, error) {
	return n.sendTextToChatAndReturnMessageIDWithParseMode(ctx, chatID, content, "")
}

func (n *telegramNotifier) sendMarkdownTextToChatAndReturnMessageID(ctx context.Context, chatID string, content string) (int64, error) {
	messageID, err := n.sendTextToChatAndReturnMessageIDWithParseMode(ctx, chatID, renderTelegramAgentMarkdownV2(content), "MarkdownV2")
	if err == nil {
		return messageID, nil
	}
	slog.Warn("发送 TG Markdown 消息失败，回退纯文本", "chat_id", strings.TrimSpace(chatID), "text_bytes", len(content), "error", err)
	return n.sendTextToChatAndReturnMessageIDWithParseMode(ctx, chatID, content, "")
}

func (n *telegramNotifier) sendTextToChatAndReturnMessageIDWithParseMode(ctx context.Context, chatID string, content string, parseMode string) (int64, error) {
	if n == nil {
		return 0, fmt.Errorf("telegram notifier is nil")
	}
	payload := telegramSendMessageRequest{
		ChatID:    strings.TrimSpace(chatID),
		Text:      content,
		ParseMode: strings.TrimSpace(parseMode),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	var lastErr error
	methodURL := n.telegramMethodURL("sendMessage")
	if methodURL == "" {
		return 0, fmt.Errorf("telegram method url is empty: sendMessage")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for attempt := 1; attempt <= telegramRequestRetryMaxAttempt; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, telegramHTTPTimeout)
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, methodURL, bytes.NewReader(raw))
		if err != nil {
			cancel()
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")

		res, err := n.client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			if !shouldRetryTelegramNetworkError(err) || attempt >= telegramRequestRetryMaxAttempt {
				return 0, lastErr
			}
			if !sleepTelegramRetry(ctx, telegramRequestRetryDelay*time.Duration(attempt)) {
				return 0, lastErr
			}
			continue
		}

		var response telegramSendMessageWithResultResponse
		decodeErr := json.NewDecoder(res.Body).Decode(&response)
		_ = res.Body.Close()
		cancel()
		if decodeErr != nil {
			lastErr = decodeErr
			if attempt >= telegramRequestRetryMaxAttempt {
				return 0, fmt.Errorf("telegram decode response failed: %w", decodeErr)
			}
			if !sleepTelegramRetry(ctx, telegramRequestRetryDelay*time.Duration(attempt)) {
				return 0, lastErr
			}
			continue
		}
		if res.StatusCode == http.StatusOK && response.OK {
			return response.Result.MessageID, nil
		}
		if res.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("telegram status=%d description=%s", res.StatusCode, response.Description)
		} else {
			lastErr = fmt.Errorf("telegram response not ok: %s", response.Description)
		}
		if !shouldRetryTelegramStatus(res.StatusCode, response.Description) || attempt >= telegramRequestRetryMaxAttempt {
			return 0, lastErr
		}
		if !sleepTelegramRetry(ctx, telegramRequestRetryDelay*time.Duration(attempt)) {
			return 0, lastErr
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("telegram request failed")
	}
	return 0, lastErr
}

func (n *telegramNotifier) sendPhotoBinaryToChat(chatID string, filename string, photoData []byte, caption string) error {
	return n.sendMultipartBinaryToChat(context.Background(), "sendPhoto", "photo", chatID, filename, photoData, caption, telegramPhotoHTTPTimeout)
}

func (n *telegramNotifier) sendPhotoURLToChat(ctx context.Context, chatID string, photoURL string, caption string) error {
	if n == nil {
		return fmt.Errorf("telegram notifier is nil")
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is empty")
	}
	photoURL = strings.TrimSpace(photoURL)
	if photoURL == "" {
		return fmt.Errorf("photo url is empty")
	}
	return n.postTelegramMethod(ctx, "sendPhoto", telegramSendPhotoRequest{
		ChatID:  chatID,
		Photo:   photoURL,
		Caption: strings.TrimSpace(caption),
	})
}

func (n *telegramNotifier) sendDocumentBinaryToChat(ctx context.Context, chatID string, filename string, documentData []byte, caption string) error {
	return n.sendMultipartBinaryToChat(ctx, "sendDocument", "document", chatID, filename, documentData, caption, telegramPhotoHTTPTimeout)
}

func (n *telegramNotifier) sendDocumentURLToChat(ctx context.Context, chatID string, documentURL string, caption string) error {
	if n == nil {
		return fmt.Errorf("telegram notifier is nil")
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is empty")
	}
	documentURL = strings.TrimSpace(documentURL)
	if documentURL == "" {
		return fmt.Errorf("document url is empty")
	}
	return n.postTelegramMethod(ctx, "sendDocument", telegramSendDocumentRequest{
		ChatID:   chatID,
		Document: documentURL,
		Caption:  strings.TrimSpace(caption),
	})
}

func (n *telegramNotifier) sendMultipartBinaryToChat(ctx context.Context, method string, fieldName string, chatID string, filename string, fileData []byte, caption string, timeout time.Duration) error {
	if n == nil {
		return fmt.Errorf("telegram notifier is nil")
	}
	method = strings.TrimSpace(method)
	fieldName = strings.TrimSpace(fieldName)
	methodURL := n.telegramMethodURL(method)
	if methodURL == "" {
		return fmt.Errorf("telegram method url is empty: %s", method)
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is empty")
	}
	if len(fileData) == 0 {
		return fmt.Errorf("file binary is empty")
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "attachment"
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", chatID); err != nil {
		return err
	}
	if strings.TrimSpace(caption) != "" {
		if err := writer.WriteField("caption", strings.TrimSpace(caption)); err != nil {
			return err
		}
	}
	filePart, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(filePart, bytes.NewReader(fileData)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	if err := n.postTelegramRawWithRetry(ctx, methodURL, writer.FormDataContentType(), body.Bytes(), timeout); err != nil {
		slog.Warn("发送 TG 二进制附件失败", "method", method, "chat_id", chatID, "filename", filename, "file_bytes", len(fileData), "caption_bytes", len(strings.TrimSpace(caption)), "error", err)
		return err
	}
	slog.Info("已发送 TG 二进制附件", "method", method, "chat_id", chatID, "filename", filename, "file_bytes", len(fileData), "caption_bytes", len(strings.TrimSpace(caption)))
	return nil
}

func (n *telegramNotifier) editMessageTextWithMarkup(chatID int64, messageID int64, content string, replyMarkup any) error {
	payload := telegramEditMessageTextRequest{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        content,
		ReplyMarkup: replyMarkup,
	}
	if err := n.postTelegramMethod(context.Background(), "editMessageText", payload); err != nil {
		if isTelegramMessageNotModifiedError(err) {
			return nil
		}
		return err
	}
	return nil
}

func (n *telegramNotifier) editMarkdownMessageText(chatID int64, messageID int64, content string) error {
	payload := telegramEditMessageTextRequest{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      renderTelegramAgentMarkdownV2(content),
		ParseMode: "MarkdownV2",
	}
	if err := n.postTelegramMethod(context.Background(), "editMessageText", payload); err != nil {
		if isTelegramMessageNotModifiedError(err) {
			return nil
		}
		slog.Warn("编辑 TG Markdown 消息失败，回退纯文本", "chat_id", chatID, "message_id", messageID, "text_bytes", len(content), "error", err)
		if fallbackErr := n.editMessageTextWithMarkup(chatID, messageID, content, nil); fallbackErr != nil {
			if isTelegramMessageNotModifiedError(fallbackErr) {
				return nil
			}
			return fallbackErr
		}
		return nil
	}
	return nil
}

func isTelegramMessageNotModifiedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

func (n *telegramNotifier) sendChatAction(ctx context.Context, chatID string, action string) error {
	chatID = strings.TrimSpace(chatID)
	action = strings.TrimSpace(action)
	if chatID == "" {
		return errors.New("telegram chat id is empty")
	}
	if action == "" {
		return errors.New("telegram chat action is empty")
	}
	payload := telegramSendChatActionRequest{
		ChatID: chatID,
		Action: action,
	}
	return n.postTelegramMethod(ctx, "sendChatAction", payload)
}

func renderTelegramAgentMarkdownV2(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	var out strings.Builder
	for index := 0; index < len(content); {
		if strings.HasPrefix(content[index:], "```") {
			end := strings.Index(content[index+3:], "```")
			if end >= 0 {
				body := content[index+3 : index+3+end]
				out.WriteString(renderTelegramMarkdownV2CodeBlock(body))
				index += 3 + end + 3
				continue
			}
		}
		if strings.HasPrefix(content[index:], "**") {
			end := strings.Index(content[index+2:], "**")
			if end >= 0 {
				inner := content[index+2 : index+2+end]
				if strings.TrimSpace(inner) != "" {
					out.WriteByte('*')
					out.WriteString(escapeTelegramMarkdownV2Text(inner))
					out.WriteByte('*')
					index += 2 + end + 2
					continue
				}
			}
		}
		if content[index] == '`' {
			end := strings.IndexByte(content[index+1:], '`')
			if end >= 0 {
				inner := content[index+1 : index+1+end]
				if strings.TrimSpace(inner) != "" {
					out.WriteByte('`')
					out.WriteString(escapeTelegramMarkdownV2CodeText(inner))
					out.WriteByte('`')
					index += 1 + end + 1
					continue
				}
			}
		}
		if text, link, advance, ok := parseTelegramMarkdownLink(content, index); ok {
			out.WriteByte('[')
			out.WriteString(escapeTelegramMarkdownV2Text(text))
			out.WriteString("](")
			out.WriteString(escapeTelegramMarkdownV2LinkURL(link))
			out.WriteByte(')')
			index += advance
			continue
		}
		if strings.HasPrefix(content[index:], "__") {
			end := strings.Index(content[index+2:], "__")
			if end >= 0 {
				inner := content[index+2 : index+2+end]
				if strings.TrimSpace(inner) != "" {
					out.WriteString("__")
					out.WriteString(escapeTelegramMarkdownV2Text(inner))
					out.WriteString("__")
					index += 2 + end + 2
					continue
				}
			}
		}
		if strings.HasPrefix(content[index:], "~~") {
			end := strings.Index(content[index+2:], "~~")
			if end >= 0 {
				inner := content[index+2 : index+2+end]
				if strings.TrimSpace(inner) != "" {
					out.WriteByte('~')
					out.WriteString(escapeTelegramMarkdownV2Text(inner))
					out.WriteByte('~')
					index += 2 + end + 2
					continue
				}
			}
		}
		if strings.HasPrefix(content[index:], "||") {
			end := strings.Index(content[index+2:], "||")
			if end >= 0 {
				inner := content[index+2 : index+2+end]
				if strings.TrimSpace(inner) != "" {
					out.WriteString("||")
					out.WriteString(escapeTelegramMarkdownV2Text(inner))
					out.WriteString("||")
					index += 2 + end + 2
					continue
				}
			}
		}
		if content[index] == '*' && canOpenTelegramMarkdownSingleDelimiter(content, index) {
			end := findTelegramMarkdownSingleDelimiterEnd(content, index+1, '*')
			if end >= 0 {
				inner := content[index+1 : end]
				if strings.TrimSpace(inner) != "" {
					out.WriteByte('_')
					out.WriteString(escapeTelegramMarkdownV2Text(inner))
					out.WriteByte('_')
					index = end + 1
					continue
				}
			}
		}
		if content[index] == '_' && canOpenTelegramMarkdownSingleDelimiter(content, index) {
			end := findTelegramMarkdownSingleDelimiterEnd(content, index+1, '_')
			if end >= 0 {
				inner := content[index+1 : end]
				if strings.TrimSpace(inner) != "" {
					out.WriteByte('_')
					out.WriteString(escapeTelegramMarkdownV2Text(inner))
					out.WriteByte('_')
					index = end + 1
					continue
				}
			}
		}
		r, size := utf8.DecodeRuneInString(content[index:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		out.WriteString(escapeTelegramMarkdownV2Rune(r))
		index += size
	}
	return out.String()
}

func findTelegramMarkdownSingleDelimiterEnd(content string, start int, delimiter byte) int {
	for index := start; index < len(content); index++ {
		if content[index] == delimiter && canCloseTelegramMarkdownSingleDelimiter(content, index) {
			return index
		}
	}
	return -1
}

func canOpenTelegramMarkdownSingleDelimiter(content string, index int) bool {
	if index+1 >= len(content) || isTelegramMarkdownASCIIWhitespace(content[index+1]) {
		return false
	}
	return index == 0 || !isTelegramMarkdownASCIIAlnum(content[index-1])
}

func canCloseTelegramMarkdownSingleDelimiter(content string, index int) bool {
	if index == 0 || isTelegramMarkdownASCIIWhitespace(content[index-1]) {
		return false
	}
	return index+1 >= len(content) || !isTelegramMarkdownASCIIAlnum(content[index+1])
}

func isTelegramMarkdownASCIIAlnum(value byte) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func isTelegramMarkdownASCIIWhitespace(value byte) bool {
	return value == ' ' || value == '\n' || value == '\r' || value == '\t'
}

func parseTelegramMarkdownLink(content string, index int) (string, string, int, bool) {
	if index >= len(content) || content[index] != '[' {
		return "", "", 0, false
	}
	textEnd := strings.IndexByte(content[index+1:], ']')
	if textEnd < 0 {
		return "", "", 0, false
	}
	textEnd += index + 1
	if textEnd+1 >= len(content) || content[textEnd+1] != '(' {
		return "", "", 0, false
	}
	linkEnd := strings.IndexByte(content[textEnd+2:], ')')
	if linkEnd < 0 {
		return "", "", 0, false
	}
	linkEnd += textEnd + 2
	text := content[index+1 : textEnd]
	link := content[textEnd+2 : linkEnd]
	if strings.TrimSpace(text) == "" || strings.TrimSpace(link) == "" {
		return "", "", 0, false
	}
	return text, strings.TrimSpace(link), linkEnd - index + 1, true
}

func renderTelegramMarkdownV2CodeBlock(body string) string {
	lang, code := splitTelegramMarkdownV2CodeBlock(body)
	var out strings.Builder
	out.WriteString("```")
	if lang != "" {
		out.WriteString(escapeTelegramMarkdownV2CodeText(lang))
	}
	out.WriteByte('\n')
	out.WriteString(escapeTelegramMarkdownV2CodeText(code))
	if !strings.HasSuffix(code, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString("```")
	return out.String()
}

func splitTelegramMarkdownV2CodeBlock(body string) (string, string) {
	body = strings.Trim(body, "\n")
	if body == "" {
		return "", ""
	}
	lineEnd := strings.IndexByte(body, '\n')
	if lineEnd <= 0 {
		return "", body
	}
	firstLine := strings.TrimSpace(body[:lineEnd])
	if isTelegramMarkdownV2CodeLanguage(firstLine) {
		return firstLine, body[lineEnd+1:]
	}
	return "", body
}

func isTelegramMarkdownV2CodeLanguage(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '+' || r == '#' {
			continue
		}
		return false
	}
	return true
}

func escapeTelegramMarkdownV2CodeText(content string) string {
	var out strings.Builder
	for _, r := range content {
		switch r {
		case '\\', '`':
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func escapeTelegramMarkdownV2LinkURL(content string) string {
	var out strings.Builder
	for _, r := range content {
		switch r {
		case '\\', ')':
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func escapeTelegramMarkdownV2Text(content string) string {
	var out strings.Builder
	for _, r := range content {
		out.WriteString(escapeTelegramMarkdownV2Rune(r))
	}
	return out.String()
}

func escapeTelegramMarkdownV2Rune(r rune) string {
	switch r {
	case '\\', '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!':
		return `\` + string(r)
	default:
		return string(r)
	}
}

func (n *telegramNotifier) answerCallbackQuery(callbackQueryID string, text string, showAlert bool) error {
	payload := telegramAnswerCallbackQueryRequest{
		CallbackQueryID: strings.TrimSpace(callbackQueryID),
		Text:            strings.TrimSpace(text),
		ShowAlert:       showAlert,
	}
	return n.postTelegramMethod(context.Background(), "answerCallbackQuery", payload)
}

func (n *telegramNotifier) setMyCommands(commands []telegramBotCommand) error {
	if len(commands) == 0 {
		return nil
	}
	payload := telegramSetMyCommandsRequest{
		Commands: commands,
	}
	return n.postTelegramMethod(context.Background(), "setMyCommands", payload)
}

func (n *telegramNotifier) postTelegramMethod(parent context.Context, method string, payload any) error {
	if n == nil {
		return fmt.Errorf("telegram notifier is nil")
	}
	methodURL := n.telegramMethodURL(method)
	if methodURL == "" {
		return fmt.Errorf("telegram method url is empty: %s", method)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return n.postTelegramRawWithRetry(parent, methodURL, "application/json", raw, telegramHTTPTimeout)
}

func (n *telegramNotifier) postTelegramRawWithRetry(parent context.Context, methodURL string, contentType string, rawBody []byte, timeout time.Duration) error {
	if n == nil {
		return fmt.Errorf("telegram notifier is nil")
	}
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		timeout = telegramHTTPTimeout
	}

	var lastErr error
	for attempt := 1; attempt <= telegramRequestRetryMaxAttempt; attempt++ {
		attemptCtx, cancel := context.WithTimeout(parent, timeout)
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, methodURL, bytes.NewReader(rawBody))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Content-Type", contentType)

		res, err := n.client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			if !shouldRetryTelegramNetworkError(err) || attempt >= telegramRequestRetryMaxAttempt {
				return lastErr
			}
			if !sleepTelegramRetry(parent, telegramRequestRetryDelay*time.Duration(attempt)) {
				return lastErr
			}
			continue
		}

		var response telegramSendMessageResponse
		decodeErr := json.NewDecoder(res.Body).Decode(&response)
		_ = res.Body.Close()
		cancel()

		if decodeErr != nil {
			lastErr = decodeErr
			if attempt >= telegramRequestRetryMaxAttempt {
				return fmt.Errorf("telegram decode response failed: %w", decodeErr)
			}
			if !sleepTelegramRetry(parent, telegramRequestRetryDelay*time.Duration(attempt)) {
				return lastErr
			}
			continue
		}

		if res.StatusCode == http.StatusOK && response.OK {
			return nil
		}

		if res.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("telegram status=%d description=%s", res.StatusCode, response.Description)
		} else {
			lastErr = fmt.Errorf("telegram response not ok: %s", response.Description)
		}

		if !shouldRetryTelegramStatus(res.StatusCode, response.Description) || attempt >= telegramRequestRetryMaxAttempt {
			return lastErr
		}
		if !sleepTelegramRetry(parent, telegramRequestRetryDelay*time.Duration(attempt)) {
			return lastErr
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("telegram request failed")
	}
	return lastErr
}

func shouldRetryTelegramNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "client.timeout") ||
		strings.Contains(text, "deadline exceeded") ||
		strings.Contains(text, "connection reset by peer") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "unexpected eof") ||
		strings.Contains(text, "timeout")
}

func shouldRetryTelegramStatus(statusCode int, description string) bool {
	if statusCode == http.StatusTooManyRequests || statusCode == http.StatusRequestTimeout {
		return true
	}
	if statusCode >= http.StatusInternalServerError {
		return true
	}
	desc := strings.ToLower(strings.TrimSpace(description))
	return strings.Contains(desc, "too many requests") || strings.Contains(desc, "timeout")
}

func sleepTelegramRetry(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (n *telegramNotifier) telegramMethodURL(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return ""
	}
	if n.apiBase != "" && n.botToken != "" {
		return fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(n.apiBase, "/"), n.botToken, method)
	}
	if method == "sendMessage" {
		return n.endpoint
	}
	return ""
}
