package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	telegramAgentToolConfirmTTL = 5 * time.Minute
	telegramAgentToolListLimit  = 20
)

type telegramToolActionKind string

const (
	telegramToolActionSetModelStatus         telegramToolActionKind = "set_model_status"
	telegramToolActionSetProviderStatus      telegramToolActionKind = "set_provider_status"
	telegramToolActionUpdateProviderConfig   telegramToolActionKind = "update_provider_config"
	telegramToolActionCreateAuthKey          telegramToolActionKind = "create_auth_key"
	telegramToolActionUpdateAuthKey          telegramToolActionKind = "update_auth_key"
	telegramToolActionCreateScheduledTask    telegramToolActionKind = "create_telegram_agent_scheduled_task"
	telegramToolActionUpdateScheduledTask    telegramToolActionKind = "update_telegram_agent_scheduled_task"
	telegramToolActionSetScheduledTaskStatus telegramToolActionKind = "set_telegram_agent_scheduled_task_status"
	telegramToolActionRunTerminalCommand     telegramToolActionKind = "run_terminal_command"
	telegramToolActionBatch                  telegramToolActionKind = "batch"
)

type telegramToolAction struct {
	ChatID             int64
	ConversationID     string
	Kind               telegramToolActionKind
	TargetID           uint
	TargetIDs          []uint
	TargetName         string
	Enabled            bool
	ProviderPatch      telegramProviderConfigPatch
	AuthKeyPatch       telegramAuthKeyPatch
	ScheduledTaskPatch telegramScheduledTaskPatch
	CommandRun         telegramCommandRun
	Actions            []telegramToolAction
	Summary            string
	CreatedAt          time.Time
}

type telegramProviderSummary struct {
	Provider     models.Provider
	TotalCount   int
	EnabledCount int
}

type telegramProviderConfigPatch struct {
	Name                       *string
	Config                     *string
	ConfigReplaced             bool
	ConfigChangedKeys          []string
	ConfigRemovedKeys          []string
	Console                    *string
	ProxyURL                   *string
	ModelsFetchMode            *string
	Capabilities               *models.ProviderCapabilities
	InterfaceConversionEnabled *bool
	InterfaceConversionTarget  *string
}

type telegramScheduledTaskPatch struct {
	Name               *string
	Prompt             *string
	Enabled            *bool
	ScheduleType       *string
	IntervalMinutes    *int
	TimeOfDay          *string
	Timezone           *string
	PushToConversation *bool
	ChatID             *int64
	ClearChatID        bool
}

var (
	telegramPendingToolActions       sync.Map
	errTelegramProviderSnapshotEmpty = errors.New("telegram provider status snapshot not found")
)

func handleTelegramAgentToolMessage(ctx context.Context, client TelegramClient, chatID int64, raw string, cfg models.TelegramAgentConfig) (bool, error) {
	hasPendingAction := hasPendingTelegramToolAction(chatID)
	if isTelegramToolConfirm(raw) && (hasPendingAction || isStrictTelegramToolConfirm(raw)) {
		return true, confirmTelegramToolAction(ctx, client, chatID, cfg)
	}
	if isTelegramToolCancel(raw) && (hasPendingAction || isStrictTelegramToolCancel(raw)) {
		return true, cancelTelegramToolAction(ctx, client, chatID)
	}
	return false, nil
}

func confirmTelegramToolAction(ctx context.Context, client TelegramClient, chatID int64, cfg models.TelegramAgentConfig) error {
	action, ok, err := loadTelegramPendingToolAction(ctx, chatID)
	if err != nil {
		return sendTelegramToolText(ctx, client, chatID, "读取待确认操作失败："+err.Error())
	}
	if !ok {
		return sendTelegramToolText(ctx, client, chatID, "当前没有待确认的项目操作。")
	}

	if action.ChatID != chatID {
		deleteTelegramPendingToolAction(ctx, chatID)
		return sendTelegramToolText(ctx, client, chatID, "待确认操作已失效，请重新发起。")
	}
	if time.Since(action.CreatedAt) > telegramAgentToolConfirmTTL {
		deleteTelegramPendingToolAction(ctx, chatID)
		return sendTelegramToolText(ctx, client, chatID, "待确认操作已超过 5 分钟，请重新发起。")
	}

	deleteTelegramPendingToolAction(ctx, chatID)
	logID := recordTelegramAgentConfirmationExecutingLog(ctx, action)
	result, err := executeTelegramToolAction(ctx, action)
	if err != nil {
		finishTelegramAgentConfirmationLog(ctx, logID, action, telegramAgentToolLogStatusFailed, "", err)
		return sendTelegramToolText(ctx, client, chatID, "执行失败："+err.Error())
	}
	finishTelegramAgentConfirmationLog(ctx, logID, action, telegramAgentToolLogStatusExecuted, result, nil)
	if shouldSummarizeTelegramToolActionResult(action) {
		if err := runTelegramAgentToolResultFollowup(ctx, client, chatID, cfg, action, result); err == nil {
			return nil
		} else {
			slog.Warn("TG Agent 工具结果整理失败，回退原始结果", "chat_id", chatID, "action", action.Kind, "error", err)
		}
	}
	return sendTelegramToolText(ctx, client, chatID, result)
}

func cancelTelegramToolAction(ctx context.Context, client TelegramClient, chatID int64) error {
	action, ok, err := loadTelegramPendingToolAction(ctx, chatID)
	if err != nil {
		return sendTelegramToolText(ctx, client, chatID, "读取待取消操作失败："+err.Error())
	}
	if ok {
		deleteTelegramPendingToolAction(ctx, chatID)
		recordTelegramAgentConfirmationLog(ctx, action, telegramAgentToolLogStatusCancelled, "已取消待确认操作。", nil)
		return sendTelegramToolText(ctx, client, chatID, "已取消待确认操作。")
	}
	return sendTelegramToolText(ctx, client, chatID, "当前没有待取消的项目操作。")
}

func storeTelegramPendingToolAction(ctx context.Context, action telegramToolAction) telegramToolAction {
	existing, ok, err := loadTelegramPendingToolAction(ctx, action.ChatID)
	if err != nil {
		telegramPendingToolActions.Store(action.ChatID, action)
		return action
	}

	if !ok {
		if err := saveTelegramPendingToolAction(ctx, action); err != nil {
			telegramPendingToolActions.Store(action.ChatID, action)
		}
		return action
	}

	merged := mergeTelegramPendingToolActions(ctx, existing, action)
	if err := saveTelegramPendingToolAction(ctx, merged); err != nil {
		telegramPendingToolActions.Store(action.ChatID, merged)
	}
	return merged
}

func mergeTelegramPendingToolActions(ctx context.Context, existing telegramToolAction, incoming telegramToolAction) telegramToolAction {
	items := flattenTelegramToolActions(existing)
	for _, item := range flattenTelegramToolActions(incoming) {
		merged := false
		for index := range items {
			if canMergeTelegramToolActions(items[index], item) {
				items[index] = mergeTelegramModelStatusActions(ctx, items[index], item)
				merged = true
				break
			}
		}
		if !merged {
			items = append(items, item)
		}
	}

	createdAt := existing.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return buildTelegramToolActionBatch(existing.ChatID, createdAt, items)
}

func flattenTelegramToolActions(action telegramToolAction) []telegramToolAction {
	if action.Kind != telegramToolActionBatch {
		return []telegramToolAction{action}
	}
	items := make([]telegramToolAction, 0, len(action.Actions))
	for _, item := range action.Actions {
		items = append(items, flattenTelegramToolActions(item)...)
	}
	return items
}

func buildTelegramToolActionBatch(chatID int64, createdAt time.Time, items []telegramToolAction) telegramToolAction {
	normalized := make([]telegramToolAction, 0, len(items))
	conversationID := ""
	for _, item := range items {
		if item.Kind == telegramToolActionBatch {
			normalized = append(normalized, flattenTelegramToolActions(item)...)
			continue
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = createdAt
		}
		if conversationID == "" {
			conversationID = item.ConversationID
		}
		normalized = append(normalized, item)
	}
	if len(normalized) == 0 {
		return telegramToolAction{ChatID: chatID, ConversationID: conversationID, Kind: telegramToolActionBatch, CreatedAt: createdAt, Summary: "批次操作（0 项）"}
	}
	if len(normalized) == 1 {
		return normalized[0]
	}

	summaryLines := make([]string, 0, len(normalized)+1)
	summaryLines = append(summaryLines, fmt.Sprintf("批次操作（%d 项）", len(normalized)))
	for _, item := range normalized {
		summaryLines = append(summaryLines, "- "+strings.ReplaceAll(strings.TrimSpace(item.Summary), "\n", "\n  "))
	}
	return telegramToolAction{
		ChatID:         chatID,
		ConversationID: conversationID,
		Kind:           telegramToolActionBatch,
		Actions:        normalized,
		Summary:        strings.Join(summaryLines, "\n"),
		CreatedAt:      createdAt,
	}
}

func canMergeTelegramToolActions(left telegramToolAction, right telegramToolAction) bool {
	return left.ChatID == right.ChatID &&
		left.Kind == telegramToolActionSetModelStatus &&
		right.Kind == telegramToolActionSetModelStatus &&
		left.Enabled == right.Enabled
}

func mergeTelegramModelStatusActions(ctx context.Context, left telegramToolAction, right telegramToolAction) telegramToolAction {
	ids := orderedUniqueTelegramModelIDs(append(telegramToolActionModelIDs(left), telegramToolActionModelIDs(right)...))
	merged := telegramToolAction{
		ChatID:         left.ChatID,
		ConversationID: left.ConversationID,
		Kind:           telegramToolActionSetModelStatus,
		TargetIDs:      ids,
		Enabled:        left.Enabled,
		CreatedAt:      left.CreatedAt,
	}
	if merged.CreatedAt.IsZero() {
		merged.CreatedAt = time.Now()
	}

	rows, err := loadTelegramAgentModelsByIDs(ctx, ids)
	if err != nil || len(rows) == 0 {
		merged.Summary = fmt.Sprintf("%s模型（共 %d 个）", telegramStatusVerb(merged.Enabled), len(ids))
		return merged
	}
	merged.Summary = fmt.Sprintf("%s模型（共 %d 个）：%s", telegramStatusVerb(merged.Enabled), len(rows), summarizeTelegramToolModelNames(rows))
	return merged
}

func telegramToolActionModelIDs(action telegramToolAction) []uint {
	ids := make([]uint, 0, len(action.TargetIDs)+1)
	if action.TargetID > 0 {
		ids = append(ids, action.TargetID)
	}
	ids = append(ids, action.TargetIDs...)
	return ids
}

func orderedUniqueTelegramModelIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func telegramToolConfirmationText(action telegramToolAction) string {
	return strings.Join([]string{
		"待确认操作",
		"操作：" + action.Summary,
		"",
		"回复“确认”执行，或回复“取消”放弃。",
		"有效期：5 分钟。",
	}, "\n")
}

func prepareOrExecuteTelegramToolAction(ctx context.Context, action telegramToolAction, requireConfirmation bool) (string, error) {
	action = attachTelegramToolActionConversationID(ctx, action)
	if requireConfirmation {
		action = storeTelegramPendingToolAction(ctx, action)
		text := telegramToolConfirmationText(action)
		recordTelegramAgentPreparedActionLog(ctx, action, text, true)
		return text, nil
	}
	logID := recordTelegramAgentToolActionExecutingLog(ctx, action, telegramAgentToolLogSourceToolAction, false)
	result, err := executeTelegramToolAction(ctx, action)
	if err != nil {
		finishTelegramAgentToolActionFailureLog(ctx, logID, action, err, false)
		return "", err
	}
	finishTelegramAgentPreparedActionLog(ctx, logID, action, result, false)
	return result, nil
}

func attachTelegramToolActionConversationID(ctx context.Context, action telegramToolAction) telegramToolAction {
	if strings.TrimSpace(action.ConversationID) == "" {
		conversationID, err := resolveTelegramActiveConversationID(ctx, action.ChatID, getTelegramSession(action.ChatID))
		if err == nil {
			action.ConversationID = conversationID
		}
	}
	for index := range action.Actions {
		if strings.TrimSpace(action.Actions[index].ConversationID) == "" {
			action.Actions[index].ConversationID = action.ConversationID
		}
	}
	return action
}

func executeTelegramToolAction(ctx context.Context, action telegramToolAction) (string, error) {
	switch action.Kind {
	case telegramToolActionBatch:
		return executeTelegramToolActionBatch(ctx, action)
	case telegramToolActionSetModelStatus:
		if len(action.TargetIDs) > 0 {
			return setTelegramAgentModelsStatus(ctx, action.TargetIDs, action.Enabled)
		}
		return setTelegramAgentModelStatus(ctx, action.TargetID, action.Enabled)
	case telegramToolActionSetProviderStatus:
		return setTelegramAgentProviderStatus(ctx, action.TargetID, action.Enabled)
	case telegramToolActionUpdateProviderConfig:
		return updateTelegramAgentProviderConfig(ctx, action.TargetID, action.ProviderPatch)
	case telegramToolActionCreateAuthKey:
		return createTelegramAgentAuthKey(ctx, action.AuthKeyPatch)
	case telegramToolActionUpdateAuthKey:
		return updateTelegramAgentAuthKey(ctx, action.TargetID, action.AuthKeyPatch)
	case telegramToolActionCreateScheduledTask:
		return createTelegramAgentScheduledTask(ctx, action.ScheduledTaskPatch)
	case telegramToolActionUpdateScheduledTask:
		return updateTelegramAgentScheduledTask(ctx, action.TargetID, action.ScheduledTaskPatch)
	case telegramToolActionSetScheduledTaskStatus:
		return setTelegramAgentScheduledTaskStatus(ctx, action.TargetID, action.Enabled)
	case telegramToolActionRunTerminalCommand:
		return executeTelegramCommandAction(ctx, action.CommandRun)
	default:
		return "", errors.New("未知的工具操作")
	}
}

func shouldSummarizeTelegramToolActionResult(action telegramToolAction) bool {
	switch action.Kind {
	case telegramToolActionRunTerminalCommand:
		return true
	case telegramToolActionBatch:
		items := flattenTelegramToolActions(action)
		for _, item := range items {
			if item.Kind != telegramToolActionRunTerminalCommand {
				return false
			}
		}
		return len(items) > 0
	default:
		return false
	}
}

func executeTelegramToolActionBatch(ctx context.Context, action telegramToolAction) (string, error) {
	items := flattenTelegramToolActions(action)
	if len(items) == 0 {
		return "", errors.New("批次操作为空")
	}

	results := make([]string, 0, len(items))
	for _, item := range items {
		result, err := executeTelegramToolAction(ctx, item)
		if err != nil {
			return "", fmt.Errorf("执行「%s」失败: %w", strings.TrimSpace(item.Summary), err)
		}
		results = append(results, strings.TrimSpace(result))
	}
	if len(results) == 1 {
		return results[0], nil
	}

	lines := []string{"已执行批次操作"}
	for index, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, strings.ReplaceAll(result, "\n", "\n   ")))
	}
	return strings.Join(lines, "\n"), nil
}

func buildTelegramSetModelStatusAction(ctx context.Context, chatID int64, target string, enabled bool) (telegramToolAction, error) {
	return buildTelegramSetModelStatusActionWithMode(ctx, chatID, target, enabled, false)
}

func buildTelegramSetModelsStatusBatchAction(ctx context.Context, chatID int64, items []telegramAgentModelStatusBatchItem) (telegramToolAction, error) {
	if len(items) == 0 {
		return telegramToolAction{}, errors.New("批量模型操作至少需要一个动作")
	}

	actions := make([]telegramToolAction, 0, len(items))
	for index, item := range items {
		if item.Enabled == nil {
			return telegramToolAction{}, fmt.Errorf("第 %d 个动作缺少 enabled 参数", index+1)
		}
		target := cleanupTelegramToolTarget(item.Target)
		action, err := buildTelegramSetModelStatusActionWithMode(ctx, chatID, target, *item.Enabled, item.Bulk)
		if err != nil {
			return telegramToolAction{}, fmt.Errorf("第 %d 个动作准备失败: %w", index+1, err)
		}
		actions = append(actions, action)
	}
	return buildTelegramToolActionBatch(chatID, time.Now(), actions), nil
}

func buildTelegramSetModelStatusActionWithMode(ctx context.Context, chatID int64, target string, enabled bool, bulk bool) (telegramToolAction, error) {
	if bulk {
		rows, err := findTelegramAgentModelsByKeyword(ctx, target)
		if err != nil {
			return telegramToolAction{}, err
		}
		ids := make([]uint, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return telegramToolAction{
			ChatID:     chatID,
			Kind:       telegramToolActionSetModelStatus,
			TargetIDs:  ids,
			TargetName: target,
			Enabled:    enabled,
			Summary:    fmt.Sprintf("%s模型（匹配“%s”，共 %d 个）：%s", telegramStatusVerb(enabled), target, len(rows), summarizeTelegramToolModelNames(rows)),
			CreatedAt:  time.Now(),
		}, nil
	}

	model, err := findTelegramAgentModel(ctx, target)
	if err != nil {
		return telegramToolAction{}, err
	}
	return telegramToolAction{
		ChatID:     chatID,
		Kind:       telegramToolActionSetModelStatus,
		TargetID:   model.ID,
		TargetName: model.Name,
		Enabled:    enabled,
		Summary:    fmt.Sprintf("%s模型：%s", telegramStatusVerb(enabled), model.Name),
		CreatedAt:  time.Now(),
	}, nil
}

func buildTelegramSetProviderStatusAction(ctx context.Context, chatID int64, target string, enabled bool) (telegramToolAction, error) {
	provider, err := findTelegramAgentProvider(ctx, target)
	if err != nil {
		return telegramToolAction{}, err
	}
	return telegramToolAction{
		ChatID:     chatID,
		Kind:       telegramToolActionSetProviderStatus,
		TargetID:   provider.ID,
		TargetName: provider.Name,
		Enabled:    enabled,
		Summary:    fmt.Sprintf("%s提供商：%s", telegramStatusVerb(enabled), provider.Name),
		CreatedAt:  time.Now(),
	}, nil
}

func buildTelegramUpdateProviderConfigAction(ctx context.Context, chatID int64, args telegramAgentToolCallArgs) (telegramToolAction, error) {
	target := cleanupTelegramToolTarget(args.Target)
	provider, err := findTelegramAgentProvider(ctx, target)
	if err != nil {
		return telegramToolAction{}, err
	}

	patch, summaryParts, err := buildTelegramProviderConfigPatch(ctx, provider, args)
	if err != nil {
		return telegramToolAction{}, err
	}
	if len(summaryParts) == 0 {
		return telegramToolAction{}, errors.New("请写明要修改的提供商配置字段")
	}

	return telegramToolAction{
		ChatID:        chatID,
		Kind:          telegramToolActionUpdateProviderConfig,
		TargetID:      provider.ID,
		TargetName:    provider.Name,
		ProviderPatch: patch,
		Summary:       fmt.Sprintf("更新提供商配置：%s\n变更：%s", provider.Name, strings.Join(summaryParts, "；")),
		CreatedAt:     time.Now(),
	}, nil
}

func setTelegramAgentModelStatus(ctx context.Context, modelID uint, enabled bool) (string, error) {
	status := 0
	if enabled {
		status = 1
	}
	result := models.DB.WithContext(ctx).
		Model(&models.Model{}).
		Where("id = ?", modelID).
		Update("status", status)
	if result.Error != nil {
		return "", result.Error
	}

	model, err := getTelegramAgentModelByID(ctx, modelID)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		fmt.Sprintf("已%s模型", telegramStatusVerb(enabled)),
		"目标：" + model.Name,
	}, "\n"), nil
}

func setTelegramAgentModelsStatus(ctx context.Context, modelIDs []uint, enabled bool) (string, error) {
	if len(modelIDs) == 0 {
		return "", errors.New("没有可操作的模型")
	}
	status := 0
	if enabled {
		status = 1
	}
	if err := models.DB.WithContext(ctx).
		Model(&models.Model{}).
		Where("id IN ?", modelIDs).
		Update("status", status).Error; err != nil {
		return "", err
	}

	var rows []models.Model
	rows, err := loadTelegramAgentModelsByIDs(ctx, modelIDs)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		fmt.Sprintf("已%s模型", telegramStatusVerb(enabled)),
		fmt.Sprintf("数量：%d 个", len(rows)),
		"目标：" + summarizeTelegramToolModelNames(rows),
	}, "\n"), nil
}

func setTelegramAgentProviderStatus(ctx context.Context, providerID uint, enabled bool) (string, error) {
	provider, err := getTelegramAgentProviderByID(ctx, providerID)
	if err != nil {
		return "", err
	}

	err = models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if enabled {
			return restoreTelegramProviderEnabledAssociations(ctx, tx, providerID)
		}
		return snapshotAndDisableTelegramProviderAssociations(ctx, tx, providerID)
	})
	if err != nil {
		if errors.Is(err, errTelegramProviderSnapshotEmpty) {
			summary, summaryErr := loadTelegramProviderSummary(ctx, providerID)
			if summaryErr != nil {
				return "", err
			}
			if summary.EnabledCount > 0 {
				return strings.Join([]string{
					"提供商无需恢复",
					"目标：" + provider.Name,
					fmt.Sprintf("当前启用关联：%d", summary.EnabledCount),
				}, "\n"), nil
			}
			return "", errors.New("没有可恢复的启用关联快照，请到提供商与模型中手动启用需要的关联")
		}
		return "", err
	}

	summary, err := loadTelegramProviderSummary(ctx, providerID)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		fmt.Sprintf("已%s提供商", telegramStatusVerb(enabled)),
		"目标：" + provider.Name,
		fmt.Sprintf("启用关联：%d/%d", summary.EnabledCount, summary.TotalCount),
	}, "\n"), nil
}

func updateTelegramAgentProviderConfig(ctx context.Context, providerID uint, patch telegramProviderConfigPatch) (string, error) {
	provider, err := getTelegramAgentProviderByID(ctx, providerID)
	if err != nil {
		return "", err
	}
	if err := validateTelegramProviderConfigPatch(ctx, provider, patch); err != nil {
		return "", err
	}

	updates := make(map[string]any)
	changedParts := make([]string, 0)
	if patch.Name != nil {
		updates["name"] = *patch.Name
		changedParts = append(changedParts, "名称")
	}
	if patch.Config != nil {
		updates["config"] = *patch.Config
		changedParts = append(changedParts, summarizeTelegramProviderConfigChange(patch))
	}
	if patch.Console != nil {
		updates["console"] = *patch.Console
		changedParts = append(changedParts, "控制台地址")
	}
	if patch.ProxyURL != nil {
		updates["proxy_url"] = *patch.ProxyURL
		changedParts = append(changedParts, "代理地址")
	}
	if patch.ModelsFetchMode != nil {
		updates["models_fetch_mode"] = *patch.ModelsFetchMode
		changedParts = append(changedParts, "模型获取方式")
	}
	if patch.Capabilities != nil {
		updates["capabilities"] = *patch.Capabilities
		changedParts = append(changedParts, "接口能力")
	}
	if patch.InterfaceConversionEnabled != nil {
		enabled := 0
		if *patch.InterfaceConversionEnabled {
			enabled = 1
		}
		updates["interface_conversion_enabled"] = enabled
		changedParts = append(changedParts, "接口转换")
	}
	if patch.InterfaceConversionTarget != nil {
		updates["interface_conversion_target"] = *patch.InterfaceConversionTarget
		if patch.InterfaceConversionEnabled == nil {
			changedParts = append(changedParts, "接口转换目标")
		}
	}
	if len(updates) == 0 {
		return "", errors.New("没有可更新的提供商配置")
	}

	if err := models.DB.WithContext(ctx).
		Model(&models.Provider{}).
		Where("id = ?", providerID).
		Updates(updates).Error; err != nil {
		return "", err
	}

	updated, err := getTelegramAgentProviderByID(ctx, providerID)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"已更新提供商配置",
		"目标：" + updated.Name,
		"修改：" + strings.Join(orderedUniqueStrings(changedParts), "、"),
	}, "\n"), nil
}

func getTelegramAgentProviderConfigText(ctx context.Context, target string) (string, error) {
	target = cleanupTelegramToolTarget(target)
	provider, err := findTelegramAgentProvider(ctx, target)
	if err != nil {
		return "", err
	}
	configMap, err := parseTelegramProviderConfigMap(provider.Config)
	if err != nil {
		return "", fmt.Errorf("配置 JSON 无法解析: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("提供商配置")
	sb.WriteString("\n名称：" + provider.Name)
	sb.WriteString(fmt.Sprintf("\n模型获取方式：%s", telegramModelsFetchModeLabel(provider.ModelsFetchMode)))
	sb.WriteString(fmt.Sprintf("\n控制台地址：%s", telegramDisplayOptionalValue(provider.Console)))
	sb.WriteString(fmt.Sprintf("\n代理地址：%s", telegramDisplayOptionalValue(provider.ProxyURL)))
	sb.WriteString(fmt.Sprintf("\n接口能力：%s", strings.Join([]string(models.NormalizeProviderCapabilities(provider.Capabilities)), "、")))
	sb.WriteString(fmt.Sprintf("\n接口转换：%s", telegramInterfaceConversionLabel(provider.InterfaceConversionEnabled == 1, provider.InterfaceConversionTarget)))
	if len(configMap) == 0 {
		sb.WriteString("\n配置字段：无")
		return sb.String(), nil
	}

	keys := make([]string, 0, len(configMap))
	for key := range configMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sb.WriteString("\n配置字段：")
	for _, key := range keys {
		sb.WriteString(fmt.Sprintf("\n- %s：%s", key, maskTelegramProviderConfigValue(key, configMap[key])))
	}
	return sb.String(), nil
}

func buildTelegramProviderConfigPatch(ctx context.Context, provider models.Provider, args telegramAgentToolCallArgs) (telegramProviderConfigPatch, []string, error) {
	var patch telegramProviderConfigPatch
	summaryParts := make([]string, 0)

	if args.Name != nil {
		name := strings.TrimSpace(*args.Name)
		if name == "" {
			return telegramProviderConfigPatch{}, nil, errors.New("提供商名称不能为空")
		}
		patch.Name = &name
		summaryParts = append(summaryParts, "名称改为 "+name)
	}

	config, configReplaced, changedKeys, removedKeys, configTouched, err := buildTelegramProviderUpdatedConfig(provider.Config, args.Config, args.ConfigUpdates, args.RemoveConfigKeys)
	if err != nil {
		return telegramProviderConfigPatch{}, nil, err
	}
	if configTouched {
		patch.Config = &config
		patch.ConfigReplaced = configReplaced
		patch.ConfigChangedKeys = changedKeys
		patch.ConfigRemovedKeys = removedKeys
		summaryParts = append(summaryParts, summarizeTelegramProviderConfigChange(patch))
	}

	if args.Console != nil {
		console := strings.TrimSpace(*args.Console)
		patch.Console = &console
		summaryParts = append(summaryParts, "控制台地址改为 "+telegramDisplayOptionalValue(console))
	}

	if args.ProxyURL != nil {
		proxyURL, err := sanitizeTelegramProviderProxyURL(*args.ProxyURL)
		if err != nil {
			return telegramProviderConfigPatch{}, nil, fmt.Errorf("代理地址无效: %w", err)
		}
		patch.ProxyURL = &proxyURL
		summaryParts = append(summaryParts, "代理地址改为 "+telegramDisplayOptionalValue(proxyURL))
	}

	if args.ModelsFetchMode != nil {
		mode, err := normalizeTelegramModelsFetchMode(*args.ModelsFetchMode)
		if err != nil {
			return telegramProviderConfigPatch{}, nil, err
		}
		patch.ModelsFetchMode = &mode
		summaryParts = append(summaryParts, "模型获取方式改为 "+telegramModelsFetchModeLabel(mode))
	}

	if args.Capabilities != nil {
		capabilities := models.NormalizeProviderCapabilities(*args.Capabilities)
		patch.Capabilities = &capabilities
		summaryParts = append(summaryParts, "接口能力改为 "+strings.Join([]string(capabilities), "、"))
	}

	conversionTouched := args.InterfaceConversionEnabled != nil || args.InterfaceConversionTarget != nil
	if conversionTouched || args.Capabilities != nil {
		conversionEnabled := provider.InterfaceConversionEnabled == 1
		if args.InterfaceConversionEnabled != nil {
			conversionEnabled = *args.InterfaceConversionEnabled
		}

		conversionTarget := strings.TrimSpace(provider.InterfaceConversionTarget)
		if args.InterfaceConversionTarget != nil {
			conversionTarget, err = normalizeTelegramInterfaceConversionTarget(*args.InterfaceConversionTarget)
			if err != nil {
				return telegramProviderConfigPatch{}, nil, err
			}
		}
		if !conversionEnabled {
			conversionTarget = ""
		}

		if conversionTouched {
			patch.InterfaceConversionEnabled = &conversionEnabled
			patch.InterfaceConversionTarget = &conversionTarget
			summaryParts = append(summaryParts, "接口转换改为 "+telegramInterfaceConversionLabel(conversionEnabled, conversionTarget))
		}
	}

	if err := validateTelegramProviderConfigPatch(ctx, provider, patch); err != nil {
		return telegramProviderConfigPatch{}, nil, err
	}
	return patch, summaryParts, nil
}

func buildTelegramProviderUpdatedConfig(current string, fullConfig *string, configUpdates map[string]any, removeKeys []string) (string, bool, []string, []string, bool, error) {
	configTouched := fullConfig != nil || len(configUpdates) > 0 || len(removeKeys) > 0
	if !configTouched {
		return "", false, nil, nil, false, nil
	}

	configReplaced := fullConfig != nil
	var configMap map[string]string
	var err error
	if fullConfig != nil {
		if strings.TrimSpace(*fullConfig) == "" {
			return "", false, nil, nil, false, errors.New("完整 config 不能为空")
		}
		configMap, err = parseTelegramProviderConfigMap(*fullConfig)
	} else {
		configMap, err = parseTelegramProviderConfigMap(current)
	}
	if err != nil {
		return "", false, nil, nil, false, fmt.Errorf("配置 JSON 无法解析: %w", err)
	}

	changedKeys := make([]string, 0, len(configUpdates))
	for rawKey, rawValue := range configUpdates {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return "", false, nil, nil, false, errors.New("config_updates 包含空字段名")
		}
		configMap[key] = telegramProviderConfigValueToString(rawValue)
		changedKeys = append(changedKeys, key)
	}

	removedKeys := make([]string, 0, len(removeKeys))
	for _, rawKey := range removeKeys {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			continue
		}
		delete(configMap, key)
		removedKeys = append(removedKeys, key)
	}
	if len(configMap) == 0 {
		return "", false, nil, nil, false, errors.New("provider config 不能为空")
	}

	normalized, err := marshalTelegramProviderConfigMap(configMap)
	if err != nil {
		return "", false, nil, nil, false, err
	}
	if configReplaced {
		changedKeys = make([]string, 0, len(configMap))
		for key := range configMap {
			changedKeys = append(changedKeys, key)
		}
	}
	return normalized, configReplaced, orderedUniqueStrings(changedKeys), orderedUniqueStrings(removedKeys), true, nil
}

func validateTelegramProviderConfigPatch(ctx context.Context, provider models.Provider, patch telegramProviderConfigPatch) error {
	if patch.Name != nil && !strings.EqualFold(strings.TrimSpace(provider.Name), strings.TrimSpace(*patch.Name)) {
		var count int64
		if err := models.DB.WithContext(ctx).
			Model(&models.Provider{}).
			Where("name = ? AND id <> ?", *patch.Name, provider.ID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("提供商名称已存在：%s", *patch.Name)
		}
	}

	capabilities := provider.Capabilities
	if patch.Capabilities != nil {
		capabilities = *patch.Capabilities
	}

	conversionEnabled := provider.InterfaceConversionEnabled == 1
	if patch.InterfaceConversionEnabled != nil {
		conversionEnabled = *patch.InterfaceConversionEnabled
	}
	conversionTarget := strings.TrimSpace(provider.InterfaceConversionTarget)
	if patch.InterfaceConversionTarget != nil {
		conversionTarget = strings.TrimSpace(*patch.InterfaceConversionTarget)
	}
	if !conversionEnabled {
		return nil
	}
	if conversionTarget == "" {
		return errors.New("启用接口转换时必须设置转换目标")
	}
	if !models.ProviderSupportsEndpoint([]string(capabilities), conversionTarget) {
		return fmt.Errorf("接口转换目标 %s 不在提供商接口能力中", conversionTarget)
	}
	return nil
}

func listTelegramAgentModels(ctx context.Context, filter string) (string, error) {
	query := models.DB.WithContext(ctx).Model(&models.Model{})
	filter = strings.TrimSpace(filter)
	if filter != "" {
		query = query.Where("name LIKE ?", "%"+filter+"%")
	}

	var rows []models.Model
	if err := query.Order("status DESC").Order("LOWER(name) ASC").Order("id ASC").Limit(telegramAgentToolListLimit + 1).Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) == 0 {
		if filter == "" {
			return "当前没有模型。", nil
		}
		return "没有找到匹配的模型：" + filter, nil
	}

	hasMore := len(rows) > telegramAgentToolListLimit
	if hasMore {
		rows = rows[:telegramAgentToolListLimit]
	}

	var sb strings.Builder
	if filter == "" {
		sb.WriteString("模型列表")
	} else {
		sb.WriteString("模型列表\n筛选：" + filter)
	}
	for _, item := range rows {
		sb.WriteString(fmt.Sprintf("\n- %s｜%s", telegramStatusLabel(item.Status), item.Name))
	}
	if hasMore {
		sb.WriteString(fmt.Sprintf("\n\n仅显示前 %d 条，可加关键词继续筛选。", telegramAgentToolListLimit))
	}
	return sb.String(), nil
}

func listTelegramAgentProviders(ctx context.Context, filter string) (string, error) {
	query := models.DB.WithContext(ctx).Model(&models.Provider{})
	filter = strings.TrimSpace(filter)
	if filter != "" {
		query = query.Where("name LIKE ?", "%"+filter+"%")
	}

	var providers []models.Provider
	if err := query.Order("LOWER(name) ASC").Order("id ASC").Limit(telegramAgentToolListLimit + 1).Find(&providers).Error; err != nil {
		return "", err
	}
	if len(providers) == 0 {
		if filter == "" {
			return "当前没有提供商。", nil
		}
		return "没有找到匹配的提供商：" + filter, nil
	}

	hasMore := len(providers) > telegramAgentToolListLimit
	if hasMore {
		providers = providers[:telegramAgentToolListLimit]
	}
	summaries, err := loadTelegramProviderSummaries(ctx, providers)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	if filter == "" {
		sb.WriteString("提供商列表")
	} else {
		sb.WriteString("提供商列表\n筛选：" + filter)
	}
	for _, item := range summaries {
		sb.WriteString(fmt.Sprintf("\n- %s｜%s｜启用关联 %d/%d",
			telegramProviderStatusLabel(item),
			item.Provider.Name,
			item.EnabledCount,
			item.TotalCount,
		))
	}
	if hasMore {
		sb.WriteString(fmt.Sprintf("\n\n仅显示前 %d 条，可加关键词继续筛选。", telegramAgentToolListLimit))
	}
	return sb.String(), nil
}

func findTelegramAgentModel(ctx context.Context, target string) (models.Model, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return models.Model{}, errors.New("请写明模型名称")
	}
	if id, ok := parseTelegramToolID(target); ok {
		return getTelegramAgentModelByID(ctx, id)
	}

	var exact []models.Model
	if err := models.DB.WithContext(ctx).
		Where("LOWER(name) = ?", strings.ToLower(target)).
		Order("id ASC").
		Find(&exact).Error; err != nil {
		return models.Model{}, err
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return models.Model{}, ambiguousTelegramToolTargetError("模型", exactModelNames(exact))
	}

	var fuzzy []models.Model
	if err := models.DB.WithContext(ctx).
		Where("name LIKE ?", "%"+target+"%").
		Order("LOWER(name) ASC").
		Order("id ASC").
		Limit(6).
		Find(&fuzzy).Error; err != nil {
		return models.Model{}, err
	}
	if len(fuzzy) == 0 {
		return models.Model{}, fmt.Errorf("未找到模型：%s", target)
	}
	if len(fuzzy) > 1 {
		return models.Model{}, ambiguousTelegramToolTargetError("模型", exactModelNames(fuzzy))
	}
	return fuzzy[0], nil
}

func findTelegramAgentModelsByKeyword(ctx context.Context, target string) ([]models.Model, error) {
	target = cleanupTelegramBulkModelTarget(target)
	if target == "" {
		return nil, errors.New("批量操作需要写明模型关键词，例如：禁用 claude 的所有模型")
	}
	if _, ok := parseTelegramToolID(target); ok {
		return nil, errors.New("批量操作请使用模型名称关键词")
	}

	var rows []models.Model
	if err := models.DB.WithContext(ctx).
		Where("name LIKE ?", "%"+target+"%").
		Order("LOWER(name) ASC").
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("未找到名称包含“%s”的模型", target)
	}
	return rows, nil
}

func findTelegramAgentProvider(ctx context.Context, target string) (models.Provider, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return models.Provider{}, errors.New("请写明提供商名称")
	}
	if id, ok := parseTelegramToolID(target); ok {
		return getTelegramAgentProviderByID(ctx, id)
	}

	var exact []models.Provider
	if err := models.DB.WithContext(ctx).
		Where("LOWER(name) = ?", strings.ToLower(target)).
		Order("id ASC").
		Find(&exact).Error; err != nil {
		return models.Provider{}, err
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return models.Provider{}, ambiguousTelegramToolTargetError("提供商", exactProviderNames(exact))
	}

	var fuzzy []models.Provider
	if err := models.DB.WithContext(ctx).
		Where("name LIKE ?", "%"+target+"%").
		Order("LOWER(name) ASC").
		Order("id ASC").
		Limit(6).
		Find(&fuzzy).Error; err != nil {
		return models.Provider{}, err
	}
	if len(fuzzy) == 0 {
		return models.Provider{}, fmt.Errorf("未找到提供商：%s", target)
	}
	if len(fuzzy) > 1 {
		return models.Provider{}, ambiguousTelegramToolTargetError("提供商", exactProviderNames(fuzzy))
	}
	return fuzzy[0], nil
}

func getTelegramAgentModelByID(ctx context.Context, id uint) (models.Model, error) {
	var model models.Model
	if err := models.DB.WithContext(ctx).Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Model{}, errors.New("未找到对应模型")
		}
		return models.Model{}, err
	}
	return model, nil
}

func loadTelegramAgentModelsByIDs(ctx context.Context, ids []uint) ([]models.Model, error) {
	ids = orderedUniqueTelegramModelIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []models.Model
	if err := models.DB.WithContext(ctx).
		Where("id IN ?", ids).
		Order("LOWER(name) ASC").
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func getTelegramAgentProviderByID(ctx context.Context, id uint) (models.Provider, error) {
	var provider models.Provider
	if err := models.DB.WithContext(ctx).Where("id = ?", id).First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Provider{}, errors.New("未找到对应提供商")
		}
		return models.Provider{}, err
	}
	return provider, nil
}

func loadTelegramProviderSummary(ctx context.Context, providerID uint) (telegramProviderSummary, error) {
	provider, err := getTelegramAgentProviderByID(ctx, providerID)
	if err != nil {
		return telegramProviderSummary{}, err
	}
	items, err := loadTelegramProviderSummaries(ctx, []models.Provider{provider})
	if err != nil {
		return telegramProviderSummary{}, err
	}
	if len(items) == 0 {
		return telegramProviderSummary{Provider: provider}, nil
	}
	return items[0], nil
}

func loadTelegramProviderSummaries(ctx context.Context, providers []models.Provider) ([]telegramProviderSummary, error) {
	if len(providers) == 0 {
		return nil, nil
	}

	providerIDs := make([]uint, 0, len(providers))
	for _, provider := range providers {
		providerIDs = append(providerIDs, provider.ID)
	}

	type providerStatusAgg struct {
		ProviderID   uint `gorm:"column:provider_id"`
		TotalCount   int  `gorm:"column:total_count"`
		EnabledCount int  `gorm:"column:enabled_count"`
	}
	var rows []providerStatusAgg
	if err := models.DB.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Select("provider_id, COUNT(*) AS total_count, COALESCE(SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END),0) AS enabled_count").
		Where("provider_id IN ?", providerIDs).
		Group("provider_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	statsByProviderID := make(map[uint]providerStatusAgg, len(rows))
	for _, row := range rows {
		statsByProviderID[row.ProviderID] = row
	}

	result := make([]telegramProviderSummary, 0, len(providers))
	for _, provider := range providers {
		stats := statsByProviderID[provider.ID]
		result = append(result, telegramProviderSummary{
			Provider:     provider,
			TotalCount:   stats.TotalCount,
			EnabledCount: stats.EnabledCount,
		})
	}
	return result, nil
}

func snapshotAndDisableTelegramProviderAssociations(ctx context.Context, tx *gorm.DB, providerID uint) error {
	var enabledIDs []uint
	if err := tx.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Where("provider_id = ? AND status = ?", providerID, 1).
		Pluck("id", &enabledIDs).Error; err != nil {
		return err
	}
	if err := saveTelegramProviderStatusSnapshot(ctx, tx, providerID, enabledIDs); err != nil {
		return err
	}
	if len(enabledIDs) == 0 {
		return nil
	}
	return tx.WithContext(ctx).
		Model(&models.ModelWithProvider{}).
		Where("provider_id = ? AND id IN ?", providerID, enabledIDs).
		Update("status", 0).Error
}

func restoreTelegramProviderEnabledAssociations(ctx context.Context, tx *gorm.DB, providerID uint) error {
	enabledIDs, err := loadTelegramProviderStatusSnapshot(ctx, tx, providerID)
	if err != nil {
		return err
	}
	if len(enabledIDs) > 0 {
		if err := tx.WithContext(ctx).
			Model(&models.ModelWithProvider{}).
			Where("provider_id = ? AND id IN ?", providerID, enabledIDs).
			Update("status", 1).Error; err != nil {
			return err
		}
	}
	return tx.WithContext(ctx).
		Where("key = ?", telegramProviderStatusSnapshotKey(providerID)).
		Delete(&models.Config{}).Error
}

func saveTelegramProviderStatusSnapshot(ctx context.Context, tx *gorm.DB, providerID uint, enabledIDs []uint) error {
	raw, err := json.Marshal(enabledIDs)
	if err != nil {
		return err
	}
	key := telegramProviderStatusSnapshotKey(providerID)
	var existing models.Config
	if err := tx.WithContext(ctx).Where("key = ?", key).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.WithContext(ctx).Create(&models.Config{Key: key, Value: string(raw)}).Error
		}
		return err
	}
	return tx.WithContext(ctx).
		Model(&models.Config{}).
		Where("id = ?", existing.ID).
		Update("value", string(raw)).Error
}

func loadTelegramProviderStatusSnapshot(ctx context.Context, tx *gorm.DB, providerID uint) ([]uint, error) {
	var cfg models.Config
	if err := tx.WithContext(ctx).Where("key = ?", telegramProviderStatusSnapshotKey(providerID)).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errTelegramProviderSnapshotEmpty
		}
		return nil, err
	}
	var enabledIDs []uint
	if err := json.Unmarshal([]byte(cfg.Value), &enabledIDs); err != nil {
		return nil, err
	}
	return enabledIDs, nil
}

func telegramProviderStatusSnapshotKey(providerID uint) string {
	return models.KeyProviderStatusSnapshotPrefix + strconv.FormatUint(uint64(providerID), 10)
}

func sendTelegramToolText(ctx context.Context, client TelegramClient, chatID int64, text string) error {
	return sendTelegramAgentTextWithAttachments(ctx, client, chatID, text)
}

func isTelegramToolConfirm(raw string) bool {
	switch normalizeTelegramToolControl(raw) {
	case "/confirm", "确认", "确认执行", "执行", "是", "好的", "好", "可以", "继续":
		return true
	default:
		return false
	}
}

func isStrictTelegramToolConfirm(raw string) bool {
	switch normalizeTelegramToolControl(raw) {
	case "/confirm", "确认", "确认执行":
		return true
	default:
		return false
	}
}

func isTelegramToolCancel(raw string) bool {
	switch normalizeTelegramToolControl(raw) {
	case "/cancel", "取消", "放弃", "不用了", "停止":
		return true
	default:
		return false
	}
}

func isStrictTelegramToolCancel(raw string) bool {
	switch normalizeTelegramToolControl(raw) {
	case "/cancel", "取消":
		return true
	default:
		return false
	}
}

func hasPendingTelegramToolAction(chatID int64) bool {
	_, ok, err := loadTelegramPendingToolAction(context.Background(), chatID)
	if err != nil {
		_, ok = telegramPendingToolActions.Load(chatID)
		return ok
	}
	return ok
}

func normalizeTelegramToolControl(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.Index(value, "@"); strings.HasPrefix(value, "/") && idx > 0 {
		value = value[:idx]
	}
	return strings.Trim(value, " \t\r\n。！？!?,，；;：:")
}

func normalizeTelegramToolText(raw string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch r {
		case '，', ',', '。', '.', '：', ':', '；', ';', '！', '!', '？', '?', '（', '）', '(', ')', '“', '”', '"', '\'', '、':
			sb.WriteByte(' ')
		default:
			sb.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

func cleanupTelegramToolTarget(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, " \t\r\n。！？!?,，；;：:\"'“”‘’[]【】（）()")
	if value == "" {
		return ""
	}

	removePhrases := []string{
		"帮我", "请", "一下", "把", "将", "设置为", "改为", "改成", "状态",
		"启用", "开启", "打开", "上线", "恢复",
		"禁用", "关闭", "停用", "下线", "暂停",
		"列出", "查看", "列表", "有哪些", "显示",
		"模型", "提供商", "provider", "model",
	}
	for _, phrase := range removePhrases {
		value = strings.ReplaceAll(value, phrase, " ")
		value = strings.ReplaceAll(value, strings.ToUpper(phrase), " ")
	}
	value = strings.Trim(value, " \t\r\n。！？!?,，；;：:\"'“”‘’[]【】（）()")
	return cleanupTelegramBulkModelTarget(strings.Join(strings.Fields(value), " "))
}

func cleanupTelegramBulkModelTarget(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	removePhrases := []string{
		"的所有", "所有的", "的全部", "全部的", "的相关", "相关的", "的有关", "有关的",
		"全部", "所有", "相关", "有关", "匹配", "名称包含", "包含",
	}
	for _, phrase := range removePhrases {
		value = strings.ReplaceAll(value, phrase, " ")
		value = strings.ReplaceAll(value, strings.ToUpper(phrase), " ")
	}
	value = strings.Trim(value, " \t\r\n。！？!?,，；;：:\"'“”‘’[]【】（）()的")
	return strings.Join(strings.Fields(value), " ")
}

func isTelegramToolBulkModelRequest(normalized string, target string) bool {
	if containsAny(normalized, []string{"所有", "全部", "相关", "有关"}) {
		return true
	}
	return strings.TrimSpace(cleanupTelegramBulkModelTarget(target)) != strings.TrimSpace(target)
}

func parseTelegramToolID(raw string) (uint, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "id")
	value = strings.Trim(value, " \t\r\n:：#")
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

func telegramStatusVerb(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "禁用"
}

func telegramStatusLabel(status int) string {
	if status == 1 {
		return "启用"
	}
	return "禁用"
}

func telegramProviderStatusLabel(summary telegramProviderSummary) string {
	if summary.EnabledCount > 0 {
		return "启用"
	}
	if summary.TotalCount == 0 {
		return "未关联"
	}
	return "禁用"
}

func ambiguousTelegramToolTargetError(kind string, names []string) error {
	names = orderedUniqueStrings(names)
	if len(names) > 5 {
		names = names[:5]
	}
	return fmt.Errorf("%s匹配到多个结果，请使用更完整名称：%s", kind, strings.Join(names, "、"))
}

func exactModelNames(models []models.Model) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Name)
	}
	return names
}

func summarizeTelegramToolModelNames(rows []models.Model) string {
	if len(rows) == 0 {
		return "无"
	}
	names := exactModelNames(rows)
	sort.Strings(names)
	if len(names) > 8 {
		return strings.Join(names[:8], "、") + fmt.Sprintf(" 等 %d 个", len(names))
	}
	return strings.Join(names, "、")
}

func exactProviderNames(providers []models.Provider) []string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.Name)
	}
	return names
}

func sanitizeTelegramProviderProxyURL(raw string) (string, error) {
	proxyURL := strings.TrimSpace(raw)
	if proxyURL == "" {
		return "", nil
	}
	lower := strings.ToLower(proxyURL)
	if strings.HasPrefix(lower, "socket5://") {
		proxyURL = "socks5://" + proxyURL[len("socket5://"):]
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https", "socks5":
		return proxyURL, nil
	default:
		return "", fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
	}
}

func normalizeTelegramModelsFetchMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "v1_models", "v1", "models", "model", "通用", "普通":
		return "v1_models", nil
	case "api_pricing", "pricing", "newapi", "new_api", "new api", "new-api":
		return "api_pricing", nil
	default:
		return "", fmt.Errorf("模型获取方式必须是 v1_models 或 api_pricing，当前为：%s", raw)
	}
}

func telegramModelsFetchModeLabel(raw string) string {
	mode, err := normalizeTelegramModelsFetchMode(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	if mode == "api_pricing" {
		return "NewAPI(api_pricing)"
	}
	return "通用(v1_models)"
}

func normalizeTelegramInterfaceConversionTarget(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none", "off", "false", "关闭", "禁用", "无":
		return "", nil
	case "chat", "chat/completions", "/v1/chat/completions":
		return "chat", nil
	case "responses", "response", "/v1/responses":
		return "responses", nil
	case "messages", "message", "claude", "anthropic", "/v1/messages":
		return "messages", nil
	default:
		return "", fmt.Errorf("接口转换目标必须是 chat、responses 或 messages，当前为：%s", raw)
	}
}

func telegramInterfaceConversionLabel(enabled bool, target string) string {
	if !enabled {
		return "关闭"
	}
	switch strings.TrimSpace(target) {
	case "chat":
		return "开启 -> /v1/chat/completions"
	case "responses":
		return "开启 -> /v1/responses"
	case "messages":
		return "开启 -> /v1/messages"
	default:
		return "开启 -> " + strings.TrimSpace(target)
	}
}

func telegramDisplayOptionalValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "空"
	}
	return truncateTelegramToolText(value, 96)
}

func parseTelegramProviderConfigMap(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}, nil
	}
	normalized, err := models.NormalizeProviderConfig(raw)
	if err != nil {
		return nil, err
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(normalized), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func marshalTelegramProviderConfigMap(values map[string]string) (string, error) {
	raw, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return "", err
	}
	return models.NormalizeProviderConfig(string(raw))
}

func telegramProviderConfigValueToString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func summarizeTelegramProviderConfigChange(patch telegramProviderConfigPatch) string {
	parts := make([]string, 0, 3)
	if patch.ConfigReplaced {
		parts = append(parts, "替换完整配置")
	} else if len(patch.ConfigChangedKeys) > 0 {
		parts = append(parts, "更新配置字段："+strings.Join(maskTelegramProviderConfigKeys(patch.ConfigChangedKeys), "、"))
	}
	if len(patch.ConfigRemovedKeys) > 0 {
		parts = append(parts, "删除配置字段："+strings.Join(patch.ConfigRemovedKeys, "、"))
	}
	if len(parts) == 0 {
		return "配置"
	}
	return strings.Join(parts, "，")
}

func maskTelegramProviderConfigKeys(keys []string) []string {
	result := make([]string, 0, len(keys))
	for _, key := range orderedUniqueStrings(keys) {
		if isSensitiveTelegramProviderConfigKey(key) {
			result = append(result, key+"（已隐藏）")
			continue
		}
		result = append(result, key)
	}
	return result
}

func maskTelegramProviderConfigValue(key string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "空"
	}
	if isSensitiveTelegramProviderConfigKey(key) {
		count := countTelegramProviderSecretValues(value)
		if count > 1 {
			return fmt.Sprintf("已隐藏（%d 个值）", count)
		}
		return "已隐藏"
	}
	return truncateTelegramToolText(value, 120)
}

func isSensitiveTelegramProviderConfigKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, needle := range []string{"api_key", "apikey", "key", "token", "secret", "password", "authorization", "bearer"} {
		if strings.Contains(key, needle) {
			return true
		}
	}
	return false
}

func countTelegramProviderSecretValues(value string) int {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == '\n' || r == '\r'
	})
	count := 0
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	if count == 0 {
		return 1
	}
	return count
}

func truncateTelegramToolText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func orderedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
