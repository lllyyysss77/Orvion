package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	telegramCommandPollTimeoutSeconds = 25
	telegramCommandIdleInterval       = 10 * time.Second
	telegramCommandErrorInterval      = 3 * time.Second
	telegramStatusCoverImageURL       = "https://i.mukyu.ru/random?wtf_gender=girls"
	telegramStatusImageWindowSize     = 3
	telegramStatusImageRefillMaxRetry = 5
	telegramStatusImageRefillBaseWait = 1500 * time.Millisecond
	telegramStatusImageDownloadRetry  = 4
	// Telegram sendPhoto 单图上限 10MB。
	telegramStatusImageTGMaxBytes = 10 << 20

	telegramModelListPageSize         = 12
	telegramModelProviderListPageSize = 8
)

var telegramCommandProcessStartTime = time.Now()

var (
	telegramProcessUsageMu      sync.Mutex
	telegramLastCPUTimeSeconds  float64
	telegramLastProcessSampleAt time.Time

	telegramStatusImageWindowMu      sync.Mutex
	telegramStatusImageWindowItems   []telegramStatusImageItem
	telegramStatusImageRefillRunning bool
)

var errTelegramStatusImageTooLarge = errors.New("状态图片超过 TG 发送大小限制")

type telegramUpdateResponse struct {
	OK          bool             `json:"ok"`
	Description string           `json:"description"`
	Result      []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	Text      string       `json:"text"`
	Chat      telegramChat `json:"chat"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

type telegramCallbackQuery struct {
	ID      string           `json:"id"`
	Data    string           `json:"data"`
	Message *telegramMessage `json:"message"`
}

type telegramInlineKeyboardMarkup struct {
	InlineKeyboard [][]telegramInlineKeyboardButton `json:"inline_keyboard"`
}

type telegramInlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type telegramModelProviderRow struct {
	ID                uint       `gorm:"column:id"`
	ModelID           uint       `gorm:"column:model_id"`
	ProviderID        uint       `gorm:"column:provider_id"`
	ProviderModel     string     `gorm:"column:provider_model"`
	Status            int        `gorm:"column:status"`
	AutoDisabledUntil *time.Time `gorm:"column:auto_disabled_until"`
	Weight            int        `gorm:"column:weight"`
	ProviderName      string     `gorm:"column:provider_name"`
}

type telegramStatusImageItem struct {
	Binary   []byte
	FileName string
	Source   string
}

// StartTelegramCommandBot 启动 Telegram 命令对话机器人（/status、/model、/help）。
func StartTelegramCommandBot(ctx context.Context) {
	go telegramCommandLoop(ctx)
}

func telegramCommandLoop(ctx context.Context) {
	var (
		offset       int64
		configSign   string
		notifier     *telegramNotifier
		pollClient   *http.Client
		allowedChat  string
		pollEndpoint string
	)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		botToken, chatID, apiBase, proxyURL, enabled, err := resolveTelegramRuntimeConfig(ctx)
		if err != nil {
			slog.Warn("读取 TG 命令配置失败", "error", err)
			if !waitWithContext(ctx, telegramCommandErrorInterval) {
				return
			}
			continue
		}
		if !enabled {
			if !waitWithContext(ctx, telegramCommandIdleInterval) {
				return
			}
			continue
		}

		nextSign := strings.Join([]string{botToken, chatID, apiBase, proxyURL}, "|")
		if nextSign != configSign || notifier == nil {
			notifier, _, err = buildTelegramNotifier(botToken, chatID, apiBase, proxyURL, true)
			if err != nil {
				slog.Warn("初始化 TG 命令发送器失败", "error", err)
				if !waitWithContext(ctx, telegramCommandErrorInterval) {
					return
				}
				continue
			}
			if err := syncTelegramCommandList(notifier); err != nil {
				slog.Warn("同步 TG 命令列表失败", "error", err)
			}
			primeTelegramStatusImageWindow(ctx)
			pollClient, err = buildTelegramCommandPollClient(proxyURL)
			if err != nil {
				slog.Warn("初始化 TG 命令轮询客户端失败", "error", err)
				if !waitWithContext(ctx, telegramCommandErrorInterval) {
					return
				}
				continue
			}
			allowedChat = strings.TrimSpace(chatID)
			pollEndpoint = fmt.Sprintf("%s/bot%s/getUpdates", strings.TrimRight(strings.TrimSpace(apiBase), "/"), strings.TrimSpace(botToken))
			configSign = nextSign

			latestOffset, offsetErr := fetchTelegramLatestOffset(ctx, pollClient, pollEndpoint)
			if offsetErr != nil {
				slog.Warn("初始化 TG 命令轮询偏移失败，将继续轮询", "error", offsetErr)
				offset = 0
			} else {
				offset = latestOffset
			}
			slog.Info("TG 命令对话已启用", "chat_id", allowedChat)
		}

		updates, nextOffset, err := fetchTelegramUpdates(ctx, pollClient, pollEndpoint, offset)
		if err != nil {
			slog.Warn("TG 命令轮询失败", "error", err)
			if !waitWithContext(ctx, telegramCommandErrorInterval) {
				return
			}
			continue
		}
		offset = nextOffset

		for _, update := range updates {
			if update.CallbackQuery != nil {
				handleTelegramModelCallback(ctx, notifier, *update.CallbackQuery, allowedChat)
				continue
			}
			if update.Message == nil {
				continue
			}
			slog.Info("接收到 TG 消息",
				"chat_id", update.Message.Chat.ID,
				"message_id", update.Message.MessageID,
				"text_bytes", len(strings.TrimSpace(update.Message.Text)),
			)
			handledStatus, statusErr := handleTelegramStatusCommand(ctx, notifier, *update.Message, allowedChat)
			if statusErr != nil {
				slog.Warn("处理 TG /status 命令失败", "error", statusErr)
			}
			if handledStatus {
				continue
			}
			if err := handleTelegramModelCommand(ctx, notifier, *update.Message, allowedChat); err != nil {
				slog.Warn("处理 TG /model 命令失败", "error", err)
			}
			reply, shouldReply := buildTelegramCommandReply(ctx, *update.Message, allowedChat)
			if !shouldReply || strings.TrimSpace(reply) == "" {
				continue
			}
			if err := notifier.sendText(reply); err != nil {
				slog.Warn("回复 TG 命令消息失败", "error", err)
			}
		}
	}
}

func buildTelegramCommandPollClient(proxyURL string) (*http.Client, error) {
	client, err := buildTelegramHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	// getUpdates 长轮询 timeout=25s，客户端超时必须更长，避免提前触发 Client.Timeout。
	client.Timeout = time.Duration(telegramCommandPollTimeoutSeconds+15) * time.Second
	return client, nil
}

func resolveTelegramRuntimeConfig(ctx context.Context) (botToken, chatID, apiBase, proxyURL string, enabled bool, err error) {
	cfg, found, loadErr := loadTelegramBreakerAlertConfig(ctx)
	if loadErr != nil {
		return "", "", "", "", false, loadErr
	}
	if found {
		botToken = strings.TrimSpace(cfg.BotToken)
		chatID = strings.TrimSpace(cfg.ChatID)
		apiBase = strings.TrimSpace(cfg.APIBase)
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
		enabled = cfg.Enabled && botToken != "" && chatID != ""
		if apiBase == "" {
			apiBase = telegramDefaultAPIBase
		}
		return botToken, chatID, apiBase, proxyURL, enabled, nil
	}

	botToken = strings.TrimSpace(os.Getenv(envTelegramBotToken))
	chatID = strings.TrimSpace(os.Getenv(envTelegramChatID))
	apiBase = strings.TrimSpace(os.Getenv(envTelegramAPIBase))
	proxyURL = strings.TrimSpace(os.Getenv(envTelegramProxyURL))
	if apiBase == "" {
		apiBase = telegramDefaultAPIBase
	}
	enabled = botToken != "" && chatID != ""
	return botToken, chatID, apiBase, proxyURL, enabled, nil
}

func fetchTelegramLatestOffset(ctx context.Context, client *http.Client, endpoint string) (int64, error) {
	updates, nextOffset, err := fetchTelegramUpdatesWithOptions(ctx, client, endpoint, 0, 0, 1)
	if err != nil {
		return 0, err
	}
	if len(updates) == 0 {
		return 0, nil
	}
	return nextOffset, nil
}

func fetchTelegramUpdates(ctx context.Context, client *http.Client, endpoint string, offset int64) ([]telegramUpdate, int64, error) {
	return fetchTelegramUpdatesWithOptions(ctx, client, endpoint, offset, telegramCommandPollTimeoutSeconds, 100)
}

func fetchTelegramUpdatesWithOptions(ctx context.Context, client *http.Client, endpoint string, offset int64, timeoutSeconds int, limit int) ([]telegramUpdate, int64, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if timeoutSeconds < 0 {
		timeoutSeconds = 0
	}
	if limit <= 0 {
		limit = 100
	}

	query := url.Values{}
	if offset > 0 {
		query.Set("offset", strconv.FormatInt(offset, 10))
	}
	query.Set("timeout", strconv.Itoa(timeoutSeconds))
	query.Set("limit", strconv.Itoa(limit))
	query.Set("allowed_updates", `["message","callback_query"]`)

	requestURL := endpoint + "?" + query.Encode()
	requestTimeout := time.Duration(timeoutSeconds+10) * time.Second
	if requestTimeout < 10*time.Second {
		requestTimeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, offset, err
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, offset, err
	}
	defer res.Body.Close()

	var payload telegramUpdateResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, offset, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, offset, fmt.Errorf("telegram getUpdates status=%d description=%s", res.StatusCode, payload.Description)
	}
	if !payload.OK {
		return nil, offset, fmt.Errorf("telegram getUpdates not ok: %s", payload.Description)
	}

	nextOffset := offset
	for _, item := range payload.Result {
		if item.UpdateID >= nextOffset {
			nextOffset = item.UpdateID + 1
		}
	}
	return payload.Result, nextOffset, nil
}

func buildTelegramCommandReply(ctx context.Context, message telegramMessage, allowedChatID string) (string, bool) {
	if !isAllowedTelegramChat(message.Chat.ID, allowedChatID) {
		return "", false
	}

	raw := strings.TrimSpace(message.Text)
	if raw == "" {
		return "", false
	}
	normalized := strings.ToLower(raw)
	cmd := extractTelegramCommand(normalized)

	switch cmd {
	case "/start", "/help":
		return buildTelegramHelpMessage(), true
	case "/status":
		return buildTelegramSystemStatusMessage(ctx), true
	case "/model", "/models":
		return "", false
	default:
		if strings.Contains(normalized, "状态") {
			return buildTelegramSystemStatusMessage(ctx), true
		}
		return buildTelegramHelpMessage(), true
	}
}

func extractTelegramCommand(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return ""
	}
	cmd := fields[0]
	if !strings.HasPrefix(cmd, "/") {
		return ""
	}
	if idx := strings.Index(cmd, "@"); idx > 0 {
		cmd = cmd[:idx]
	}
	return cmd
}

func isAllowedTelegramChat(chatID int64, allowedChatID string) bool {
	allowed := strings.TrimSpace(allowedChatID)
	if allowed == "" {
		return false
	}
	if allowed == strconv.FormatInt(chatID, 10) {
		return true
	}
	allowedInt, err := strconv.ParseInt(allowed, 10, 64)
	if err != nil {
		return false
	}
	return allowedInt == chatID
}

func buildTelegramHelpMessage() string {
	return strings.Join([]string{
		"【🤖 Orvion TG 命令帮助】\n",
		"📊 /status - 查看系统状态摘要",
		"🧩 /model - 查看模型与模型提供商",
		"📘 /help - 显示帮助",
	}, "\n")
}

func buildTelegramSystemStatusMessage(ctx context.Context) string {
	now := time.Now()
	timeText := now.Format("2006-01-02 15:04:05")

	var modelTotal int64
	var modelProviderEnabled int64
	var todayReqs int64
	var todaySuccess int64
	var todayAmount float64

	if models.DB != nil {
		db := models.DB.WithContext(ctx)
		_ = db.Model(&models.Model{}).Where("deleted_at IS NULL").Count(&modelTotal).Error
		_ = db.Model(&models.ModelWithProvider{}).Where("deleted_at IS NULL").Where("status = ?", 1).Count(&modelProviderEnabled).Error

		year, month, day := now.Date()
		startOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		_ = db.Model(&models.ChatLog{}).Where("deleted_at IS NULL").Where("created_at >= ?", startOfDay).Count(&todayReqs).Error
		_ = db.Model(&models.ChatLog{}).Where("deleted_at IS NULL").Where("created_at >= ?", startOfDay).Where("status = ?", "success").Count(&todaySuccess).Error
		_ = db.Model(&models.ChatLog{}).Where("deleted_at IS NULL").Where("created_at >= ?", startOfDay).Select("COALESCE(SUM(total_cost), 0)").Scan(&todayAmount).Error
	}

	todayFailure := todayReqs - todaySuccess
	if todayFailure < 0 {
		todayFailure = 0
	}
	successRate := 0.0
	if todayReqs > 0 {
		successRate = float64(todaySuccess) / float64(todayReqs) * 100
	}

	totalAmount, amountErr := GetOrInitTotalConsumedAmount(ctx)
	if amountErr != nil {
		totalAmount = 0
	}

	firstDeployTime, firstDeployErr := GetOrInitFirstDeployTime(ctx)
	if firstDeployErr != nil {
		firstDeployTime = now
	}
	firstDeployTime = firstDeployTime.Local()
	deployUptime := now.Sub(firstDeployTime)
	if deployUptime < 0 {
		deployUptime = 0
	}
	processUptime := now.Sub(telegramCommandProcessStartTime)
	if processUptime < 0 {
		processUptime = 0
	}

	processUsage := collectTelegramProcessUsage(now)

	return strings.Join([]string{
		"【🤖 Orvion 系统状态】\n",
		fmt.Sprintf("🕒 时间：%s", timeText),
		fmt.Sprintf("🏷️ 版本：%s", consts.Version),
		"━━━━━━━━━━━━━━━━",
		fmt.Sprintf("💻 整体：CPU %.2f%%｜内存 %s", processUsage.CPUPercent, formatBytesBinary(processUsage.MemoryBytes)),
		fmt.Sprintf("🧿 模型：%d（启用模型关联：%d）", modelTotal, modelProviderEnabled),
		"━━━━━━━━━━━━━━━━",
		fmt.Sprintf("📈 今日请求：%d（成功：%d，失败：%d，成功率：%.2f%%）", todayReqs, todaySuccess, todayFailure, successRate),
		fmt.Sprintf("💰 今日消耗/累计消耗：$%.2f / $%.2f", todayAmount, totalAmount),
		fmt.Sprintf("⏳ 部署时长：%s", formatHumanDuration(deployUptime)),
		fmt.Sprintf("🚀 进程时长：%s", formatHumanDuration(processUptime)),
	}, "\n")
}

func resolveHealthStatusIcon(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "healthy":
		return "✅"
	case "degraded":
		return "⚠️"
	case "unhealthy":
		return "❌"
	default:
		return "ℹ️"
	}
}

func resolveDatabaseStatusIcon(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok":
		return "✅"
	case "slow":
		return "⚠️"
	case "connection_error", "ping_failed", "not_initialized":
		return "❌"
	default:
		return "ℹ️"
	}
}

func formatHumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	parts := make([]string, 0, 4)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d天", days))
	}
	if hours > 0 || len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%d小时", hours))
	}
	if minutes > 0 || len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%d分钟", minutes))
	}
	parts = append(parts, fmt.Sprintf("%d秒", seconds))
	return strings.Join(parts, "")
}

func syncTelegramCommandList(notifier *telegramNotifier) error {
	if notifier == nil {
		return nil
	}
	return notifier.setMyCommands([]telegramBotCommand{
		{Command: "status", Description: "查看系统状态摘要"},
		{Command: "model", Description: "查看模型与模型提供商状态"},
		{Command: "help", Description: "显示帮助"},
	})
}

func primeTelegramStatusImageWindow(ctx context.Context) {
	for i := 0; i < telegramStatusImageWindowSize; i++ {
		go func(worker int) {
			if err := prefetchTelegramStatusImageIntoWindow(ctx, fmt.Sprintf("startup_worker_%d", worker)); err != nil {
				slog.Warn("启动预拉 /status 图片失败", "worker", worker, "error", err)
			}
		}(i + 1)
	}
}

func popTelegramStatusImageWindowItem(ctx context.Context) (telegramStatusImageItem, bool) {
	telegramStatusImageWindowMu.Lock()
	needRefill := false
	if len(telegramStatusImageWindowItems) == 0 {
		telegramStatusImageWindowMu.Unlock()
		scheduleTelegramStatusImageRefill(ctx)
		return telegramStatusImageItem{}, false
	}

	item := telegramStatusImageWindowItems[0]
	telegramStatusImageWindowItems[0] = telegramStatusImageItem{}
	telegramStatusImageWindowItems = telegramStatusImageWindowItems[1:]
	if len(telegramStatusImageWindowItems) < telegramStatusImageWindowSize {
		needRefill = true
	}
	telegramStatusImageWindowMu.Unlock()
	if needRefill {
		scheduleTelegramStatusImageRefill(ctx)
	}
	return item, true
}

func scheduleTelegramStatusImageRefill(ctx context.Context) {
	telegramStatusImageWindowMu.Lock()
	if telegramStatusImageRefillRunning || len(telegramStatusImageWindowItems) >= telegramStatusImageWindowSize {
		telegramStatusImageWindowMu.Unlock()
		return
	}
	telegramStatusImageRefillRunning = true
	telegramStatusImageWindowMu.Unlock()

	go func() {
		defer func() {
			telegramStatusImageWindowMu.Lock()
			telegramStatusImageRefillRunning = false
			telegramStatusImageWindowMu.Unlock()
		}()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			telegramStatusImageWindowMu.Lock()
			missing := telegramStatusImageWindowSize - len(telegramStatusImageWindowItems)
			telegramStatusImageWindowMu.Unlock()
			if missing <= 0 {
				return
			}

			if err := prefetchTelegramStatusImageIntoWindowWithRetry(ctx, "refill"); err != nil {
				slog.Warn("异步补充 /status 图片缓存失败", "error", err)
				return
			}
		}
	}()
}

func prefetchTelegramStatusImageIntoWindowWithRetry(ctx context.Context, trigger string) error {
	var lastErr error
	for attempt := 1; attempt <= telegramStatusImageRefillMaxRetry; attempt++ {
		err := prefetchTelegramStatusImageIntoWindow(ctx, trigger)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt >= telegramStatusImageRefillMaxRetry {
			break
		}
		wait := telegramStatusImageRefillBaseWait * time.Duration(attempt)
		slog.Warn("异步补图失败，准备重试", "trigger", trigger, "attempt", attempt, "max_attempt", telegramStatusImageRefillMaxRetry, "next_wait", wait.String(), "error", err)
		if !waitWithContext(ctx, wait) {
			return ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unknown refill error")
	}
	return fmt.Errorf("补图重试耗尽: %w", lastErr)
}

func prefetchTelegramStatusImageIntoWindow(ctx context.Context, trigger string) error {
	photoURL := buildTelegramStatusCoverImageURL()
	binary, fileName, sourceURL, err := downloadTelegramStatusCoverImage(ctx, photoURL)
	if err != nil {
		return err
	}

	telegramStatusImageWindowMu.Lock()
	if len(telegramStatusImageWindowItems) >= telegramStatusImageWindowSize {
		telegramStatusImageWindowMu.Unlock()
		clearTelegramStatusBinary(binary)
		return nil
	}
	telegramStatusImageWindowItems = append(telegramStatusImageWindowItems, telegramStatusImageItem{
		Binary:   binary,
		FileName: fileName,
		Source:   sourceURL,
	})
	cacheSize := len(telegramStatusImageWindowItems)
	telegramStatusImageWindowMu.Unlock()

	slog.Info("缓存 /status 图片成功", "trigger", trigger, "cache_size", cacheSize, "filename", fileName, "bytes", len(binary))
	return nil
}

func handleTelegramStatusCommand(ctx context.Context, notifier *telegramNotifier, message telegramMessage, allowedChatID string) (bool, error) {
	if notifier == nil {
		return false, nil
	}
	if !isAllowedTelegramChat(message.Chat.ID, allowedChatID) {
		return false, nil
	}

	cmd := extractTelegramCommand(strings.ToLower(strings.TrimSpace(message.Text)))
	if cmd != "/status" {
		return false, nil
	}

	content := buildTelegramSystemStatusMessage(ctx)
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	item, ok := popTelegramStatusImageWindowItem(ctx)
	if !ok {
		photoURL := buildTelegramStatusCoverImageURL()
		photoBinary, fileName, sourceURL, err := downloadTelegramStatusCoverImage(ctx, photoURL)
		if err != nil {
			slog.Error("下载 /status 状态图片失败", "url", photoURL, "error", err)
			// 下载图片失败时回退到纯文本，避免状态消息丢失。
			if fallbackErr := notifier.sendTextWithMarkupToChat(chatID, content, nil); fallbackErr != nil {
				return true, fmt.Errorf("下载状态图片失败: %v, 文本回退也失败: %w", err, fallbackErr)
			}
			return true, nil
		}
		item = telegramStatusImageItem{
			Binary:   photoBinary,
			FileName: fileName,
			Source:   sourceURL,
		}
	}
	defer clearTelegramStatusBinary(item.Binary)

	if err := notifier.sendPhotoBinaryToChat(chatID, item.FileName, item.Binary, content); err != nil {
		// 图片发送失败时回退到纯文本，避免状态消息丢失。
		if fallbackErr := notifier.sendTextWithMarkupToChat(chatID, content, nil); fallbackErr != nil {
			return true, fmt.Errorf("发送状态图片失败: %v, 文本回退也失败: %w", err, fallbackErr)
		}
		return true, nil
	}
	scheduleTelegramStatusImageRefill(ctx)
	return true, nil
}

func buildTelegramStatusCoverImageURL() string {
	parsed, err := url.Parse(telegramStatusCoverImageURL)
	if err != nil {
		return telegramStatusCoverImageURL
	}
	query := parsed.Query()
	query.Set("_ts", strconv.FormatInt(time.Now().UnixNano(), 10))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func downloadTelegramStatusCoverImage(ctx context.Context, rawURL string) ([]byte, string, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, "", "", fmt.Errorf("状态图片地址为空")
	}

	var lastErr error
	for attempt := 1; attempt <= telegramStatusImageDownloadRetry; attempt++ {
		urlToDownload := rawURL
		if attempt > 1 {
			urlToDownload = buildTelegramStatusCoverImageURL()
		}

		binary, fileName, err := downloadTelegramStatusCoverImageOnce(ctx, urlToDownload)
		if err == nil {
			return binary, fileName, urlToDownload, nil
		}

		if errors.Is(err, errTelegramStatusImageTooLarge) {
			lastErr = err
			slog.Warn("下载 /status 状态图片超限，跳过并重试",
				"url", urlToDownload,
				"attempt", attempt,
				"max_attempt", telegramStatusImageDownloadRetry,
				"limit_bytes", telegramStatusImageTGMaxBytes,
				"error", err,
			)
			continue
		}
		return nil, "", "", err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("下载状态图片失败")
	}
	return nil, "", "", fmt.Errorf("状态图片多次下载均超限: %w", lastErr)
}

func downloadTelegramStatusCoverImageOnce(ctx context.Context, rawURL string) ([]byte, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Expires", "0")

	client := &http.Client{Timeout: 12 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载状态图片失败，status=%d", res.StatusCode)
	}
	if res.ContentLength > telegramStatusImageTGMaxBytes {
		return nil, "", fmt.Errorf("%w: content_length=%d limit=%d", errTelegramStatusImageTooLarge, res.ContentLength, telegramStatusImageTGMaxBytes)
	}

	binary, err := io.ReadAll(io.LimitReader(res.Body, telegramStatusImageTGMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(binary) == 0 {
		return nil, "", fmt.Errorf("下载状态图片为空")
	}
	if len(binary) > telegramStatusImageTGMaxBytes {
		clearTelegramStatusBinary(binary)
		return nil, "", fmt.Errorf("%w: bytes=%d limit=%d", errTelegramStatusImageTooLarge, len(binary), telegramStatusImageTGMaxBytes)
	}

	contentType := strings.TrimSpace(res.Header.Get("Content-Type"))
	filename := fmt.Sprintf("status_%d%s", time.Now().UnixNano(), telegramImageExtByContentType(contentType))
	slog.Info("下载 /status 状态图片成功", "url", rawURL, "filename", filename, "bytes", len(binary))
	return binary, filename, nil
}

func telegramImageExtByContentType(contentType string) string {
	if contentType == "" {
		return ".jpg"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = contentType
	}
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	case "image/avif":
		return ".avif"
	default:
		return ".img"
	}
}

func clearTelegramStatusBinary(binary []byte) {
	for i := range binary {
		binary[i] = 0
	}
}

func handleTelegramModelCommand(ctx context.Context, notifier *telegramNotifier, message telegramMessage, allowedChatID string) error {
	if notifier == nil {
		return nil
	}
	if !isAllowedTelegramChat(message.Chat.ID, allowedChatID) {
		return nil
	}
	cmd := extractTelegramCommand(strings.ToLower(strings.TrimSpace(message.Text)))
	if cmd != "/model" && cmd != "/models" {
		return nil
	}

	text, markup, err := buildTelegramModelListView(ctx, 1)
	if err != nil {
		return notifier.sendTextWithMarkupToChat(strconv.FormatInt(message.Chat.ID, 10), "模型列表读取失败："+err.Error(), nil)
	}
	return notifier.sendTextWithMarkupToChat(strconv.FormatInt(message.Chat.ID, 10), text, markup)
}

func handleTelegramModelCallback(ctx context.Context, notifier *telegramNotifier, callback telegramCallbackQuery, allowedChatID string) {
	if notifier == nil {
		return
	}
	if callback.Message == nil {
		if strings.TrimSpace(callback.ID) != "" {
			_ = notifier.answerCallbackQuery(callback.ID, "无效操作", false)
		}
		return
	}
	if !isAllowedTelegramChat(callback.Message.Chat.ID, allowedChatID) {
		_ = notifier.answerCallbackQuery(callback.ID, "无权限操作", true)
		return
	}

	text, markup, callbackText, err := resolveTelegramModelCallbackAction(ctx, strings.TrimSpace(callback.Data))
	if err != nil {
		_ = notifier.answerCallbackQuery(callback.ID, "操作失败："+err.Error(), true)
		return
	}
	if text == "" {
		_ = notifier.answerCallbackQuery(callback.ID, "暂不支持该操作", false)
		return
	}
	if callbackText == "" {
		callbackText = "已处理"
	}
	_ = notifier.answerCallbackQuery(callback.ID, callbackText, false)

	if err := notifier.editMessageTextWithMarkup(callback.Message.Chat.ID, callback.Message.MessageID, text, markup); err != nil {
		_ = notifier.sendTextWithMarkupToChat(strconv.FormatInt(callback.Message.Chat.ID, 10), text, markup)
	}
}

func resolveTelegramModelCallbackAction(ctx context.Context, data string) (string, *telegramInlineKeyboardMarkup, string, error) {
	if data == "" || !strings.HasPrefix(data, "mdl:") {
		return "", nil, "", nil
	}

	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return "", nil, "", fmt.Errorf("回调参数无效")
	}

	switch parts[1] {
	case "list":
		if len(parts) != 3 {
			return "", nil, "", fmt.Errorf("列表参数无效")
		}
		page := parseTelegramPositivePage(parts[2], 1)
		text, markup, err := buildTelegramModelListView(ctx, page)
		return text, markup, "模型列表已刷新", err
	case "detail":
		if len(parts) != 4 {
			return "", nil, "", fmt.Errorf("详情参数无效")
		}
		modelID, err := parseTelegramUint(parts[2])
		if err != nil {
			return "", nil, "", fmt.Errorf("模型 ID 无效")
		}
		page := parseTelegramPositivePage(parts[3], 1)
		text, markup, err := buildTelegramModelDetailView(ctx, modelID, page, "")
		return text, markup, "模型详情已刷新", err
	case "toggle":
		if len(parts) != 5 {
			return "", nil, "", fmt.Errorf("切换参数无效")
		}
		mwpID, err := parseTelegramUint(parts[2])
		if err != nil {
			return "", nil, "", fmt.Errorf("模型提供商 ID 无效")
		}
		modelID, err := parseTelegramUint(parts[3])
		if err != nil {
			return "", nil, "", fmt.Errorf("模型 ID 无效")
		}
		page := parseTelegramPositivePage(parts[4], 1)
		notice, err := toggleTelegramModelProviderStatus(ctx, mwpID, modelID)
		if err != nil {
			return "", nil, "", err
		}
		text, markup, err := buildTelegramModelDetailView(ctx, modelID, page, notice)
		return text, markup, "状态已切换", err
	default:
		return "", nil, "", fmt.Errorf("不支持的操作")
	}
}

func buildTelegramModelListView(ctx context.Context, page int) (string, *telegramInlineKeyboardMarkup, error) {
	if models.DB == nil {
		return "", nil, fmt.Errorf("数据库未初始化")
	}
	items := make([]models.Model, 0)
	if err := models.DB.WithContext(ctx).
		Where("deleted_at IS NULL").
		Order("id ASC").
		Find(&items).Error; err != nil {
		return "", nil, err
	}

	if len(items) == 0 {
		text := strings.Join([]string{
			"【🧩 Orvion 模型列表】",
			"当前没有可用模型。",
		}, "\n")
		return text, nil, nil
	}

	totalPages := (len(items) + telegramModelListPageSize - 1) / telegramModelListPageSize
	page = clampTelegramPage(page, totalPages)
	start := (page - 1) * telegramModelListPageSize
	end := start + telegramModelListPageSize
	if end > len(items) {
		end = len(items)
	}

	rows := make([][]telegramInlineKeyboardButton, 0, telegramModelListPageSize/2+2)
	currentRow := make([]telegramInlineKeyboardButton, 0, 2)
	for _, model := range items[start:end] {
		icon := "⚪️"
		if model.Status == 1 {
			icon = "🟢"
		}
		buttonText := truncateTelegramButtonText(fmt.Sprintf("%s %s", icon, strings.TrimSpace(model.Name)), 56)
		currentRow = append(currentRow, telegramInlineKeyboardButton{
			Text:         buttonText,
			CallbackData: fmt.Sprintf("mdl:detail:%d:1", model.ID),
		})
		if len(currentRow) == 2 {
			rows = append(rows, currentRow)
			currentRow = make([]telegramInlineKeyboardButton, 0, 2)
		}
	}
	if len(currentRow) > 0 {
		rows = append(rows, currentRow)
	}

	if totalPages > 1 {
		nav := make([]telegramInlineKeyboardButton, 0, 2)
		if page > 1 {
			nav = append(nav, telegramInlineKeyboardButton{
				Text:         "⬅️ 上一页",
				CallbackData: fmt.Sprintf("mdl:list:%d", page-1),
			})
		}
		if page < totalPages {
			nav = append(nav, telegramInlineKeyboardButton{
				Text:         "下一页 ➡️",
				CallbackData: fmt.Sprintf("mdl:list:%d", page+1),
			})
		}
		if len(nav) > 0 {
			rows = append(rows, nav)
		}
	}

	text := strings.Join([]string{
		"【🧩 Orvion 模型列表】",
		fmt.Sprintf("共 %d 个模型，当前第 %d/%d 页", len(items), page, totalPages),
		"点击模型可查看对应模型提供商，并切换启用/禁用状态。",
	}, "\n")

	return text, &telegramInlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

func buildTelegramModelDetailView(ctx context.Context, modelID uint, page int, notice string) (string, *telegramInlineKeyboardMarkup, error) {
	if models.DB == nil {
		return "", nil, fmt.Errorf("数据库未初始化")
	}
	var model models.Model
	if err := models.DB.WithContext(ctx).Where("id = ?", modelID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil, fmt.Errorf("模型不存在")
		}
		return "", nil, err
	}

	rows := make([]telegramModelProviderRow, 0)
	if err := models.DB.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Select("model_with_providers.id, model_with_providers.model_id, model_with_providers.provider_id, model_with_providers.provider_model, model_with_providers.status, model_with_providers.auto_disabled_until, model_with_providers.weight, providers.name AS provider_name").
		Joins("LEFT JOIN providers ON providers.id = model_with_providers.provider_id AND providers.deleted_at IS NULL").
		Where("model_with_providers.model_id = ?", modelID).
		Where("model_with_providers.deleted_at IS NULL").
		Order("model_with_providers.id ASC").
		Scan(&rows).Error; err != nil {
		return "", nil, err
	}

	type modelProviderSuccessStat struct {
		ModelWithProviderID uint  `gorm:"column:model_with_provider_id"`
		TotalCount          int64 `gorm:"column:total_count"`
		SuccessCount        int64 `gorm:"column:success_count"`
	}

	successRateByModelProvider := make(map[uint]float64, len(rows))
	if len(rows) > 0 {
		ids := make([]uint, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		stats := make([]modelProviderSuccessStat, 0, len(rows))
		if err := models.DB.WithContext(ctx).
			Model(&models.ChatLog{}).
			Select("model_with_provider_id AS model_with_provider_id, COUNT(*) AS total_count, SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_count").
			Where("deleted_at IS NULL").
			Where("model_with_provider_id IN ?", ids).
			Group("model_with_provider_id").
			Scan(&stats).Error; err == nil {
			for _, stat := range stats {
				if stat.TotalCount <= 0 {
					continue
				}
				successRateByModelProvider[stat.ModelWithProviderID] = float64(stat.SuccessCount) / float64(stat.TotalCount) * 100
			}
		}
	}

	enabledCount := 0
	for _, row := range rows {
		if row.Status == 1 {
			enabledCount++
		}
	}
	disabledCount := len(rows) - enabledCount

	keyboardRows := make([][]telegramInlineKeyboardButton, 0, telegramModelProviderListPageSize+3)
	totalPages := 1
	if len(rows) > 0 {
		totalPages = (len(rows) + telegramModelProviderListPageSize - 1) / telegramModelProviderListPageSize
	}
	page = clampTelegramPage(page, totalPages)

	if len(rows) > 0 {
		start := (page - 1) * telegramModelProviderListPageSize
		end := start + telegramModelProviderListPageSize
		if end > len(rows) {
			end = len(rows)
		}

		for _, row := range rows[start:end] {
			icon := "🔴"
			if row.Status == 1 {
				icon = "🟢"
			}
			providerName := strings.TrimSpace(row.ProviderName)
			if providerName == "" {
				providerName = fmt.Sprintf("provider#%d", row.ProviderID)
			}
			successRateText := "--"
			if rate, ok := successRateByModelProvider[row.ID]; ok {
				successRateText = fmt.Sprintf("%.1f%%", rate)
			}
			text := truncateTelegramButtonText(fmt.Sprintf("%s %s / %s ｜ %s", icon, providerName, strings.TrimSpace(row.ProviderModel), successRateText), 56)
			keyboardRows = append(keyboardRows, []telegramInlineKeyboardButton{
				{
					Text:         text,
					CallbackData: fmt.Sprintf("mdl:toggle:%d:%d:%d", row.ID, modelID, page),
				},
			})
		}
	}

	if totalPages > 1 {
		nav := make([]telegramInlineKeyboardButton, 0, 2)
		if page > 1 {
			nav = append(nav, telegramInlineKeyboardButton{
				Text:         "⬅️ 上一页",
				CallbackData: fmt.Sprintf("mdl:detail:%d:%d", modelID, page-1),
			})
		}
		if page < totalPages {
			nav = append(nav, telegramInlineKeyboardButton{
				Text:         "下一页 ➡️",
				CallbackData: fmt.Sprintf("mdl:detail:%d:%d", modelID, page+1),
			})
		}
		if len(nav) > 0 {
			keyboardRows = append(keyboardRows, nav)
		}
	}

	keyboardRows = append(keyboardRows, []telegramInlineKeyboardButton{
		{Text: "🔙 返回模型列表", CallbackData: "mdl:list:1"},
	})

	lines := []string{
		"【🧩 Orvion 模型详情】",
		fmt.Sprintf("模型：%s", strings.TrimSpace(model.Name)),
		fmt.Sprintf("模型提供商：%d（启用：%d，禁用：%d）", len(rows), enabledCount, disabledCount),
	}
	if len(rows) > 0 {
		lines = append(lines, fmt.Sprintf("当前第 %d/%d 页，点击按钮可切换状态。", page, totalPages))
	} else {
		lines = append(lines, "当前模型还没有配置模型提供商。")
	}
	if strings.TrimSpace(notice) != "" {
		lines = append(lines, "——", strings.TrimSpace(notice))
	}

	return strings.Join(lines, "\n"), &telegramInlineKeyboardMarkup{InlineKeyboard: keyboardRows}, nil
}

func toggleTelegramModelProviderStatus(ctx context.Context, modelWithProviderID uint, modelID uint) (string, error) {
	if models.DB == nil {
		return "", fmt.Errorf("数据库未初始化")
	}
	var relation models.ModelWithProvider
	if err := models.DB.WithContext(ctx).
		Where("id = ?", modelWithProviderID).
		Where("model_id = ?", modelID).
		First(&relation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("模型提供商关联不存在")
		}
		return "", err
	}

	nextStatus := 1
	actionText := "✅ 已启用"
	if relation.Status == 1 {
		nextStatus = 0
		actionText = "⛔️ 已禁用"
	}

	if err := models.DB.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Where("id = ?", relation.ID).
		Updates(map[string]any{
			"status":              nextStatus,
			"auto_disabled_until": nil,
		}).Error; err != nil {
		return "", err
	}

	providerName := fmt.Sprintf("provider#%d", relation.ProviderID)
	var provider models.Provider
	if err := models.DB.WithContext(ctx).
		Where("id = ?", relation.ProviderID).
		First(&provider).Error; err == nil {
		if name := strings.TrimSpace(provider.Name); name != "" {
			providerName = name
		}
	}

	return fmt.Sprintf("%s：%s / %s", actionText, providerName, strings.TrimSpace(relation.ProviderModel)), nil
}

func parseTelegramUint(raw string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(value), nil
}

func parseTelegramPositivePage(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func clampTelegramPage(page int, totalPages int) int {
	if totalPages <= 0 {
		return 1
	}
	if page < 1 {
		return 1
	}
	if page > totalPages {
		return totalPages
	}
	return page
}

func truncateTelegramButtonText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "…"
}

func waitWithContext(ctx context.Context, duration time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(duration):
		return true
	}
}

type telegramProcessUsage struct {
	MemoryBytes uint64
	CPUPercent  float64
}

func collectTelegramProcessUsage(now time.Time) telegramProcessUsage {
	usage := telegramProcessUsage{}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	usage.MemoryBytes = mem.Sys

	var rusage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &rusage); err != nil {
		return usage
	}
	cpuSeconds := float64(rusage.Utime.Sec) + float64(rusage.Utime.Usec)/1_000_000 +
		float64(rusage.Stime.Sec) + float64(rusage.Stime.Usec)/1_000_000

	telegramProcessUsageMu.Lock()
	defer telegramProcessUsageMu.Unlock()

	if !telegramLastProcessSampleAt.IsZero() && now.After(telegramLastProcessSampleAt) {
		deltaCPU := cpuSeconds - telegramLastCPUTimeSeconds
		deltaWall := now.Sub(telegramLastProcessSampleAt).Seconds()
		if deltaCPU >= 0 && deltaWall > 0 {
			usage.CPUPercent = normalizeCPUPercent(deltaCPU, deltaWall)
		}
	} else {
		uptime := now.Sub(telegramCommandProcessStartTime).Seconds()
		if uptime > 0 {
			usage.CPUPercent = normalizeCPUPercent(cpuSeconds, uptime)
		}
	}

	telegramLastCPUTimeSeconds = cpuSeconds
	telegramLastProcessSampleAt = now
	return usage
}

func normalizeCPUPercent(deltaCPU float64, deltaWall float64) float64 {
	cores := float64(runtime.NumCPU())
	if cores <= 0 || deltaWall <= 0 {
		return 0
	}
	value := (deltaCPU / deltaWall) * 100 / cores
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func formatBytesBinary(bytes uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(bytes)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return fmt.Sprintf("%.2f%s", value, units[unit])
}
