package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func listTelegramAgentScheduledTasks(ctx context.Context, args telegramAgentToolCallArgs) (string, error) {
	if models.DB == nil {
		return "", errors.New("数据库未初始化")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	query := models.DB.WithContext(ctx).Model(&models.TelegramAgentScheduledTask{})
	filter := strings.TrimSpace(args.Query)
	if filter != "" {
		query = query.Where("name LIKE ? OR prompt LIKE ?", "%"+filter+"%", "%"+filter+"%")
	}
	switch strings.ToLower(strings.TrimSpace(args.Status)) {
	case "", "all":
	case "enabled":
		query = query.Where("enabled = ?", 1)
	case "disabled":
		query = query.Where("enabled = ?", 0)
	case "running":
		query = query.Where("running = ?", 1)
	default:
		return "", fmt.Errorf("状态筛选无效: %s", args.Status)
	}

	var rows []models.TelegramAgentScheduledTask
	if err := query.Order("enabled DESC").Order("next_run_at ASC").Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) == 0 {
		if filter == "" {
			return "当前没有 Agent 定时任务。", nil
		}
		return "没有找到匹配的 Agent 定时任务：" + filter, nil
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	var sb strings.Builder
	sb.WriteString("Agent 定时任务列表")
	if filter != "" {
		sb.WriteString("\n筛选：" + filter)
	}
	for _, task := range rows {
		sb.WriteString("\n- " + task.Name)
		sb.WriteString("｜" + telegramScheduledTaskStatusLabel(task))
		sb.WriteString("｜" + telegramScheduledTaskScheduleLabel(task))
		if task.PushToConversation == 1 {
			sb.WriteString("｜推送到对话")
		}
		if task.NextRunAt != nil && !task.NextRunAt.IsZero() {
			sb.WriteString("｜下次 " + formatTelegramAgentLogTime(*task.NextRunAt))
		}
		if strings.TrimSpace(task.LastStatus) != "" {
			sb.WriteString("｜最近 " + telegramScheduledTaskLastStatusLabel(task.LastStatus))
		}
	}
	if hasMore {
		sb.WriteString(fmt.Sprintf("\n\n仅显示前 %d 条，可加关键词继续筛选。", limit))
	}
	return sb.String(), nil
}

func buildTelegramCreateScheduledTaskAction(_ context.Context, chatID int64, args telegramAgentToolCallArgs) (telegramToolAction, error) {
	patch, summaryParts, err := buildTelegramScheduledTaskPatch(args, true)
	if err != nil {
		return telegramToolAction{}, err
	}
	if patch.Name == nil || strings.TrimSpace(*patch.Name) == "" {
		return telegramToolAction{}, errors.New("任务名称不能为空")
	}
	if patch.Prompt == nil || strings.TrimSpace(*patch.Prompt) == "" {
		return telegramToolAction{}, errors.New("任务内容不能为空")
	}

	return telegramToolAction{
		ChatID:             chatID,
		Kind:               telegramToolActionCreateScheduledTask,
		ScheduledTaskPatch: patch,
		Summary:            "创建 Agent 定时任务：" + *patch.Name + "\n配置：" + strings.Join(summaryParts, "；"),
		CreatedAt:          time.Now(),
	}, nil
}

func buildTelegramUpdateScheduledTaskAction(ctx context.Context, chatID int64, args telegramAgentToolCallArgs) (telegramToolAction, error) {
	task, err := findTelegramAgentScheduledTask(ctx, args.Target)
	if err != nil {
		return telegramToolAction{}, err
	}
	patch, summaryParts, err := buildTelegramScheduledTaskPatch(args, false)
	if err != nil {
		return telegramToolAction{}, err
	}
	if len(summaryParts) == 0 {
		return telegramToolAction{}, errors.New("请写明要修改的定时任务字段")
	}

	return telegramToolAction{
		ChatID:             chatID,
		Kind:               telegramToolActionUpdateScheduledTask,
		TargetID:           task.ID,
		TargetName:         task.Name,
		ScheduledTaskPatch: patch,
		Summary:            "更新 Agent 定时任务：" + task.Name + "\n变更：" + strings.Join(summaryParts, "；"),
		CreatedAt:          time.Now(),
	}, nil
}

func buildTelegramSetScheduledTaskStatusAction(ctx context.Context, chatID int64, args telegramAgentToolCallArgs) (telegramToolAction, error) {
	if args.Enabled == nil {
		return telegramToolAction{}, errors.New("缺少 enabled 参数")
	}
	task, err := findTelegramAgentScheduledTask(ctx, args.Target)
	if err != nil {
		return telegramToolAction{}, err
	}
	return telegramToolAction{
		ChatID:     chatID,
		Kind:       telegramToolActionSetScheduledTaskStatus,
		TargetID:   task.ID,
		TargetName: task.Name,
		Enabled:    *args.Enabled,
		Summary:    fmt.Sprintf("%s Agent 定时任务：%s", telegramStatusVerb(*args.Enabled), task.Name),
		CreatedAt:  time.Now(),
	}, nil
}

func buildTelegramScheduledTaskPatch(args telegramAgentToolCallArgs, create bool) (telegramScheduledTaskPatch, []string, error) {
	var patch telegramScheduledTaskPatch
	summaryParts := make([]string, 0)

	if args.Name != nil {
		name := strings.TrimSpace(*args.Name)
		if name == "" {
			return telegramScheduledTaskPatch{}, nil, errors.New("任务名称不能为空")
		}
		patch.Name = &name
		summaryParts = append(summaryParts, "名称 "+name)
	}
	if args.TaskPrompt != nil {
		prompt := strings.TrimSpace(*args.TaskPrompt)
		if prompt == "" {
			return telegramScheduledTaskPatch{}, nil, errors.New("任务内容不能为空")
		}
		patch.Prompt = &prompt
		summaryParts = append(summaryParts, "任务内容已更新")
	}
	if args.Enabled != nil {
		patch.Enabled = args.Enabled
		summaryParts = append(summaryParts, "状态 "+telegramScheduledTaskBoolLabel(*args.Enabled, "启用", "禁用"))
	}
	if args.ScheduleType != nil {
		scheduleType := normalizeTelegramAgentScheduledTaskType(*args.ScheduleType)
		patch.ScheduleType = &scheduleType
		summaryParts = append(summaryParts, "类型 "+telegramScheduledTaskScheduleTypeLabel(scheduleType))
	}
	if args.IntervalMinutes != nil {
		if *args.IntervalMinutes <= 0 {
			return telegramScheduledTaskPatch{}, nil, errors.New("间隔分钟必须大于 0")
		}
		patch.IntervalMinutes = args.IntervalMinutes
		if patch.ScheduleType == nil {
			scheduleType := TelegramAgentScheduleTypeInterval
			patch.ScheduleType = &scheduleType
			summaryParts = append(summaryParts, "类型 间隔")
		}
		summaryParts = append(summaryParts, fmt.Sprintf("间隔 %d 分钟", *args.IntervalMinutes))
	}
	if args.TimeOfDay != nil {
		timeOfDay := strings.TrimSpace(*args.TimeOfDay)
		if _, _, err := parseTelegramAgentScheduledTaskTimeOfDay(timeOfDay); err != nil {
			return telegramScheduledTaskPatch{}, nil, err
		}
		patch.TimeOfDay = &timeOfDay
		if patch.ScheduleType == nil {
			scheduleType := TelegramAgentScheduleTypeDaily
			patch.ScheduleType = &scheduleType
			summaryParts = append(summaryParts, "类型 每天")
		}
		summaryParts = append(summaryParts, "每天 "+timeOfDay)
	}
	if args.Timezone != nil {
		timezone := strings.TrimSpace(*args.Timezone)
		if timezone == "" {
			timezone = "Local"
		}
		if _, err := telegramAgentScheduledTaskLocation(timezone); err != nil {
			return telegramScheduledTaskPatch{}, nil, err
		}
		patch.Timezone = &timezone
		summaryParts = append(summaryParts, "时区 "+timezone)
	}
	if args.PushToConversation != nil {
		patch.PushToConversation = args.PushToConversation
		summaryParts = append(summaryParts, "推送到对话 "+telegramScheduledTaskBoolLabel(*args.PushToConversation, "开启", "关闭"))
	}
	if args.ClearChatID {
		patch.ClearChatID = true
		summaryParts = append(summaryParts, "Chat ID 改为默认")
	} else if args.ChatID != nil {
		chatID := *args.ChatID
		if chatID < 0 {
			return telegramScheduledTaskPatch{}, nil, errors.New("Chat ID 不能为负数")
		}
		patch.ChatID = &chatID
		if chatID == 0 {
			summaryParts = append(summaryParts, "Chat ID 改为默认")
		} else {
			summaryParts = append(summaryParts, fmt.Sprintf("Chat ID %d", chatID))
		}
	}

	if create {
		if patch.Enabled == nil {
			enabled := true
			patch.Enabled = &enabled
			summaryParts = append(summaryParts, "状态 启用")
		}
		if patch.ScheduleType == nil {
			scheduleType := TelegramAgentScheduleTypeInterval
			patch.ScheduleType = &scheduleType
			summaryParts = append(summaryParts, "类型 间隔")
		}
		if *patch.ScheduleType == TelegramAgentScheduleTypeInterval && patch.IntervalMinutes == nil {
			interval := 60
			patch.IntervalMinutes = &interval
			summaryParts = append(summaryParts, "间隔 60 分钟")
		}
		if *patch.ScheduleType == TelegramAgentScheduleTypeDaily && patch.TimeOfDay == nil {
			return telegramScheduledTaskPatch{}, nil, errors.New("每天执行任务必须提供 time_of_day，格式 HH:mm")
		}
		if patch.Timezone == nil {
			timezone := "Local"
			patch.Timezone = &timezone
		}
		if patch.PushToConversation == nil {
			push := false
			patch.PushToConversation = &push
		}
	}

	return patch, orderedUniqueStrings(summaryParts), nil
}

func createTelegramAgentScheduledTask(ctx context.Context, patch telegramScheduledTaskPatch) (string, error) {
	task := models.TelegramAgentScheduledTask{
		Enabled:            1,
		ScheduleType:       TelegramAgentScheduleTypeInterval,
		IntervalMinutes:    60,
		Timezone:           "Local",
		PushToConversation: 0,
	}
	applyTelegramScheduledTaskPatch(&task, patch)
	if err := NormalizeTelegramAgentScheduledTaskForSave(&task, time.Now(), true); err != nil {
		return "", err
	}
	if err := models.DB.WithContext(ctx).Create(&task).Error; err != nil {
		return "", err
	}
	return telegramScheduledTaskSavedText("已创建 Agent 定时任务", task), nil
}

func updateTelegramAgentScheduledTask(ctx context.Context, taskID uint, patch telegramScheduledTaskPatch) (string, error) {
	task, err := getTelegramAgentScheduledTaskByID(ctx, taskID)
	if err != nil {
		return "", err
	}
	applyTelegramScheduledTaskPatch(&task, patch)
	if err := NormalizeTelegramAgentScheduledTaskForSave(&task, time.Now(), true); err != nil {
		return "", err
	}
	task.Running = 0
	if err := models.DB.WithContext(ctx).Save(&task).Error; err != nil {
		return "", err
	}
	return telegramScheduledTaskSavedText("已更新 Agent 定时任务", task), nil
}

func setTelegramAgentScheduledTaskStatus(ctx context.Context, taskID uint, enabled bool) (string, error) {
	task, err := getTelegramAgentScheduledTaskByID(ctx, taskID)
	if err != nil {
		return "", err
	}
	task.Enabled = boolToInt(enabled)
	task.Running = 0
	if enabled {
		if err := NormalizeTelegramAgentScheduledTaskForSave(&task, time.Now(), true); err != nil {
			return "", err
		}
	}
	if err := models.DB.WithContext(ctx).Save(&task).Error; err != nil {
		return "", err
	}
	return telegramScheduledTaskSavedText(fmt.Sprintf("已%s Agent 定时任务", telegramStatusVerb(enabled)), task), nil
}

func applyTelegramScheduledTaskPatch(task *models.TelegramAgentScheduledTask, patch telegramScheduledTaskPatch) {
	if patch.Name != nil {
		task.Name = *patch.Name
	}
	if patch.Prompt != nil {
		task.Prompt = *patch.Prompt
	}
	if patch.Enabled != nil {
		task.Enabled = boolToInt(*patch.Enabled)
	}
	if patch.ScheduleType != nil {
		task.ScheduleType = *patch.ScheduleType
	}
	if patch.IntervalMinutes != nil {
		task.IntervalMinutes = *patch.IntervalMinutes
	}
	if patch.TimeOfDay != nil {
		task.TimeOfDay = *patch.TimeOfDay
	}
	if patch.Timezone != nil {
		task.Timezone = *patch.Timezone
	}
	if patch.PushToConversation != nil {
		task.PushToConversation = boolToInt(*patch.PushToConversation)
	}
	if patch.ClearChatID {
		task.ChatID = 0
	} else if patch.ChatID != nil {
		task.ChatID = *patch.ChatID
	}
}

func telegramScheduledTaskSavedText(title string, task models.TelegramAgentScheduledTask) string {
	lines := []string{
		title,
		"名称：" + task.Name,
		"状态：" + telegramScheduledTaskStatusLabel(task),
		"计划：" + telegramScheduledTaskScheduleLabel(task),
		"推送到对话：" + telegramScheduledTaskBoolLabel(task.PushToConversation == 1, "开启", "关闭"),
	}
	if task.NextRunAt != nil && !task.NextRunAt.IsZero() {
		lines = append(lines, "下次运行："+formatTelegramAgentLogTime(*task.NextRunAt))
	}
	if task.ChatID != 0 {
		lines = append(lines, fmt.Sprintf("Chat ID：%d", task.ChatID))
	}
	return strings.Join(lines, "\n")
}

func findTelegramAgentScheduledTask(ctx context.Context, target string) (models.TelegramAgentScheduledTask, error) {
	target = cleanupTelegramToolTarget(target)
	if target == "" {
		return models.TelegramAgentScheduledTask{}, errors.New("请写明 Agent 定时任务名称")
	}
	if id, ok := parseTelegramToolID(target); ok {
		return getTelegramAgentScheduledTaskByID(ctx, id)
	}

	var exact []models.TelegramAgentScheduledTask
	if err := models.DB.WithContext(ctx).
		Where("LOWER(name) = ?", strings.ToLower(target)).
		Order("id ASC").
		Find(&exact).Error; err != nil {
		return models.TelegramAgentScheduledTask{}, err
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return models.TelegramAgentScheduledTask{}, ambiguousTelegramToolTargetError("Agent 定时任务", exactTelegramScheduledTaskNames(exact))
	}

	var fuzzy []models.TelegramAgentScheduledTask
	if err := models.DB.WithContext(ctx).
		Where("name LIKE ?", "%"+target+"%").
		Order("LOWER(name) ASC").
		Order("id ASC").
		Limit(6).
		Find(&fuzzy).Error; err != nil {
		return models.TelegramAgentScheduledTask{}, err
	}
	if len(fuzzy) == 0 {
		return models.TelegramAgentScheduledTask{}, fmt.Errorf("未找到 Agent 定时任务：%s", target)
	}
	if len(fuzzy) > 1 {
		return models.TelegramAgentScheduledTask{}, ambiguousTelegramToolTargetError("Agent 定时任务", exactTelegramScheduledTaskNames(fuzzy))
	}
	return fuzzy[0], nil
}

func getTelegramAgentScheduledTaskByID(ctx context.Context, id uint) (models.TelegramAgentScheduledTask, error) {
	var task models.TelegramAgentScheduledTask
	if err := models.DB.WithContext(ctx).Where("id = ?", id).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.TelegramAgentScheduledTask{}, errors.New("未找到对应 Agent 定时任务")
		}
		return models.TelegramAgentScheduledTask{}, err
	}
	return task, nil
}

func exactTelegramScheduledTaskNames(rows []models.TelegramAgentScheduledTask) []string {
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.Name)
	}
	return result
}

func telegramScheduledTaskStatusLabel(task models.TelegramAgentScheduledTask) string {
	if task.Running == 1 {
		return "执行中"
	}
	if task.Enabled == 1 {
		return "启用"
	}
	return "禁用"
}

func telegramScheduledTaskLastStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return "成功"
	case "error", "failed":
		return "失败"
	case "running":
		return "执行中"
	default:
		if strings.TrimSpace(status) == "" {
			return "无"
		}
		return strings.TrimSpace(status)
	}
}

func telegramScheduledTaskScheduleLabel(task models.TelegramAgentScheduledTask) string {
	switch normalizeTelegramAgentScheduledTaskType(task.ScheduleType) {
	case TelegramAgentScheduleTypeDaily:
		timeOfDay := strings.TrimSpace(task.TimeOfDay)
		if timeOfDay == "" {
			timeOfDay = "未设置"
		}
		timezone := strings.TrimSpace(task.Timezone)
		if timezone == "" {
			timezone = "Local"
		}
		return "每天 " + timeOfDay + "（" + timezone + "）"
	default:
		return fmt.Sprintf("每 %d 分钟", task.IntervalMinutes)
	}
}

func telegramScheduledTaskScheduleTypeLabel(scheduleType string) string {
	switch normalizeTelegramAgentScheduledTaskType(scheduleType) {
	case TelegramAgentScheduleTypeDaily:
		return "每天"
	default:
		return "间隔"
	}
}

func telegramScheduledTaskBoolLabel(value bool, trueText string, falseText string) string {
	if value {
		return trueText
	}
	return falseText
}
