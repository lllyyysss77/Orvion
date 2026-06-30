package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	agenttools "github.com/racio/orvion/agent/tools"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	TelegramAgentScheduleTypeInterval = agenttools.TelegramAgentScheduleTypeInterval
	TelegramAgentScheduleTypeDaily    = agenttools.TelegramAgentScheduleTypeDaily

	telegramAgentScheduledTaskToolName = "telegram_agent_scheduled_task"
	telegramAgentScheduledTaskSource   = "scheduled_task"
	telegramAgentScheduledTaskAction   = "scheduled_task"
	telegramAgentScheduledTaskStaleRun = 2 * time.Hour
	telegramAgentScheduledTaskMaxText  = 30 * 1024
)

var telegramAgentScheduledTaskClaimMu sync.Mutex

type TelegramAgentScheduledTaskRunResult struct {
	Text   string
	ChatID int64
	Pushed bool
}

func NormalizeTelegramAgentScheduledTaskForSave(task *models.TelegramAgentScheduledTask, now time.Time, refreshNextRun bool) error {
	task.Name = strings.TrimSpace(task.Name)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.ScheduleType = normalizeTelegramAgentScheduledTaskType(task.ScheduleType)
	task.TimeOfDay = strings.TrimSpace(task.TimeOfDay)
	task.Timezone = strings.TrimSpace(task.Timezone)
	task.LastStatus = strings.TrimSpace(task.LastStatus)

	if task.Name == "" {
		return errors.New("任务名称不能为空")
	}
	if task.Prompt == "" {
		return errors.New("任务内容不能为空")
	}
	if task.Enabled != 1 {
		task.Enabled = 0
	}
	if task.PushToConversation != 1 {
		task.PushToConversation = 0
	}
	if task.Timezone == "" {
		task.Timezone = "Local"
	}

	switch task.ScheduleType {
	case TelegramAgentScheduleTypeInterval:
		if task.IntervalMinutes <= 0 {
			return errors.New("间隔分钟必须大于 0")
		}
		task.TimeOfDay = ""
	case TelegramAgentScheduleTypeDaily:
		if _, _, err := parseTelegramAgentScheduledTaskTimeOfDay(task.TimeOfDay); err != nil {
			return err
		}
		task.IntervalMinutes = 0
	default:
		return errors.New("定时类型无效")
	}

	if refreshNextRun {
		nextRunAt, err := CalculateTelegramAgentScheduledTaskNextRunAt(*task, now)
		if err != nil {
			return err
		}
		task.NextRunAt = &nextRunAt
	}
	return nil
}

func CalculateTelegramAgentScheduledTaskNextRunAt(task models.TelegramAgentScheduledTask, from time.Time) (time.Time, error) {
	if from.IsZero() {
		from = time.Now()
	}
	switch normalizeTelegramAgentScheduledTaskType(task.ScheduleType) {
	case TelegramAgentScheduleTypeInterval:
		if task.IntervalMinutes <= 0 {
			return time.Time{}, errors.New("间隔分钟必须大于 0")
		}
		return from.Add(time.Duration(task.IntervalMinutes) * time.Minute), nil
	case TelegramAgentScheduleTypeDaily:
		hour, minute, err := parseTelegramAgentScheduledTaskTimeOfDay(task.TimeOfDay)
		if err != nil {
			return time.Time{}, err
		}
		location, err := telegramAgentScheduledTaskLocation(task.Timezone)
		if err != nil {
			return time.Time{}, err
		}
		localFrom := from.In(location)
		year, month, day := localFrom.Date()
		next := time.Date(year, month, day, hour, minute, 0, 0, location)
		if !next.After(localFrom) {
			next = next.AddDate(0, 0, 1)
		}
		return next, nil
	default:
		return time.Time{}, errors.New("定时类型无效")
	}
}

func ClaimDueTelegramAgentScheduledTasks(ctx context.Context, now time.Time, limit int) ([]models.TelegramAgentScheduledTask, error) {
	if models.DB == nil {
		return nil, errors.New("数据库未初始化")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 {
		limit = 5
	}

	telegramAgentScheduledTaskClaimMu.Lock()
	defer telegramAgentScheduledTaskClaimMu.Unlock()

	staleBefore := now.Add(-telegramAgentScheduledTaskStaleRun)
	if err := models.DB.WithContext(ctx).
		Model(&models.TelegramAgentScheduledTask{}).
		Where("running = ? AND updated_at < ?", 1, staleBefore).
		Updates(map[string]any{
			"running":     0,
			"last_status": "error",
			"last_error":  "上一次执行超时未释放，已自动解锁",
		}).Error; err != nil {
		return nil, err
	}

	var rows []models.TelegramAgentScheduledTask
	if err := models.DB.WithContext(ctx).
		Where("enabled = ? AND running = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", 1, 0, now).
		Order("next_run_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	claimed := make([]models.TelegramAgentScheduledTask, 0, len(rows))
	for _, row := range rows {
		runAt := now
		result := models.DB.WithContext(ctx).
			Model(&models.TelegramAgentScheduledTask{}).
			Where("id = ? AND running = ?", row.ID, 0).
			Updates(map[string]any{
				"running":     1,
				"last_run_at": runAt,
				"last_status": "running",
				"last_error":  "",
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		row.Running = 1
		row.LastRunAt = &runAt
		row.LastStatus = "running"
		row.LastError = ""
		claimed = append(claimed, row)
	}
	return claimed, nil
}

func ExecuteTelegramAgentScheduledTask(ctx context.Context, task models.TelegramAgentScheduledTask, client TelegramClient, defaultChatID int64) (TelegramAgentScheduledTaskRunResult, error) {
	result := TelegramAgentScheduledTaskRunResult{
		ChatID: resolveTelegramAgentScheduledTaskChatID(task, defaultChatID),
		Pushed: task.PushToConversation == 1,
	}

	cfg, err := loadTelegramAgentConfig(ctx)
	if err != nil {
		return result, err
	}
	if !isTelegramAgentEnabled(cfg) {
		return result, errors.New("TG Agent 未启用")
	}

	prompt := buildTelegramAgentScheduledTaskPrompt(task)
	logID := recordTelegramAgentScheduledTaskExecutingLog(ctx, task, result.ChatID)
	if task.PushToConversation == 1 {
		if client == nil {
			err = errors.New("推送到对话需要 Telegram 客户端")
			finishTelegramAgentScheduledTaskLog(ctx, logID, task, result, err)
			return result, err
		}
		if result.ChatID == 0 {
			err = errors.New("推送到对话需要配置 Telegram Chat ID")
			finishTelegramAgentScheduledTaskLog(ctx, logID, task, result, err)
			return result, err
		}
		err = runTelegramAgentConversationWithHistoryMode(ctx, client, result.ChatID, prompt, nil, cfg, false)
		if err != nil {
			finishTelegramAgentScheduledTaskLog(ctx, logID, task, result, err)
			return result, err
		}
		result.Text = "已推送到 Agent 对话"
		finishTelegramAgentScheduledTaskLog(ctx, logID, task, result, nil)
		return result, nil
	}

	answer, err := runTelegramAgentScheduledTaskSilently(ctx, cfg, prompt)
	result.Text = answer
	finishTelegramAgentScheduledTaskLog(ctx, logID, task, result, err)
	return result, err
}

func FinishTelegramAgentScheduledTask(ctx context.Context, task models.TelegramAgentScheduledTask, result TelegramAgentScheduledTaskRunResult, runErr error, finishedAt time.Time) error {
	if models.DB == nil {
		return errors.New("数据库未初始化")
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	nextRunAt, nextErr := CalculateTelegramAgentScheduledTaskNextRunAt(task, finishedAt)
	if runErr == nil && nextErr != nil {
		runErr = nextErr
	}

	status := "success"
	errorText := ""
	if runErr != nil {
		status = "error"
		errorText = runErr.Error()
	}

	updates := map[string]any{
		"running":     0,
		"last_status": status,
		"last_result": limitTelegramAgentScheduledTaskText(result.Text),
		"last_error":  limitTelegramAgentScheduledTaskText(errorText),
		"run_count":   gorm.Expr("COALESCE(run_count, 0) + 1"),
	}
	if nextErr == nil {
		updates["next_run_at"] = nextRunAt
	}
	if runErr != nil {
		updates["failure_count"] = gorm.Expr("COALESCE(failure_count, 0) + 1")
	}

	return models.DB.WithContext(ctx).
		Model(&models.TelegramAgentScheduledTask{}).
		Where("id = ?", task.ID).
		Updates(updates).Error
}

func runTelegramAgentScheduledTaskSilently(ctx context.Context, cfg models.TelegramAgentConfig, prompt string) (string, error) {
	pool, err := buildTelegramAgentDirectProviderPool(cfg, false)
	if err != nil {
		return "", err
	}

	var answer strings.Builder
	_, err = streamTelegramAgentPlainReplyWithPool(ctx, cfg, pool, nil, prompt, func(delta string) error {
		answer.WriteString(delta)
		return nil
	})
	finalAnswer := strings.TrimSpace(answer.String())
	if err != nil {
		return finalAnswer, err
	}
	if finalAnswer == "" {
		finalAnswer = "上游返回了空响应。"
	}
	return finalAnswer, nil
}

func recordTelegramAgentScheduledTaskExecutingLog(ctx context.Context, task models.TelegramAgentScheduledTask, chatID int64) uint {
	return agenttools.RecordToolCallLog(ctx, telegramAgentToolRuntime(), models.TelegramAgentToolCallLog{
		ChatID:        chatID,
		Source:        telegramAgentScheduledTaskSource,
		ToolName:      telegramAgentScheduledTaskToolName,
		Arguments:     telegramAgentScheduledTaskArguments(task),
		Status:        agenttools.ToolLogStatusExecuting,
		ActionKind:    telegramAgentScheduledTaskAction,
		ActionSummary: task.Name,
		OK:            0,
		Final:         0,
		ExecutedAt:    nil,
	})
}

func finishTelegramAgentScheduledTaskLog(ctx context.Context, logID uint, task models.TelegramAgentScheduledTask, result TelegramAgentScheduledTaskRunResult, err error) {
	status := agenttools.ToolLogStatusExecuted
	ok := 1
	errorText := ""
	text := result.Text
	if err != nil {
		status = agenttools.ToolLogStatusFailed
		ok = 0
		errorText = err.Error()
		text = errorText
	}
	now := time.Now()
	agenttools.UpdateToolCallLog(ctx, telegramAgentToolRuntime(), logID, models.TelegramAgentToolCallLog{
		ChatID:        result.ChatID,
		Source:        telegramAgentScheduledTaskSource,
		ToolName:      telegramAgentScheduledTaskToolName,
		Arguments:     telegramAgentScheduledTaskArguments(task),
		Result:        limitTelegramAgentScheduledTaskText(text),
		Status:        status,
		OK:            ok,
		Final:         1,
		ActionKind:    telegramAgentScheduledTaskAction,
		ActionSummary: task.Name,
		Error:         limitTelegramAgentScheduledTaskText(errorText),
		ExecutedAt:    &now,
	})
}

func telegramAgentScheduledTaskArguments(task models.TelegramAgentScheduledTask) string {
	payload := map[string]any{
		"name":                 task.Name,
		"schedule_type":        normalizeTelegramAgentScheduledTaskType(task.ScheduleType),
		"push_to_conversation": task.PushToConversation == 1,
	}
	if task.IntervalMinutes > 0 {
		payload["interval_minutes"] = task.IntervalMinutes
	}
	if strings.TrimSpace(task.TimeOfDay) != "" {
		payload["time_of_day"] = strings.TrimSpace(task.TimeOfDay)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func buildTelegramAgentScheduledTaskPrompt(task models.TelegramAgentScheduledTask) string {
	return strings.Join([]string{
		"这是一个 Orvion TG Agent 定时任务，请按任务要求执行并给出结果。",
		"",
		"任务名称：",
		task.Name,
		"",
		"任务内容：",
		task.Prompt,
	}, "\n")
}

func resolveTelegramAgentScheduledTaskChatID(task models.TelegramAgentScheduledTask, defaultChatID int64) int64 {
	if task.ChatID != 0 {
		return task.ChatID
	}
	return defaultChatID
}

func normalizeTelegramAgentScheduledTaskType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case TelegramAgentScheduleTypeDaily:
		return TelegramAgentScheduleTypeDaily
	default:
		return TelegramAgentScheduleTypeInterval
	}
}

func parseTelegramAgentScheduledTaskTimeOfDay(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("每天执行时间格式必须为 HH:mm")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, errors.New("每天执行时间格式必须为 HH:mm")
	}
	return hour, minute, nil
}

func telegramAgentScheduledTaskLocation(timezone string) (*time.Location, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" || strings.EqualFold(timezone, "local") {
		return time.Local, nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("时区无效: %s", timezone)
	}
	return location, nil
}

func limitTelegramAgentScheduledTaskText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= telegramAgentScheduledTaskMaxText {
		return value
	}
	return value[:telegramAgentScheduledTaskMaxText] + "\n...已截断"
}
