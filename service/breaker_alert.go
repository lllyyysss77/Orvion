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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/racio/orvion/balancers"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	telegramDefaultAPIBase         = "https://api.telegram.org"
	telegramHTTPTimeout            = 25 * time.Second
	telegramPhotoHTTPTimeout       = 35 * time.Second
	telegramRequestRetryMaxAttempt = 3
	telegramRequestRetryDelay      = 800 * time.Millisecond
	telegramMarkdownBoxBorderMax   = 33
	telegramWideMessageWidth       = 72
	telegramWideMessageMaxRunes    = 3500
	telegramWideCaptionMaxRunes    = 900
)

var ErrTelegramNotifierNotConfigured = errors.New("telegram 告警未启用或配置不完整")

var telegramWideMessagePad = strings.Repeat("\u2800", telegramWideMessageWidth)

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
	ChatID    string `json:"chat_id"`
	Photo     string `json:"photo"`
	Caption   string `json:"caption,omitempty"`
	ParseMode string `json:"parse_mode,omitempty"`
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

type telegramDeleteMessageRequest struct {
	ChatID    string `json:"chat_id"`
	MessageID int64  `json:"message_id"`
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
// 只读取系统配置（configs.key=breaker_alert_tg），避免运行时配置来源分叉。
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
			slog.Warn("发送熔断告警失败", "error", err)
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
		networkCfg, _, networkErr := loadNetworkForwardingConfig(ctx)
		if networkErr != nil {
			return nil, false, networkErr
		}
		return buildTelegramNotifier(cfg.BotToken, cfg.ChatID, cfg.APIBase, networkCfg.TelegramProxyURL, cfg.Enabled)
	}
	return nil, false, nil
}

func loadNetworkForwardingConfig(ctx context.Context) (models.NetworkForwardingConfig, bool, error) {
	config, err := gorm.G[models.Config](models.DB).Where(models.ColumnEquals("key"), models.KeyNetworkForwarding).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.NetworkForwardingConfig{}, false, nil
		}
		return models.NetworkForwardingConfig{}, false, err
	}

	raw := strings.TrimSpace(config.Value)
	if raw == "" {
		return models.NetworkForwardingConfig{}, true, nil
	}

	var cfg models.NetworkForwardingConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return models.NetworkForwardingConfig{}, true, fmt.Errorf("解析网络转发配置失败: %w", err)
	}
	cfg.TelegramProxyURL = strings.TrimSpace(cfg.TelegramProxyURL)
	cfg.ProxyIP = strings.TrimSpace(cfg.ProxyIP)
	return cfg, true, nil
}

func loadTelegramBreakerAlertConfig(ctx context.Context) (models.TelegramBreakerAlertConfig, bool, error) {
	config, err := gorm.G[models.Config](models.DB).Where(models.ColumnEquals("key"), models.KeyTelegramBreakerAlert).First(ctx)
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
	return notifier.sendText(defaultTelegramBreakerAlertTestMessage())
}

func defaultTelegramBreakerAlertTestMessage() string {
	return strings.Join([]string{
		"【Orvion TG 告警测试】",
		fmt.Sprintf("时间：%s", time.Now().Format("2006-01-02 15:04:05")),
		"状态：测试消息发送成功",
		"说明：这是一条来自系统配置页面的测试消息。",
	}, "\n")
}

func (n *telegramNotifier) SendBreakerOpen(event balancers.BreakerOpenEvent) error {
	content := strings.Join([]string{
		"【Orvion 熔断告警】",
		fmt.Sprintf("时间：%s", event.At.Format("2006-01-02 15:04:05")),
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
	content := buildModelProviderAutoDisableAlertContent(detail, event, time.Now())
	return sendTelegramCaptionWithStatusImage(ctx, notifier, notifier.chatID, content)
}

func buildModelProviderAutoDisableAlertContent(detail modelProviderAutoDisableAlertDetail, event ModelProviderAutoDisableAlertEvent, now time.Time) string {
	rows := []telegramAlignedAlertRow{
		{Label: "时间", Value: now.Format("2006-01-02 15:04:05")},
	}
	if strings.TrimSpace(detail.ModelName) != "" {
		rows = append(rows, telegramAlignedAlertRow{Label: "模型", Value: strings.TrimSpace(detail.ModelName)})
	}
	if strings.TrimSpace(detail.ProviderName) != "" || strings.TrimSpace(detail.ProviderModel) != "" {
		providerText := strings.TrimSpace(detail.ProviderName)
		if providerText == "" {
			providerText = "未知提供商"
		}
		if providerModel := strings.TrimSpace(detail.ProviderModel); providerModel != "" {
			providerText += " / " + providerModel
		}
		rows = append(rows, telegramAlignedAlertRow{Label: "提供商", Value: providerText})
	}
	rows = append(rows,
		telegramAlignedAlertRow{Label: "触发原因", Value: fmt.Sprintf("检测窗口内错误达到 %d 次", event.Threshold)},
		telegramAlignedAlertRow{Label: "检测窗口", Value: event.Window.String()},
		telegramAlignedAlertRow{Label: "恢复时间", Value: event.ResumeAt.Format("2006-01-02 15:04:05")},
	)

	return "【Orvion 模型提供商熔断】\n" + formatTelegramAlignedAlertRows(rows)
}

type telegramAlignedAlertRow struct {
	Label string
	Value string
}

func formatTelegramAlignedAlertRows(rows []telegramAlignedAlertRow) string {
	maxLabelWidth := 0
	for _, row := range rows {
		if width := telegramTextDisplayWidth(row.Label); width > maxLabelWidth {
			maxLabelWidth = width
		}
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		label := strings.TrimSpace(row.Label)
		value := strings.TrimSpace(row.Value)
		padding := telegramAlignedTextPadding(maxLabelWidth - telegramTextDisplayWidth(label))
		lines = append(lines, label+padding+"："+value)
	}
	return strings.Join(lines, "\n")
}

func telegramAlignedTextPadding(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat("　", width/2) + strings.Repeat(" ", width%2)
}

func telegramTextDisplayWidth(content string) int {
	width := 0
	for _, r := range content {
		if isTelegramZeroWidthRune(r) {
			continue
		}
		if isTelegramWideRune(r) {
			width += 2
			continue
		}
		width++
	}
	return width
}

func isTelegramZeroWidthRune(r rune) bool {
	return (r >= 0x0300 && r <= 0x036F) ||
		(r >= 0xFE00 && r <= 0xFE0F) ||
		(r >= 0x1F3FB && r <= 0x1F3FF) ||
		r == 0x200D
}

func isTelegramWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2329 && r <= 0x232A) ||
		(r >= 0x2E80 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE19) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1FAFF)
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
		slog.Warn("读取模型提供商熔断告警详情失败", "error", err)
		return modelProviderAutoDisableAlertDetail{}
	}
	return detail
}

func (n *telegramNotifier) sendText(content string) error {
	return n.sendTextWithMarkupToChat(n.chatID, content, nil)
}

func (n *telegramNotifier) sendTextWithMarkupToChat(chatID string, content string, replyMarkup any) error {
	return n.sendTextWithMarkupToChatWithParseMode(chatID, content, replyMarkup, "")
}

func (n *telegramNotifier) sendTextWithMarkupToChatWithParseMode(chatID string, content string, replyMarkup any, parseMode string) error {
	content = widenTelegramMessageForTelegram(content)
	payload := telegramSendMessageRequest{
		ChatID:      chatID,
		Text:        content,
		ParseMode:   strings.TrimSpace(parseMode),
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

func widenTelegramMessageForTelegram(content string) string {
	return widenTelegramTextForTelegram(content, telegramWideMessageMaxRunes)
}

func widenTelegramCaptionForTelegram(content string) string {
	return widenTelegramTextForTelegram(content, telegramWideCaptionMaxRunes)
}

func widenTelegramTextForTelegram(content string, maxRunes int) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	trimmed := strings.TrimRight(content, "\r\n")
	if strings.HasSuffix(trimmed, telegramWideMessagePad) {
		return content
	}
	if len([]rune(trimmed)) > maxRunes {
		return content
	}
	if telegramTextMaxLineDisplayWidth(trimmed) >= telegramWideMessageWidth {
		return content
	}
	return trimmed + "\n" + telegramWideMessagePad
}

func telegramTextMaxLineDisplayWidth(content string) int {
	maxWidth := 0
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		width := telegramTextDisplayWidth(strings.TrimRight(line, "\r"))
		if width > maxWidth {
			maxWidth = width
		}
	}
	return maxWidth
}

func (n *telegramNotifier) sendTextToChatAndReturnMessageIDWithParseMode(ctx context.Context, chatID string, content string, parseMode string) (int64, error) {
	if n == nil {
		return 0, fmt.Errorf("telegram notifier is nil")
	}
	content = widenTelegramMessageForTelegram(content)
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
	return n.sendPhotoBinaryToChatWithParseMode(context.Background(), chatID, filename, photoData, caption, "")
}

func (n *telegramNotifier) sendPhotoBinaryToChatWithParseMode(ctx context.Context, chatID string, filename string, photoData []byte, caption string, parseMode string) error {
	return n.sendMultipartBinaryToChat(ctx, "sendPhoto", "photo", chatID, filename, photoData, caption, strings.TrimSpace(parseMode), telegramPhotoHTTPTimeout)
}

func (n *telegramNotifier) sendPhotoURLToChat(ctx context.Context, chatID string, photoURL string, caption string) error {
	return n.sendPhotoURLToChatWithCaptionWidening(ctx, chatID, photoURL, caption, true)
}

func (n *telegramNotifier) sendPhotoURLToChatWithoutCaptionWidening(ctx context.Context, chatID string, photoURL string, caption string) error {
	return n.sendPhotoURLToChatWithCaptionWidening(ctx, chatID, photoURL, caption, false)
}

func (n *telegramNotifier) sendPhotoURLToChatWithCaptionWidening(ctx context.Context, chatID string, photoURL string, caption string, widenCaption bool) error {
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
	caption = strings.TrimSpace(caption)
	if widenCaption {
		caption = widenTelegramCaptionForTelegram(caption)
	}
	return n.postTelegramMethod(ctx, "sendPhoto", telegramSendPhotoRequest{
		ChatID:  chatID,
		Photo:   photoURL,
		Caption: caption,
	})
}

func (n *telegramNotifier) sendDocumentBinaryToChat(ctx context.Context, chatID string, filename string, documentData []byte, caption string) error {
	return n.sendMultipartBinaryToChat(ctx, "sendDocument", "document", chatID, filename, documentData, caption, "", telegramPhotoHTTPTimeout)
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
		Caption:  widenTelegramCaptionForTelegram(strings.TrimSpace(caption)),
	})
}

func (n *telegramNotifier) sendMultipartBinaryToChat(ctx context.Context, method string, fieldName string, chatID string, filename string, fileData []byte, caption string, parseMode string, timeout time.Duration) error {
	return n.sendMultipartBinaryToChatWithCaptionWidening(ctx, method, fieldName, chatID, filename, fileData, caption, parseMode, timeout, true)
}

func (n *telegramNotifier) sendMultipartBinaryToChatWithoutCaptionWidening(ctx context.Context, method string, fieldName string, chatID string, filename string, fileData []byte, caption string, parseMode string, timeout time.Duration) error {
	return n.sendMultipartBinaryToChatWithCaptionWidening(ctx, method, fieldName, chatID, filename, fileData, caption, parseMode, timeout, false)
}

func (n *telegramNotifier) sendMultipartBinaryToChatWithCaptionWidening(ctx context.Context, method string, fieldName string, chatID string, filename string, fileData []byte, caption string, parseMode string, timeout time.Duration, widenCaption bool) error {
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
	caption = strings.TrimSpace(caption)
	if widenCaption {
		caption = widenTelegramCaptionForTelegram(caption)
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return err
		}
	}
	if strings.TrimSpace(parseMode) != "" {
		if err := writer.WriteField("parse_mode", strings.TrimSpace(parseMode)); err != nil {
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
	content = widenTelegramMessageForTelegram(content)
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
	rendered := widenTelegramMessageForTelegram(renderTelegramAgentMarkdownV2(content))
	payload := telegramEditMessageTextRequest{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      rendered,
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
	content = normalizeTelegramAgentMarkdownBlocks(content)

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
				if writeTelegramMarkdownV2Entity(&out, inner, "*", "*") {
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
					if writeTelegramMarkdownV2EntityFromInlineCode(&out, inner) {
						index += 1 + end + 1
						continue
					}
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
				if writeTelegramMarkdownV2Entity(&out, inner, "__", "__") {
					index += 2 + end + 2
					continue
				}
			}
		}
		if strings.HasPrefix(content[index:], "~~") {
			end := strings.Index(content[index+2:], "~~")
			if end >= 0 {
				inner := content[index+2 : index+2+end]
				if writeTelegramMarkdownV2Entity(&out, inner, "~", "~") {
					index += 2 + end + 2
					continue
				}
			}
		}
		if strings.HasPrefix(content[index:], "||") {
			end := strings.Index(content[index+2:], "||")
			if end >= 0 {
				inner := content[index+2 : index+2+end]
				if writeTelegramMarkdownV2Entity(&out, inner, "||", "||") {
					index += 2 + end + 2
					continue
				}
			}
		}
		if content[index] == '*' && canOpenTelegramMarkdownSingleDelimiter(content, index) {
			end := findTelegramMarkdownSingleDelimiterEnd(content, index+1, '*')
			if end >= 0 {
				inner := content[index+1 : end]
				if writeTelegramMarkdownV2Entity(&out, inner, "_", "_") {
					index = end + 1
					continue
				}
			}
		}
		if content[index] == '_' && canOpenTelegramMarkdownSingleDelimiter(content, index) {
			end := findTelegramMarkdownSingleDelimiterEnd(content, index+1, '_')
			if end >= 0 {
				inner := content[index+1 : end]
				if writeTelegramMarkdownV2Entity(&out, inner, "_", "_") {
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

func writeTelegramMarkdownV2EntityFromInlineCode(out *strings.Builder, inner string) bool {
	trimmed := strings.TrimSpace(inner)
	if strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") && len(trimmed) > 4 {
		return writeTelegramMarkdownV2Entity(out, strings.TrimSpace(trimmed[2:len(trimmed)-2]), "*", "*")
	}
	return false
}

func normalizeTelegramAgentMarkdownBlocks(content string) string {
	lines := strings.Split(content, "\n")
	lines = repairTelegramAgentMalformedCodeFences(lines)
	normalized := make([]string, 0, len(lines))
	inCodeBlock := false
	for index := 0; index < len(lines); {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if isTelegramMarkdownFenceLine(trimmed) {
			normalized = append(normalized, line)
			inCodeBlock = !inCodeBlock
			index++
			continue
		}
		if inCodeBlock {
			normalized = append(normalized, line)
			index++
			continue
		}
		if isTelegramMarkdownBlockQuoteLine(trimmed) {
			quoteLines, nextIndex := collectTelegramMarkdownBlockQuoteCard(lines, index)
			if len(quoteLines) > 0 {
				normalized = append(normalized, quoteLines...)
				index = nextIndex
				continue
			}
		}
		if isTelegramMarkdownTableRow(trimmed) && index+1 < len(lines) && isTelegramMarkdownTableSeparator(strings.TrimSpace(lines[index+1])) {
			tableLines, nextIndex := collectTelegramMarkdownTableBox(lines, index)
			if len(tableLines) > 0 {
				normalized = append(normalized, tableLines...)
				index = nextIndex
				continue
			}
		}
		if isTelegramBoxDrawingBlockStart(trimmed) {
			boxLines, nextIndex := collectTelegramBareBoxDrawingBlock(lines, index)
			if len(boxLines) > 0 {
				normalized = append(normalized, boxLines...)
				index = nextIndex
				continue
			}
		}
		if isTelegramMarkdownHorizontalRule(trimmed) {
			normalized = append(normalized, "━━━━━━━━━━━━")
			index++
			continue
		}
		if heading := parseTelegramMarkdownHeadingText(trimmed); heading != "" {
			normalized = append(normalized, "**"+heading+"**")
			index++
			continue
		}
		normalized = append(normalized, line)
		index++
	}
	return strings.Join(normalized, "\n")
}

func collectTelegramMarkdownBlockQuoteCard(lines []string, start int) ([]string, int) {
	index := start
	raw := make([]string, 0, 4)
	for index < len(lines) {
		trimmed := strings.TrimSpace(lines[index])
		if !isTelegramMarkdownBlockQuoteLine(trimmed) {
			break
		}
		raw = append(raw, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
		index++
	}
	if len(raw) == 0 {
		return nil, start
	}

	title := ""
	titleLine := ""
	items := make([]string, 0, len(raw))
	plain := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if item, ok := parseTelegramMarkdownQuoteListItem(line); ok {
			items = append(items, item)
			continue
		}
		if title == "" && len(items) == 0 && len(plain) == 0 {
			titleLine = line
			title = strings.TrimRight(strings.TrimSpace(line), "：:")
			continue
		}
		plain = append(plain, line)
	}

	if len(items) == 0 {
		out := make([]string, 0, len(plain)+1)
		if titleLine != "" {
			out = append(out, "• "+titleLine)
		}
		for _, line := range plain {
			out = append(out, "• "+line)
		}
		if len(out) == 0 {
			return nil, start
		}
		return out, index
	}

	out := make([]string, 0, len(items)+len(plain)+2)
	if title != "" {
		out = append(out, title)
		if len(items) > 0 || len(plain) > 0 {
			out = append(out, "━━━━━━━━━━━━")
		}
	}
	for _, line := range plain {
		out = append(out, line)
	}
	for _, item := range items {
		out = append(out, "• "+item)
	}
	if len(out) == 0 {
		return nil, start
	}
	return out, index
}

func isTelegramMarkdownBlockQuoteLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), ">")
}

func parseTelegramMarkdownQuoteListItem(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	for _, prefix := range []string{"- ", "* ", "+ ", "• "} {
		if strings.HasPrefix(line, prefix) {
			item := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			item = trimTelegramMarkdownQuoteListStatusIcon(item)
			if item == "" {
				return "", false
			}
			return item, true
		}
	}
	return "", false
}

func trimTelegramMarkdownQuoteListStatusIcon(item string) string {
	item = strings.TrimSpace(item)
	for _, icon := range []string{"❌", "✅", "⚠️", "⚠", "🚫"} {
		item = strings.TrimSpace(strings.TrimPrefix(item, icon))
	}
	return item
}

func collectTelegramBareBoxDrawingBlock(lines []string, start int) ([]string, int) {
	index := start
	body := make([]string, 0, 8)
	hasEnd := false
	for index < len(lines) {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if !isTelegramBoxDrawingLine(trimmed) {
			break
		}
		body = append(body, line)
		if isTelegramBoxDrawingBlockEnd(trimmed) {
			hasEnd = true
			index++
			break
		}
		index++
	}
	if !hasEnd || len(body) < 3 {
		return nil, start
	}
	body = normalizeTelegramBareBoxDrawingLines(body)
	out := make([]string, 0, len(body)+2)
	out = append(out, "```text")
	out = append(out, body...)
	out = append(out, "```")
	return out, index
}

func normalizeTelegramBareBoxDrawingLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, normalizeTelegramBoxDrawingBorderLine(line))
	}
	return out
}

func normalizeTelegramBoxDrawingBorderLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return line
	}
	runes := []rune(trimmed)
	if len(runes) < 2 {
		return line
	}
	if !isTelegramBoxDrawingBorderStartRune(runes[0]) {
		return line
	}
	borderWidth := 0
	for _, r := range runes[1:] {
		if r != '─' && r != '━' && r != '═' && !isTelegramBoxDrawingBorderEndRune(r) {
			return line
		}
		if r == '─' || r == '━' || r == '═' {
			borderWidth++
		}
	}
	return string(runes[0]) + telegramMarkdownBoxBorder(borderWidth)
}

func isTelegramBoxDrawingBorderStartRune(r rune) bool {
	return r == '┌' || r == '┏' || r == '╭' || r == '╔' || r == '├' || r == '┣' || r == '╞' || r == '╟' || r == '╠' || r == '└' || r == '┗' || r == '╰' || r == '╚'
}

func isTelegramBoxDrawingBorderEndRune(r rune) bool {
	return r == '┐' || r == '┓' || r == '╮' || r == '╗' || r == '┤' || r == '┫' || r == '╡' || r == '╢' || r == '╣' || r == '┘' || r == '┛' || r == '╯' || r == '╝'
}

func isTelegramBoxDrawingBlockStart(line string) bool {
	return strings.HasPrefix(line, "┌") || strings.HasPrefix(line, "┏") || strings.HasPrefix(line, "╭") || strings.HasPrefix(line, "╔")
}

func isTelegramBoxDrawingBlockEnd(line string) bool {
	return strings.HasPrefix(line, "└") || strings.HasPrefix(line, "┗") || strings.HasPrefix(line, "╰") || strings.HasPrefix(line, "╚")
}

func isTelegramBoxDrawingLine(line string) bool {
	if line == "" {
		return false
	}
	for _, prefix := range []string{"┌", "┏", "╭", "╔", "├", "┣", "╞", "╟", "╠", "│", "┃", "║", "└", "┗", "╰", "╚"} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func repairTelegramAgentMalformedCodeFences(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	repaired := make([]string, 0, len(lines))
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		lang, ok := parseTelegramAgentMalformedCodeFenceOpen(line)
		if !ok {
			repaired = append(repaired, line)
			continue
		}
		closeIndex := findTelegramAgentMalformedCodeFenceClose(lines, index+1)
		if closeIndex < 0 {
			repaired = append(repaired, line)
			continue
		}
		repaired = append(repaired, "```"+lang)
		repaired = append(repaired, lines[index+1:closeIndex]...)
		repaired = append(repaired, "```")
		index = closeIndex
	}
	return repaired
}

func parseTelegramAgentMalformedCodeFenceOpen(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		return "", false
	}
	tickCount := 0
	for tickCount < len(trimmed) && trimmed[tickCount] == '`' {
		tickCount++
	}
	if tickCount == 0 || tickCount > 2 {
		return "", false
	}
	lang := strings.TrimSpace(trimmed[tickCount:])
	if !isTelegramMarkdownV2CodeLanguage(lang) {
		return "", false
	}
	return lang, true
}

func findTelegramAgentMalformedCodeFenceClose(lines []string, start int) int {
	for index := start; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		if trimmed == "`" || trimmed == "``" || trimmed == "```" {
			return index
		}
	}
	return -1
}

func isTelegramMarkdownFenceLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

func collectTelegramMarkdownTableBox(lines []string, start int) ([]string, int) {
	header := parseTelegramMarkdownTableCells(lines[start])
	if len(header) == 0 {
		return nil, start
	}
	index := start + 2
	rows := make([][]string, 0)
	for index < len(lines) {
		trimmed := strings.TrimSpace(lines[index])
		if !isTelegramMarkdownTableRow(trimmed) {
			break
		}
		cells := parseTelegramMarkdownTableCells(trimmed)
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
		index++
	}
	if len(rows) == 0 {
		return nil, start
	}

	boxLines := renderTelegramMarkdownTableBox(header, rows)
	if len(boxLines) == 0 {
		return nil, start
	}
	out := make([]string, 0, len(boxLines)+2)
	out = append(out, "```text")
	out = append(out, boxLines...)
	out = append(out, "```")
	return out, index
}

func renderTelegramMarkdownTableBox(header []string, rows [][]string) []string {
	title := strings.Join(header, " / ")
	labelWidth := 0
	body := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if len(row) == 1 {
			body = append(body, row[0])
			continue
		}
		if width := telegramTextDisplayWidth(row[0]); width > labelWidth {
			labelWidth = width
		}
	}
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if len(row) == 1 {
			body = append(body, row[0])
			continue
		}
		label := padTelegramCodeBlockTextRight(row[0], labelWidth)
		value := strings.Join(row[1:], " / ")
		body = append(body, label+" : "+value)
	}
	if strings.TrimSpace(title) == "" && len(body) == 0 {
		return nil
	}

	contentWidth := telegramTextDisplayWidth(title)
	for _, line := range body {
		if width := telegramTextDisplayWidth(line); width > contentWidth {
			contentWidth = width
		}
	}
	if contentWidth < 1 {
		contentWidth = 1
	}

	border := telegramMarkdownBoxBorder(contentWidth + 2)
	out := make([]string, 0, len(body)+4)
	out = append(out, "┌"+border)
	if strings.TrimSpace(title) != "" {
		out = append(out, "│ "+title)
		out = append(out, "├"+border)
	}
	for _, line := range body {
		out = append(out, "│ "+line)
	}
	out = append(out, "└"+border)
	return out
}

func telegramMarkdownBoxBorder(width int) string {
	if width < 1 {
		width = 1
	}
	if width > telegramMarkdownBoxBorderMax {
		width = telegramMarkdownBoxBorderMax
	}
	return strings.Repeat("─", width)
}

func parseTelegramMarkdownTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.Trim(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cell := cleanTelegramMarkdownTableCell(part)
		if cell == "" {
			continue
		}
		cells = append(cells, cell)
	}
	return cells
}

func cleanTelegramMarkdownTableCell(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("**", "", "__", "", "~~", "", "`", "")
	return strings.TrimSpace(replacer.Replace(value))
}

func isTelegramMarkdownTableRow(line string) bool {
	return strings.Contains(line, "|") && strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|")
}

func isTelegramMarkdownTableSeparator(line string) bool {
	if !isTelegramMarkdownTableRow(line) {
		return false
	}
	cells := parseTelegramMarkdownTableCells(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		cell = strings.Trim(cell, ":")
		if cell == "" {
			return false
		}
		for _, r := range cell {
			if r != '-' {
				return false
			}
		}
	}
	return true
}

func padTelegramCodeBlockTextRight(value string, targetWidth int) string {
	padding := targetWidth - telegramTextDisplayWidth(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func isTelegramMarkdownHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	for _, r := range line {
		if r != '-' && r != '*' && r != '_' {
			return false
		}
	}
	return true
}

func parseTelegramMarkdownHeadingText(line string) string {
	if !strings.HasPrefix(line, "#") {
		return ""
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return ""
	}
	return strings.TrimSpace(line[level+1:])
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
	linkEnd := findTelegramMarkdownLinkURLClose(content, textEnd+2)
	if linkEnd < 0 {
		return "", "", 0, false
	}
	text := content[index+1 : textEnd]
	link := content[textEnd+2 : linkEnd]
	if strings.TrimSpace(text) == "" || strings.TrimSpace(link) == "" {
		return "", "", 0, false
	}
	return text, strings.TrimSpace(link), linkEnd - index + 1, true
}

func findTelegramMarkdownLinkURLClose(content string, start int) int {
	depth := 0
	escaped := false
	for index := start; index < len(content); index++ {
		c := content[index]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			if depth == 0 {
				return index
			}
			depth--
		}
	}
	return -1
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

func writeTelegramMarkdownV2Entity(out *strings.Builder, inner string, open string, close string) bool {
	leading, core, trailing := splitTelegramMarkdownV2EntityWhitespace(inner)
	if core == "" {
		return false
	}
	out.WriteString(escapeTelegramMarkdownV2Text(leading))
	out.WriteString(open)
	out.WriteString(escapeTelegramMarkdownV2Text(core))
	out.WriteString(close)
	out.WriteString(escapeTelegramMarkdownV2Text(trailing))
	return true
}

func splitTelegramMarkdownV2EntityWhitespace(content string) (string, string, string) {
	start := 0
	for start < len(content) {
		r, size := utf8.DecodeRuneInString(content[start:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if !unicode.IsSpace(r) {
			break
		}
		start += size
	}

	end := len(content)
	for end > start {
		r, size := utf8.DecodeLastRuneInString(content[:end])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	return content[:start], content[start:end], content[end:]
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
