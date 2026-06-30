package tools

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/racio/orvion/models"
)

const (
	TelegramAgentScheduleTypeInterval = "interval"
	TelegramAgentScheduleTypeDaily    = "daily"
)

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
