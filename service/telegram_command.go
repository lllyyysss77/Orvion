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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/racio/orvion/agent"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/providers"
	"gorm.io/gorm"
)

const (
	telegramCommandPollTimeoutSeconds  = 25
	telegramCommandIdleInterval        = 10 * time.Second
	telegramCommandErrorInterval       = 3 * time.Second
	telegramDefaultStatusCoverImageURL = "https://i.mukyu.ru/random?wtf_gender=girls"
	telegramStatusImageWindowSize      = 3
	telegramStatusImageRefillMaxRetry  = 5
	telegramStatusImageRefillBaseWait  = 1500 * time.Millisecond
	telegramStatusImageDownloadRetry   = 4
	telegramStatusImageDownloadTimeout = 18 * time.Second
	telegramStatusImageRetryBaseWait   = 1200 * time.Millisecond
	// Telegram sendPhoto 单图上限 10MB。
	telegramStatusImageTGMaxBytes   = 10 << 20
	telegramAgentPhotoMaxBytes      = 10 << 20
	telegramAgentDocumentMaxBytes   = 50 << 20
	telegramAgentInputImageMaxBytes = 10 << 20
	telegramAgentInputImageMaxCount = 4

	telegramModelListPageSize         = 12
	telegramModelProviderListPageSize = 8

	telegramDailyUsageReportHour          = 7
	telegramDailyUsageReportMinute        = 0
	telegramDailyUsageReportCheckInterval = 1 * time.Minute
)

var telegramCommandProcessStartTime = time.Now()

var (
	telegramProcessUsageMu      sync.Mutex
	telegramLastCPUTimeSeconds  float64
	telegramLastProcessSampleAt time.Time

	telegramStatusImageWindowMu      sync.Mutex
	telegramStatusImageWindowItems   []telegramStatusImageItem
	telegramStatusImageRefillRunning bool
	telegramStatusImageNextID        uint64
)

var errTelegramStatusImageTooLarge = errors.New("状态图片超过 TG 发送大小限制")

type telegramUpdateResponse struct {
	OK          bool             `json:"ok"`
	Description string           `json:"description"`
	Result      []telegramUpdate `json:"result"`
}

type telegramGetFileResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		FileID       string `json:"file_id"`
		FileUniqueID string `json:"file_unique_id"`
		FileSize     int64  `json:"file_size"`
		FilePath     string `json:"file_path"`
	} `json:"result"`
}

type telegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query"`
}

type telegramMessage struct {
	MessageID int64                 `json:"message_id"`
	Text      string                `json:"text"`
	Caption   string                `json:"caption"`
	Chat      telegramChat          `json:"chat"`
	Photo     []telegramPhotoSize   `json:"photo"`
	Document  *telegramDocumentFile `json:"document"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

type telegramPhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type telegramDocumentFile struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MIMEType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
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
	ID       uint64
	Binary   []byte
	FileName string
	Source   string
	CachedAt time.Time
}

type telegramDailySlowRequest struct {
	ID        uint
	ModelName string
	Provider  string
	Status    string
	LatencyMs int
	CreatedAt time.Time
}

type telegramDailyUsageSummary struct {
	StartDate       time.Time
	EndDate         time.Time
	TotalCost       float64
	TotalRequests   int64
	SuccessRequests int64
	TopModelName    string
	TopModelReqs    int64
	TopAuthKeyName  string
	TopAuthKeyReqs  int64
	SlowestRequest  *telegramDailySlowRequest
}

// StartTelegramCommandBot 启动 Telegram 命令对话机器人（/status、/model、/help）。
func StartTelegramCommandBot(ctx context.Context) {
	registerTelegramAgentSystemToolHooks()
	pkg.GoSafe("service.telegram_command_loop", func() { telegramCommandLoop(ctx) })
}

// StartTelegramDailyUsageReport 每天早上 7 点推送前一天使用日报。
func StartTelegramDailyUsageReport(ctx context.Context) {
	pkg.GoSafe("service.telegram_daily_usage_report_loop", func() { telegramDailyUsageReportLoop(ctx) })
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
				"text_bytes", len(strings.TrimSpace(resolveTelegramMessageText(*update.Message))),
				"photo_count", len(update.Message.Photo),
				"has_document", update.Message.Document != nil,
			)
			handledHelp, helpErr := handleTelegramHelpCommand(ctx, notifier, *update.Message, allowedChat)
			if helpErr != nil {
				slog.Warn("处理 TG /help 命令失败", "error", helpErr)
			}
			if handledHelp {
				continue
			}
			handledRestart, restartErr := handleTelegramRestartCommand(ctx, notifier, pollClient, *update.Message, allowedChat)
			if restartErr != nil {
				slog.Warn("处理 TG /restart 命令失败", "error", restartErr)
			}
			if handledRestart {
				continue
			}
			handledStop, stopErr := handleTelegramStopCommand(ctx, notifier, *update.Message, allowedChat)
			if stopErr != nil {
				slog.Warn("处理 TG /stop 命令失败", "error", stopErr)
			}
			if handledStop {
				continue
			}
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
			handledAgent, agentErr := handleTelegramAgentMessage(ctx, notifier, *update.Message, allowedChat)
			if agentErr != nil {
				slog.Warn("处理 TG Agent 消息失败", "error", agentErr)
			}
			if handledAgent {
				continue
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

func telegramDailyUsageReportLoop(ctx context.Context) {
	lastSentScheduleDate, err := loadTelegramDailyUsageReportLastSentDate(ctx)
	if err != nil {
		slog.Warn("读取 TG 每日使用日报发送游标失败", "error", err)
	}

	runOnce := func(now time.Time) {
		persistedLastSent, loadErr := loadTelegramDailyUsageReportLastSentDate(ctx)
		if loadErr != nil {
			slog.Warn("刷新 TG 每日使用日报发送游标失败", "error", loadErr)
		} else if persistedLastSent != "" {
			lastSentScheduleDate = persistedLastSent
		}

		scheduleDate, shouldRun := shouldRunTelegramDailyUsageReport(now, lastSentScheduleDate)
		if !shouldRun {
			return
		}

		notifier, ok, resolveErr := resolveTelegramNotifier(ctx)
		if resolveErr != nil {
			slog.Warn("加载 TG 每日使用日报通知器失败", "schedule_date", scheduleDate, "error", resolveErr)
			return
		}
		if !ok || notifier == nil {
			return
		}

		claimed, claimErr := claimTelegramDailyUsageReportScheduleDate(ctx, scheduleDate)
		if claimErr != nil {
			slog.Warn("占用 TG 每日使用日报发送游标失败", "schedule_date", scheduleDate, "error", claimErr)
			return
		}
		if !claimed {
			lastSentScheduleDate = scheduleDate
			return
		}

		lastSentScheduleDate = scheduleDate
		sendErr := dispatchTelegramDailyUsageReportWithNotifier(ctx, notifier, now)
		if sendErr != nil {
			slog.Warn("发送 TG 每日使用日报失败", "schedule_date", scheduleDate, "error", sendErr)
			return
		}
	}

	runOnce(time.Now())

	ticker := time.NewTicker(telegramDailyUsageReportCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runOnce(now)
		}
	}
}

func shouldRunTelegramDailyUsageReport(now time.Time, lastSentScheduleDate string) (string, bool) {
	scheduleDate := now.Format("2006-01-02")
	y, m, d := now.Date()
	triggerAt := time.Date(y, m, d, telegramDailyUsageReportHour, telegramDailyUsageReportMinute, 0, 0, now.Location())
	if now.Before(triggerAt) {
		return scheduleDate, false
	}
	if strings.TrimSpace(lastSentScheduleDate) == scheduleDate {
		return scheduleDate, false
	}
	return scheduleDate, true
}

func dispatchTelegramDailyUsageReportWithNotifier(ctx context.Context, notifier *telegramNotifier, now time.Time) error {
	content := buildTelegramYesterdayUsageReportMessage(ctx, now)
	if err := sendTelegramCaptionWithStatusImageWithoutWidening(ctx, notifier, notifier.chatID, content); err != nil {
		return err
	}

	slog.Info("已发送 TG 每日使用日报",
		"chat_id", notifier.chatID,
		"yesterday", now.AddDate(0, 0, -1).Format("2006-01-02"),
	)
	return nil
}

func loadTelegramDailyUsageReportLastSentDate(ctx context.Context) (string, error) {
	cfg, err := gorm.G[models.Config](models.DB).
		Where(models.ColumnEquals("key"), models.KeyTelegramDailyUsageReportLastSentDate).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}

	value := strings.TrimSpace(cfg.Value)
	if _, parseErr := time.Parse("2006-01-02", value); parseErr != nil {
		return "", nil
	}
	return value, nil
}

func claimTelegramDailyUsageReportScheduleDate(ctx context.Context, scheduleDate string) (bool, error) {
	scheduleDate = strings.TrimSpace(scheduleDate)
	if scheduleDate == "" {
		return false, nil
	}
	if models.DB == nil {
		return false, errors.New("database is not initialized")
	}

	claimed := false
	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cfg models.Config
		err := tx.Where(models.ColumnEquals("key"), models.KeyTelegramDailyUsageReportLastSentDate).First(&cfg).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if createErr := tx.Create(&models.Config{
					Key:   models.KeyTelegramDailyUsageReportLastSentDate,
					Value: scheduleDate,
				}).Error; createErr != nil {
					return createErr
				}
				claimed = true
				return nil
			}
			return err
		}

		if strings.TrimSpace(cfg.Value) == scheduleDate {
			return nil
		}
		if err := tx.Model(&models.Config{}).
			Where(models.ColumnEquals("key"), models.KeyTelegramDailyUsageReportLastSentDate).
			Update("value", scheduleDate).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
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
		if networkCfg, _, networkErr := loadNetworkForwardingConfig(ctx); networkErr != nil {
			return "", "", "", "", false, networkErr
		} else {
			proxyURL = strings.TrimSpace(networkCfg.TelegramProxyURL)
		}
		enabled = cfg.Enabled && botToken != "" && chatID != ""
		if apiBase == "" {
			apiBase = telegramDefaultAPIBase
		}
		return botToken, chatID, apiBase, proxyURL, enabled, nil
	}

	return "", "", "", "", false, nil
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
	case "/model", "/models":
		return "", false
	default:
		if strings.HasPrefix(normalized, "/") {
			return buildTelegramHelpMessage(), true
		}
		return "", false
	}
}

func isTelegramSystemStatusText(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.Trim(normalized, " \t\r\n。！？!?,，；;：:")
	switch normalized {
	case "状态", "系统状态", "查看状态", "查看系统状态", "orvion状态", "orvion系统状态":
		return true
	default:
		return false
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
		"🧹 /new - 开启新的 TG Agent 对话",
		"⏹️ /stop - 中断当前 TG Agent 回复",
		"🖼️ /img <提示词> - 使用生图模型生成图片",
		"♻️ /restart - 重启 TG Agent 并释放连接",
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
		_ = db.Model(&models.Model{}).Count(&modelTotal).Error
		_ = db.Model(&models.ModelWithProvider{}).Where("status = ?", 1).Count(&modelProviderEnabled).Error

		year, month, day := now.Date()
		startOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		agg, aggErr := models.QueryChatLogMetricsAgg(ctx, models.ChatLogQueryScope{StartAt: &startOfDay}, "created_at >= ?", startOfDay)
		if aggErr == nil {
			todayReqs = agg.Reqs
			todaySuccess = agg.Success
			todayAmount = agg.Amount
		}
	}

	todayFailure := todayReqs - todaySuccess
	if todayFailure < 0 {
		todayFailure = 0
	}
	successRate := 0.0
	if todayReqs > 0 {
		successRate = float64(todaySuccess) / float64(todayReqs) * 100
	}

	totalAmount := 0.0
	firstDeployTime := now
	if models.DB != nil {
		var amountErr error
		totalAmount, amountErr = GetOrInitTotalConsumedAmount(ctx)
		if amountErr != nil {
			totalAmount = 0
		}

		var firstDeployErr error
		firstDeployTime, firstDeployErr = GetOrInitFirstDeployTime(ctx)
		if firstDeployErr != nil {
			firstDeployTime = now
		}
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
	statusPairs := formatTelegramStatusMetricPairs([]telegramStatusMetricPair{
		{LeftLabel: "CPU", LeftValue: fmt.Sprintf("%.2f%%", processUsage.CPUPercent), RightLabel: "内存", RightValue: formatBytesBinary(processUsage.MemoryBytes)},
		{LeftLabel: "模型总数", LeftValue: fmt.Sprintf("%d", modelTotal), RightLabel: "启用提供方", RightValue: fmt.Sprintf("%d", modelProviderEnabled), RightOffset: 2},
		{LeftLabel: "请求", LeftValue: fmt.Sprintf("%d", todayReqs), RightLabel: "成功率", RightValue: fmt.Sprintf("%.2f%%", successRate)},
		{LeftLabel: "成功", LeftValue: fmt.Sprintf("%d", todaySuccess), RightLabel: "失败", RightValue: fmt.Sprintf("%d", todayFailure)},
	})

	return strings.Join([]string{
		fmt.Sprintf("## 🤖 Orvion 系统状态\n"),

		fmt.Sprintf("**🕒 时间**：%s", timeText),
		fmt.Sprintf("**🏷️ 版本**：`%s`", consts.Version),

		"\n---\n",

		fmt.Sprintf("### 💻 资源使用"),
		statusPairs[0],

		"\n### 🧿 模型信息",
		statusPairs[1],

		"\n---\n",

		fmt.Sprintf("### 📈 今日统计"),
		statusPairs[2],
		statusPairs[3],

		fmt.Sprintf("\n### 💰 消耗"),
		fmt.Sprintf("- 今日 / 累计：`$%.2f / $%.2f`", todayAmount, totalAmount),

		fmt.Sprintf("\n### ⏳ 时间"),
		fmt.Sprintf("- 部署时长：`%s`", formatHumanDuration(deployUptime)),
		fmt.Sprintf("- 运行时长：`%s`", formatHumanDuration(processUptime)),
	}, "\n")
}

type telegramStatusMetricPair struct {
	LeftLabel   string
	LeftValue   string
	RightLabel  string
	RightValue  string
	RightOffset int
}

func formatTelegramStatusMetricPairs(pairs []telegramStatusMetricPair) []string {
	leftParts := make([]string, 0, len(pairs))
	maxLeftWidth := 0
	for _, pair := range pairs {
		left := fmt.Sprintf("- %s：%s", pair.LeftLabel, pair.LeftValue)
		leftParts = append(leftParts, left)
		if width := telegramTextDisplayWidth(left); width > maxLeftWidth {
			maxLeftWidth = width
		}
	}

	lines := make([]string, 0, len(pairs))
	for index, pair := range pairs {
		left := leftParts[index]
		padding := strings.Repeat(" ", maxLeftWidth-telegramTextDisplayWidth(left)+2+pair.RightOffset)
		lines = append(lines, fmt.Sprintf("`%s%s- %s：%s`", left, padding, pair.RightLabel, pair.RightValue))
	}
	return lines
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
		{Command: "new", Description: "开启新的 TG Agent 对话"},
		{Command: "stop", Description: "中断当前 TG Agent 回复"},
		{Command: "img", Description: "使用生图模型生成图片"},
		{Command: "restart", Description: "重启 TG Agent 并释放连接"},
		{Command: "help", Description: "显示帮助"},
	})
}

func primeTelegramStatusImageWindow(ctx context.Context) {
	for i := 0; i < telegramStatusImageWindowSize; i++ {
		worker := i + 1
		pkg.GoSafe("service.telegram_status_image_prefetch", func() {
			if err := prefetchTelegramStatusImageIntoWindow(ctx, fmt.Sprintf("startup_worker_%d", worker)); err != nil {
				slog.Warn("启动预拉 /status 图片失败", "worker", worker, "error", err)
			}
		})
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
	scheduleTelegramStatusImageRefillWithTrigger(ctx, "refill")
}

func scheduleTelegramStatusImageRefillWithTrigger(ctx context.Context, trigger string) {
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		trigger = "refill"
	}

	telegramStatusImageWindowMu.Lock()
	if telegramStatusImageRefillRunning || len(telegramStatusImageWindowItems) >= telegramStatusImageWindowSize {
		telegramStatusImageWindowMu.Unlock()
		return
	}
	telegramStatusImageRefillRunning = true
	telegramStatusImageWindowMu.Unlock()

	pkg.GoSafe("service.telegram_status_image_refill", func() {
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

			if err := prefetchTelegramStatusImageIntoWindowWithRetry(ctx, trigger); err != nil {
				slog.Warn("异步补充 /status 图片缓存失败", "error", err)
				return
			}
		}
	})
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
	baseURL := resolveTelegramStatusCoverImageBaseURL(ctx)
	binary, fileName, sourceURL, err := downloadTelegramStatusCoverImage(ctx, baseURL)
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
		ID:       nextTelegramStatusImageCacheIDLocked(),
		Binary:   binary,
		FileName: fileName,
		Source:   sourceURL,
		CachedAt: time.Now(),
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
		if !isTelegramSystemStatusText(message.Text) {
			return false, nil
		}
	}

	content := buildTelegramSystemStatusMessage(ctx)
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	return true, sendTelegramSystemStatusMessage(ctx, notifier, chatID, content)
}

func sendTelegramSystemStatusMessage(ctx context.Context, notifier *telegramNotifier, chatID string, content string) error {
	rendered := renderTelegramAgentMarkdownV2(content)
	return sendTelegramCaptionWithStatusImageWithParseMode(ctx, notifier, chatID, rendered, "MarkdownV2")
}

func handleTelegramHelpCommand(ctx context.Context, notifier *telegramNotifier, message telegramMessage, allowedChatID string) (bool, error) {
	if notifier == nil {
		return false, nil
	}
	if !isAllowedTelegramChat(message.Chat.ID, allowedChatID) {
		return false, nil
	}

	cmd := extractTelegramCommand(strings.ToLower(strings.TrimSpace(message.Text)))
	if cmd != "/help" && cmd != "/start" {
		return false, nil
	}

	chatID := strconv.FormatInt(message.Chat.ID, 10)
	return true, sendTelegramHelpMessage(ctx, notifier, chatID)
}

func handleTelegramRestartCommand(ctx context.Context, notifier *telegramNotifier, pollClient *http.Client, message telegramMessage, allowedChatID string) (bool, error) {
	if notifier == nil {
		return false, nil
	}
	if !isAllowedTelegramChat(message.Chat.ID, allowedChatID) {
		return false, nil
	}

	cmd := extractTelegramCommand(strings.ToLower(strings.TrimSpace(message.Text)))
	if cmd != "/restart" {
		return false, nil
	}

	resetResult, err := agent.ResetTelegramRuntime(ctx, message.Chat.ID)
	if err != nil {
		return true, err
	}
	clearedProviderClients := providers.ResetHTTPClientCache()
	clearedStatusImages := clearTelegramStatusImageWindow()
	scheduleTelegramStatusImageRefillWithTrigger(RootContext(), "restart")
	closeTelegramClientIdleConnections(notifier.client)
	closeTelegramClientIdleConnections(pollClient)
	runtime.GC()

	slog.Info("TG Agent 已重启",
		"chat_id", message.Chat.ID,
		"conversation_id", resetResult.ConversationID,
		"cleared_sessions", resetResult.ClearedSessions,
		"cleared_provider_clients", clearedProviderClients,
		"cleared_status_images", clearedStatusImages,
	)
	content := strings.Join([]string{
		"TG Agent 已重启。",
		fmt.Sprintf("已开启新的对话，清理内存会话：%d 个。", resetResult.ClearedSessions),
		fmt.Sprintf("已释放模型连接缓存：%d 个，状态图片缓存：%d 张。", clearedProviderClients, clearedStatusImages),
	}, "\n")
	return true, notifier.sendText(content)
}

func handleTelegramStopCommand(ctx context.Context, notifier *telegramNotifier, message telegramMessage, allowedChatID string) (bool, error) {
	if notifier == nil {
		return false, nil
	}
	if !isAllowedTelegramChat(message.Chat.ID, allowedChatID) {
		return false, nil
	}

	cmd := extractTelegramCommand(strings.ToLower(strings.TrimSpace(message.Text)))
	if cmd != "/stop" {
		return false, nil
	}
	_ = ctx
	agent.StopTelegramReply(message.Chat.ID)
	return true, nil
}

func sendTelegramHelpMessage(ctx context.Context, notifier *telegramNotifier, chatID string) error {
	content := buildTelegramHelpMessage()
	return sendTelegramCaptionWithStatusImage(ctx, notifier, chatID, content)
}

func clearTelegramStatusImageWindow() int {
	telegramStatusImageWindowMu.Lock()
	defer telegramStatusImageWindowMu.Unlock()

	count := len(telegramStatusImageWindowItems)
	for index := range telegramStatusImageWindowItems {
		telegramStatusImageWindowItems[index] = telegramStatusImageItem{}
	}
	telegramStatusImageWindowItems = nil
	return count
}

func closeTelegramClientIdleConnections(client *http.Client) {
	if client == nil {
		return
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	closer, ok := transport.(interface{ CloseIdleConnections() })
	if !ok {
		return
	}
	closer.CloseIdleConnections()
}

type telegramAgentClient struct {
	notifier *telegramNotifier
}

func (c telegramAgentClient) SendMessage(ctx context.Context, chatID int64, text string) (int64, error) {
	if c.notifier == nil {
		return 0, errors.New("telegram notifier is nil")
	}
	return c.notifier.sendMarkdownTextToChatAndReturnMessageID(ctx, strconv.FormatInt(chatID, 10), text)
}

func (c telegramAgentClient) EditMessage(ctx context.Context, chatID int64, messageID int64, text string) error {
	if c.notifier == nil {
		return errors.New("telegram notifier is nil")
	}
	return c.notifier.editMarkdownMessageText(chatID, messageID, text)
}

func (c telegramAgentClient) DeleteMessage(ctx context.Context, chatID int64, messageID int64) error {
	if c.notifier == nil {
		return errors.New("telegram notifier is nil")
	}
	return c.notifier.postTelegramMethod(ctx, "deleteMessage", telegramDeleteMessageRequest{
		ChatID:    strconv.FormatInt(chatID, 10),
		MessageID: messageID,
	})
}

func (c telegramAgentClient) SendTyping(ctx context.Context, chatID int64) error {
	if c.notifier == nil {
		return errors.New("telegram notifier is nil")
	}
	return c.notifier.sendChatAction(ctx, strconv.FormatInt(chatID, 10), "typing")
}

func (c telegramAgentClient) SendPhoto(ctx context.Context, chatID int64, source string, caption string) error {
	if c.notifier == nil {
		return errors.New("telegram notifier is nil")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("图片来源不能为空")
	}
	chatIDText := strconv.FormatInt(chatID, 10)
	if isTelegramAgentAttachmentURL(source) {
		return c.notifier.sendPhotoURLToChatWithoutCaptionWidening(ctx, chatIDText, source, caption)
	}
	data, filename, err := readTelegramAgentAttachmentFile(source, telegramAgentPhotoMaxBytes)
	if err != nil {
		return err
	}
	return c.notifier.sendMultipartBinaryToChatWithoutCaptionWidening(ctx, "sendPhoto", "photo", chatIDText, filename, data, caption, "", telegramPhotoHTTPTimeout)
}

func (c telegramAgentClient) SendDocument(ctx context.Context, chatID int64, source string, caption string) error {
	if c.notifier == nil {
		return errors.New("telegram notifier is nil")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("文件来源不能为空")
	}
	chatIDText := strconv.FormatInt(chatID, 10)
	if isTelegramAgentAttachmentURL(source) {
		return c.notifier.sendDocumentURLToChat(ctx, chatIDText, source, caption)
	}
	data, filename, err := readTelegramAgentAttachmentFile(source, telegramAgentDocumentMaxBytes)
	if err != nil {
		return err
	}
	return c.notifier.sendDocumentBinaryToChat(ctx, chatIDText, filename, data, caption)
}

func handleTelegramAgentMessage(ctx context.Context, notifier *telegramNotifier, message telegramMessage, allowedChatID string) (bool, error) {
	if notifier == nil {
		return false, nil
	}
	if !isAllowedTelegramChat(message.Chat.ID, allowedChatID) {
		return false, nil
	}
	attachments, err := buildTelegramAgentInputAttachments(ctx, notifier, message)
	if err != nil {
		if len(message.Photo) > 0 || message.Document != nil {
			_, _ = telegramAgentClient{notifier: notifier}.SendMessage(ctx, message.Chat.ID, "读取图片失败："+err.Error())
			return true, err
		}
		return false, nil
	}
	if !shouldDispatchTelegramAgentMessage(message, attachments) {
		return false, nil
	}
	agentMessage := agent.TelegramMessage{
		ChatID:      message.Chat.ID,
		MessageID:   message.MessageID,
		Text:        resolveTelegramMessageText(message),
		Attachments: attachments,
	}
	pkg.GoSafe("service.telegram_agent_message", func() {
		handled, err := agent.HandleTelegramMessage(ctx, telegramAgentClient{notifier: notifier}, agentMessage)
		if err != nil {
			slog.Warn("处理 TG Agent 消息失败", "chat_id", message.Chat.ID, "message_id", message.MessageID, "error", err)
			return
		}
		if !handled {
			slog.Debug("TG Agent 消息未处理", "chat_id", message.Chat.ID, "message_id", message.MessageID)
		}
	})
	return true, nil
}

func shouldDispatchTelegramAgentMessage(message telegramMessage, attachments []agent.TelegramInputAttachment) bool {
	raw := strings.TrimSpace(resolveTelegramMessageText(message))
	if raw == "" {
		return len(attachments) > 0
	}
	cmd := extractTelegramCommand(strings.ToLower(raw))
	switch cmd {
	case "":
		return true
	case "/new", "/reset", "/img":
		return true
	default:
		return false
	}
}

func resolveTelegramMessageText(message telegramMessage) string {
	if text := strings.TrimSpace(message.Text); text != "" {
		return text
	}
	return strings.TrimSpace(message.Caption)
}

func buildTelegramAgentInputAttachments(ctx context.Context, notifier *telegramNotifier, message telegramMessage) ([]agent.TelegramInputAttachment, error) {
	if notifier == nil {
		return nil, errors.New("telegram notifier is nil")
	}
	attachments := make([]agent.TelegramInputAttachment, 0, 2)
	if photo, ok := selectTelegramLargestPhoto(message.Photo); ok {
		attachment, err := notifier.downloadTelegramAgentInputImage(ctx, photo.FileID, "telegram-photo-"+telegramTextFallback(photo.FileUniqueID, strconv.FormatInt(message.MessageID, 10))+".jpg", "image/jpeg", photo.FileSize)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if message.Document != nil && isTelegramImageDocument(*message.Document) {
		document := *message.Document
		fileName := strings.TrimSpace(document.FileName)
		if fileName == "" {
			fileName = "telegram-document-" + telegramTextFallback(document.FileUniqueID, strconv.FormatInt(message.MessageID, 10))
		}
		attachment, err := notifier.downloadTelegramAgentInputImage(ctx, document.FileID, fileName, document.MIMEType, document.FileSize)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	if len(attachments) > telegramAgentInputImageMaxCount {
		return attachments[:telegramAgentInputImageMaxCount], nil
	}
	return attachments, nil
}

func selectTelegramLargestPhoto(photos []telegramPhotoSize) (telegramPhotoSize, bool) {
	var selected telegramPhotoSize
	for _, photo := range photos {
		if strings.TrimSpace(photo.FileID) == "" {
			continue
		}
		if strings.TrimSpace(selected.FileID) == "" || telegramPhotoScore(photo) > telegramPhotoScore(selected) {
			selected = photo
		}
	}
	return selected, strings.TrimSpace(selected.FileID) != ""
}

func telegramPhotoScore(photo telegramPhotoSize) int64 {
	if photo.FileSize > 0 {
		return photo.FileSize
	}
	return int64(photo.Width) * int64(photo.Height)
}

func isTelegramImageDocument(document telegramDocumentFile) bool {
	mimeType := strings.ToLower(strings.TrimSpace(document.MIMEType))
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	guessedType := strings.ToLower(mime.TypeByExtension(filepath.Ext(document.FileName)))
	return strings.HasPrefix(guessedType, "image/")
}

func (n *telegramNotifier) downloadTelegramAgentInputImage(ctx context.Context, fileID string, fileName string, mimeType string, declaredSize int64) (agent.TelegramInputAttachment, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return agent.TelegramInputAttachment{}, errors.New("Telegram 文件 ID 为空")
	}
	if declaredSize > telegramAgentInputImageMaxBytes {
		return agent.TelegramInputAttachment{}, fmt.Errorf("图片超过大小限制: %d > %d", declaredSize, telegramAgentInputImageMaxBytes)
	}
	data, filePath, detectedType, err := n.downloadTelegramFile(ctx, fileID, telegramAgentInputImageMaxBytes)
	if err != nil {
		return agent.TelegramInputAttachment{}, err
	}
	if strings.TrimSpace(fileName) == "" {
		fileName = filepath.Base(filePath)
	}
	if strings.TrimSpace(fileName) == "" || fileName == "." {
		fileName = "telegram-image"
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = detectedType
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(fileName))
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "image/") {
		return agent.TelegramInputAttachment{}, fmt.Errorf("Telegram 文件不是图片: %s", mimeType)
	}
	return agent.TelegramInputAttachment{
		FileName: filepath.Base(fileName),
		MIMEType: mimeType,
		Data:     data,
	}, nil
}

func (n *telegramNotifier) downloadTelegramFile(ctx context.Context, fileID string, maxBytes int64) ([]byte, string, string, error) {
	if n == nil {
		return nil, "", "", errors.New("telegram notifier is nil")
	}
	filePath, err := n.getTelegramFilePath(ctx, fileID)
	if err != nil {
		return nil, "", "", err
	}
	fileURL := n.telegramFileURL(filePath)
	if fileURL == "" {
		return nil, "", "", errors.New("Telegram 文件下载地址为空")
	}
	reqCtx, cancel := context.WithTimeout(ctx, telegramPhotoHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	res, err := n.client.Do(req)
	if err != nil {
		return nil, "", "", fmt.Errorf("下载 Telegram 文件失败: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, "", "", fmt.Errorf("下载 Telegram 文件返回状态 %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("读取 Telegram 文件失败: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, "", "", fmt.Errorf("图片超过大小限制: %d > %d", len(raw), maxBytes)
	}
	return raw, filePath, strings.TrimSpace(res.Header.Get("Content-Type")), nil
}

func (n *telegramNotifier) getTelegramFilePath(ctx context.Context, fileID string) (string, error) {
	methodURL := n.telegramMethodURL("getFile")
	if methodURL == "" {
		return "", errors.New("Telegram getFile 地址为空")
	}
	reqCtx, cancel := context.WithTimeout(ctx, telegramHTTPTimeout)
	defer cancel()
	parsed, err := url.Parse(methodURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("file_id", strings.TrimSpace(fileID))
	parsed.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	res, err := n.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("调用 Telegram getFile 失败: %w", err)
	}
	defer res.Body.Close()
	var response telegramGetFileResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&response); err != nil {
		return "", fmt.Errorf("解析 Telegram getFile 响应失败: %w", err)
	}
	if res.StatusCode != http.StatusOK || !response.OK {
		if strings.TrimSpace(response.Description) == "" {
			response.Description = res.Status
		}
		return "", fmt.Errorf("Telegram getFile 失败: %s", response.Description)
	}
	if strings.TrimSpace(response.Result.FilePath) == "" {
		return "", errors.New("Telegram getFile 未返回 file_path")
	}
	if response.Result.FileSize > telegramAgentInputImageMaxBytes {
		return "", fmt.Errorf("图片超过大小限制: %d > %d", response.Result.FileSize, telegramAgentInputImageMaxBytes)
	}
	return response.Result.FilePath, nil
}

func (n *telegramNotifier) telegramFileURL(filePath string) string {
	if n == nil || strings.TrimSpace(n.apiBase) == "" || strings.TrimSpace(n.botToken) == "" {
		return ""
	}
	filePath = strings.TrimLeft(strings.TrimSpace(filePath), "/")
	if filePath == "" {
		return ""
	}
	return fmt.Sprintf("%s/file/bot%s/%s", strings.TrimRight(n.apiBase, "/"), strings.TrimSpace(n.botToken), escapeTelegramFilePath(filePath))
}

func escapeTelegramFilePath(filePath string) string {
	parts := strings.Split(filePath, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func telegramTextFallback(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func isTelegramAgentAttachmentURL(source string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")
}

func readTelegramAgentAttachmentFile(source string, maxBytes int64) ([]byte, string, error) {
	path, err := normalizeTelegramAgentAttachmentPath(source)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("读取附件失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("附件必须是普通文件")
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, "", fmt.Errorf("附件超过大小限制: %d > %d", info.Size(), maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("读取附件内容失败: %w", err)
	}
	return data, filepath.Base(path), nil
}

func normalizeTelegramAgentAttachmentPath(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("附件路径不能为空")
	}
	if strings.HasPrefix(strings.ToLower(source), "file://") {
		parsed, err := url.Parse(source)
		if err != nil {
			return "", fmt.Errorf("附件 file URL 无效: %w", err)
		}
		source = parsed.Path
	}
	absPath, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("附件路径无效: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("附件路径不可用: %w", err)
	}
	if !isTelegramAgentAttachmentPathAllowed(realPath) {
		return "", errors.New("附件路径不在允许目录内")
	}
	return realPath, nil
}

func isTelegramAgentAttachmentPathAllowed(path string) bool {
	roots := []string{}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
	}
	if tmpDir := strings.TrimSpace(os.TempDir()); tmpDir != "" {
		roots = append(roots, tmpDir)
	}
	for _, root := range roots {
		rootPath, err := filepath.EvalSymlinks(root)
		if err != nil {
			rootPath, err = filepath.Abs(root)
			if err != nil {
				continue
			}
		}
		if isPathWithinRoot(path, rootPath) {
			return true
		}
	}
	return false
}

func isPathWithinRoot(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func sendTelegramCaptionWithStatusImage(ctx context.Context, notifier *telegramNotifier, chatID string, content string) error {
	return sendTelegramCaptionWithStatusImageWithParseMode(ctx, notifier, chatID, content, "")
}

func sendTelegramCaptionWithStatusImageWithoutWidening(ctx context.Context, notifier *telegramNotifier, chatID string, content string) error {
	return sendTelegramCaptionWithStatusImageOptions(ctx, notifier, chatID, content, "", false)
}

func sendTelegramCaptionWithStatusImageWithParseMode(ctx context.Context, notifier *telegramNotifier, chatID string, content string, parseMode string) error {
	return sendTelegramCaptionWithStatusImageOptions(ctx, notifier, chatID, content, parseMode, true)
}

func sendTelegramCaptionWithStatusImageOptions(ctx context.Context, notifier *telegramNotifier, chatID string, content string, parseMode string, widenCaption bool) error {
	if notifier == nil {
		return errors.New("telegram notifier is nil")
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return errors.New("telegram chat id is empty")
	}
	content = strings.TrimSpace(content)

	item, ok := popTelegramStatusImageWindowItem(ctx)
	if !ok {
		baseURL := resolveTelegramStatusCoverImageBaseURL(ctx)
		photoBinary, fileName, sourceURL, err := downloadTelegramStatusCoverImage(ctx, baseURL)
		if err != nil {
			slog.Error("下载 TG 状态图片失败", "base_url", baseURL, "error", err)
			// 下载图片失败时回退到纯文本，避免关键消息丢失。
			if fallbackErr := notifier.sendTextWithMarkupToChatWithParseMode(chatID, content, nil, parseMode); fallbackErr != nil {
				return fmt.Errorf("下载状态图片失败: %v, 文本回退也失败: %w", err, fallbackErr)
			}
			return nil
		}
		item = telegramStatusImageItem{
			Binary:   photoBinary,
			FileName: fileName,
			Source:   sourceURL,
		}
	}
	defer clearTelegramStatusBinary(item.Binary)

	var sendErr error
	if widenCaption {
		sendErr = notifier.sendPhotoBinaryToChatWithParseMode(ctx, chatID, item.FileName, item.Binary, content, parseMode)
	} else {
		sendErr = notifier.sendMultipartBinaryToChatWithoutCaptionWidening(ctx, "sendPhoto", "photo", chatID, item.FileName, item.Binary, content, parseMode, telegramPhotoHTTPTimeout)
	}
	if sendErr != nil {
		// 图片发送失败时回退到纯文本，避免关键消息丢失。
		if fallbackErr := notifier.sendTextWithMarkupToChatWithParseMode(chatID, content, nil, parseMode); fallbackErr != nil {
			return fmt.Errorf("发送状态图片失败: %v, 文本回退也失败: %w", sendErr, fallbackErr)
		}
		return nil
	}

	scheduleTelegramStatusImageRefill(ctx)
	return nil
}

func buildTelegramYesterdayUsageReportMessage(ctx context.Context, now time.Time) string {
	start, end := resolveTelegramYesterdayRange(now)
	summary := collectTelegramDailyUsageSummary(ctx, start, end)
	yesterdayText := start.Format("2006-01-02")

	lines := []string{
		"【🌞 Orvion 昨日使用小报】\n",
		fmt.Sprintf("早安呀～这是 %s 的可爱小账本 ✨", yesterdayText),
		"━━━━━━━━━━━━━━━━",
		fmt.Sprintf("💸 花费：$%.4f", summary.TotalCost),
	}

	if summary.TopModelName != "" && summary.TopModelReqs > 0 {
		lines = append(lines, fmt.Sprintf("🔥 最活跃模型：%s（%d 次）", summary.TopModelName, summary.TopModelReqs))
	} else {
		lines = append(lines, "🔥 最活跃模型：昨天还没有请求，先攒攒能量~")
	}
	if summary.TopAuthKeyName != "" && summary.TopAuthKeyReqs > 0 {
		lines = append(lines, fmt.Sprintf("🔑 最常用 API Key：%s（%d 次）", summary.TopAuthKeyName, summary.TopAuthKeyReqs))
	} else {
		lines = append(lines, "🔑 最常用 API Key：昨天没有 API Key 请求记录")
	}

	if summary.SlowestRequest != nil {
		statusText := telegramReadableStatus(summary.SlowestRequest.Status)
		lines = append(lines, fmt.Sprintf("🐢 最慢一次请求：%s / %s（%s，%s）",
			summary.SlowestRequest.ModelName,
			summary.SlowestRequest.Provider,
			formatTelegramLatency(summary.SlowestRequest.LatencyMs),
			statusText,
		))
	} else {
		lines = append(lines, "🐢 最慢一次请求：暂无记录，速度小火箭待命中~")
	}

	if summary.TotalRequests > 0 {
		failed := summary.TotalRequests - summary.SuccessRequests
		if failed < 0 {
			failed = 0
		}
		lines = append(lines, fmt.Sprintf("📦 请求总数：%d（成功 %d / 失败 %d）", summary.TotalRequests, summary.SuccessRequests, failed))
	}
	lines = append(lines, "💌 今天也一起稳定发挥、少花钱多产出～")
	return strings.Join(lines, "\n")
}

func resolveTelegramYesterdayRange(now time.Time) (time.Time, time.Time) {
	y, m, d := now.Date()
	startOfToday := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	startOfYesterday := startOfToday.AddDate(0, 0, -1)
	return startOfYesterday, startOfToday
}

func collectTelegramDailyUsageSummary(ctx context.Context, start time.Time, end time.Time) telegramDailyUsageSummary {
	summary := telegramDailyUsageSummary{
		StartDate: start,
		EndDate:   end,
	}
	if models.DB == nil {
		return summary
	}

	dailyStats, err := models.QueryChatLogDailyUsageSummary(ctx, start, end)
	if err != nil {
		return summary
	}
	summary.TotalRequests = dailyStats.TotalRequests
	summary.SuccessRequests = dailyStats.SuccessRequests
	summary.TotalCost = dailyStats.TotalCost
	summary.TopModelName = strings.TrimSpace(dailyStats.TopModelName)
	summary.TopModelReqs = dailyStats.TopModelReqs

	if dailyStats.TopAuthKeyID > 0 {
		summary.TopAuthKeyReqs = dailyStats.TopAuthKeyReqs
		summary.TopAuthKeyName = fmt.Sprintf("ID:%d", dailyStats.TopAuthKeyID)

		var authKey models.AuthKey
		if findErr := models.DB.WithContext(ctx).
			Where("id = ?", dailyStats.TopAuthKeyID).
			First(&authKey).Error; findErr == nil {
			name := strings.TrimSpace(authKey.Name)
			if name != "" {
				summary.TopAuthKeyName = name
			}
		}
	}

	if dailyStats.SlowestRequest != nil {
		slowRow := dailyStats.SlowestRequest
		latency := slowRow.LatencyMs
		if latency < 0 {
			latency = 0
		}
		modelName := strings.TrimSpace(slowRow.Name)
		if modelName == "" {
			modelName = "unknown-model"
		}
		providerName := strings.TrimSpace(slowRow.Provider)
		if providerName == "" {
			providerName = "unknown-provider"
		}
		summary.SlowestRequest = &telegramDailySlowRequest{
			ID:        slowRow.ID,
			ModelName: modelName,
			Provider:  providerName,
			Status:    strings.TrimSpace(slowRow.Status),
			LatencyMs: latency,
			CreatedAt: slowRow.CreatedAt,
		}
	}

	return summary
}

func telegramReadableStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return "成功"
	case "error":
		return "失败"
	default:
		return "未知状态"
	}
}

func formatTelegramLatency(latencyMs int) string {
	if latencyMs <= 0 {
		return "0 ms"
	}
	if latencyMs < 1000 {
		return fmt.Sprintf("%d ms", latencyMs)
	}
	return fmt.Sprintf("%.2f s", float64(latencyMs)/1000)
}

func resolveTelegramStatusCoverImageBaseURL(ctx context.Context) string {
	fallbackURL := strings.TrimSpace(telegramDefaultStatusCoverImageURL)

	cfg, found, err := loadTelegramBreakerAlertConfig(ctx)
	if err != nil {
		slog.Warn("读取 TG 状态图片配置失败，回退默认地址", "error", err)
		return fallbackURL
	}
	if !found {
		return fallbackURL
	}

	configURL := strings.TrimSpace(cfg.StatusImageURL)
	if normalizedConfigURL, ok := normalizeTelegramStatusImageURL(configURL); ok {
		return normalizedConfigURL
	}
	if configURL != "" {
		slog.Warn("TG 状态图片配置无效，回退默认地址", "value", configURL)
	}
	return fallbackURL
}

func resolveTelegramStatusImageProxyURL(ctx context.Context) string {
	cfg, found, err := loadNetworkForwardingConfig(ctx)
	if err != nil {
		slog.Warn("读取 TG 状态图片代理配置失败", "error", err)
		return ""
	}
	if !found {
		return ""
	}

	configProxyURL := strings.TrimSpace(cfg.TelegramProxyURL)
	if configProxyURL != "" {
		return configProxyURL
	}
	return ""
}

func normalizeTelegramStatusImageURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", false
	}
	return raw, true
}

func buildTelegramStatusCoverImageURL(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return strings.TrimSpace(baseURL)
	}
	query := parsed.Query()
	// 某些图片源参数会重复（例如 wtf_gender），去重后再追加时间戳，避免异常命中风控或缓存。
	for key, values := range query {
		if len(values) <= 1 {
			continue
		}
		chosen := strings.TrimSpace(values[len(values)-1])
		if chosen == "" {
			chosen = values[len(values)-1]
		}
		query.Set(key, chosen)
	}
	query.Set("_ts", strconv.FormatInt(time.Now().UnixNano(), 10))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func downloadTelegramStatusCoverImage(ctx context.Context, baseURL string) ([]byte, string, string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, "", "", fmt.Errorf("状态图片地址为空")
	}

	directClient := &http.Client{Timeout: telegramStatusImageDownloadTimeout}
	proxyURL := resolveTelegramStatusImageProxyURL(ctx)
	var proxyClient *http.Client
	if proxyURL != "" {
		client, err := buildTelegramHTTPClientWithTimeout(proxyURL, telegramStatusImageDownloadTimeout)
		if err != nil {
			slog.Warn("TG 状态图片代理配置无效，跳过代理重试", "proxy_url", proxyURL, "error", err)
		} else {
			proxyClient = client
		}
	}

	var lastErr error
	for attempt := 1; attempt <= telegramStatusImageDownloadRetry; attempt++ {
		urlToDownload := buildTelegramStatusCoverImageURL(baseURL)

		binary, fileName, err := downloadTelegramStatusCoverImageOnce(ctx, directClient, urlToDownload, false)
		if err == nil {
			return binary, fileName, urlToDownload, nil
		}
		if proxyClient != nil && !errors.Is(err, errTelegramStatusImageTooLarge) {
			slog.Warn("下载 /status 状态图片直连失败，尝试代理获取",
				"url", urlToDownload,
				"attempt", attempt,
				"max_attempt", telegramStatusImageDownloadRetry,
				"error", err,
			)
			proxyBinary, proxyFileName, proxyErr := downloadTelegramStatusCoverImageOnce(ctx, proxyClient, urlToDownload, true)
			if proxyErr == nil {
				return proxyBinary, proxyFileName, urlToDownload, nil
			}
			slog.Warn("下载 /status 状态图片代理获取失败",
				"url", urlToDownload,
				"attempt", attempt,
				"max_attempt", telegramStatusImageDownloadRetry,
				"error", proxyErr,
			)
			err = fmt.Errorf("直连失败: %w; 代理失败: %v", err, proxyErr)
		}

		lastErr = err
		if attempt >= telegramStatusImageDownloadRetry {
			break
		}
		if errors.Is(err, errTelegramStatusImageTooLarge) {
			slog.Warn("下载 /status 状态图片超限，跳过并重试",
				"url", urlToDownload,
				"attempt", attempt,
				"max_attempt", telegramStatusImageDownloadRetry,
				"limit_bytes", telegramStatusImageTGMaxBytes,
				"error", err,
			)
			continue
		}
		if !isRetryableStatusImageDownloadError(err) {
			return nil, "", "", err
		}
		wait := telegramStatusImageRetryBaseWait * time.Duration(attempt)
		slog.Warn("下载 /status 状态图片失败，准备重试",
			"url", urlToDownload,
			"attempt", attempt,
			"max_attempt", telegramStatusImageDownloadRetry,
			"next_wait", wait.String(),
			"error", err,
		)
		if !waitWithContext(ctx, wait) {
			return nil, "", "", ctx.Err()
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("下载状态图片失败")
	}
	return nil, "", "", fmt.Errorf("状态图片多次下载失败: %w", lastErr)
}

func downloadTelegramStatusCoverImageOnce(ctx context.Context, client *http.Client, rawURL string, useProxy bool) ([]byte, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, telegramStatusImageDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Expires", "0")

	if client == nil {
		client = &http.Client{Timeout: telegramStatusImageDownloadTimeout}
	}
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
	slog.Info("下载 /status 状态图片成功", "url", rawURL, "filename", filename, "bytes", len(binary), "use_proxy", useProxy)
	return binary, filename, nil
}

func isRetryableStatusImageDownloadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timeout") {
		return true
	}
	if strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") {
		return true
	}
	if strings.Contains(msg, "status=429") {
		return true
	}
	for code := 500; code <= 599; code++ {
		if strings.Contains(msg, fmt.Sprintf("status=%d", code)) {
			return true
		}
	}
	return false
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
		Joins("LEFT JOIN providers ON providers.id = model_with_providers.provider_id").
		Where("model_with_providers.model_id = ?", modelID).
		Order("model_with_providers.id ASC").
		Scan(&rows).Error; err != nil {
		return "", nil, err
	}

	successRateByModelProvider := make(map[uint]float64, len(rows))
	if len(rows) > 0 {
		ids := make([]uint, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		stats, err := models.QueryChatLogModelProviderSuccessStats(ctx, ids)
		if err == nil {
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
