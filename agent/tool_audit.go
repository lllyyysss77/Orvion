package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/racio/orvion/models"
)

const (
	telegramAgentToolLogSourceFunctionCall = "function_call"
	telegramAgentToolLogSourceToolAction   = "tool_action"

	telegramAgentToolLogStatusExecuting = "executing"
	telegramAgentToolLogStatusCompleted = "completed"
	telegramAgentToolLogStatusExecuted  = "executed"
	telegramAgentToolLogStatusFailed    = "failed"
)

func recordTelegramAgentFunctionToolCallLog(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCall telegramAgentOpenAIToolCall, rawArgs string, toolResult string) {
	recordTelegramAgentToolCallLog(ctx, buildTelegramAgentFunctionToolCallLog(ctx, chatID, cfg, toolCall, rawArgs, toolResult))
}

func recordTelegramAgentFunctionToolCallExecutingLog(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCall telegramAgentOpenAIToolCall, rawArgs string) uint {
	return recordTelegramAgentToolCallLog(ctx, models.TelegramAgentToolCallLog{
		ChatID:         chatID,
		ConversationID: resolveTelegramAgentToolLogConversationID(ctx, chatID),
		Source:         telegramAgentToolLogSourceFunctionCall,
		ToolCallID:     strings.TrimSpace(toolCall.ID),
		ToolName:       strings.TrimSpace(toolCall.Function.Name),
		Arguments:      maskTelegramAgentToolArguments(rawArgs),
		Status:         telegramAgentToolLogStatusExecuting,
		OK:             0,
		Final:          0,
	})
}

func finishTelegramAgentFunctionToolCallLog(ctx context.Context, logID uint, chatID int64, cfg models.TelegramAgentConfig, toolCall telegramAgentOpenAIToolCall, rawArgs string, toolResult string) {
	log := buildTelegramAgentFunctionToolCallLog(ctx, chatID, cfg, toolCall, rawArgs, toolResult)
	if logID == 0 {
		recordTelegramAgentToolCallLog(ctx, log)
		return
	}
	updateTelegramAgentToolCallLog(ctx, logID, log)
}

func buildTelegramAgentFunctionToolCallLog(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCall telegramAgentOpenAIToolCall, rawArgs string, toolResult string) models.TelegramAgentToolCallLog {
	payload := parseTelegramAgentToolResultPayload(toolResult)
	status := telegramAgentToolLogStatusCompleted
	if !payload.OK {
		status = telegramAgentToolLogStatusFailed
	} else if payload.Final {
		status = telegramAgentToolLogStatusExecuted
	}

	log := models.TelegramAgentToolCallLog{
		ChatID:         chatID,
		ConversationID: resolveTelegramAgentToolLogConversationID(ctx, chatID),
		Source:         telegramAgentToolLogSourceFunctionCall,
		ToolCallID:     strings.TrimSpace(toolCall.ID),
		ToolName:       strings.TrimSpace(toolCall.Function.Name),
		Arguments:      maskTelegramAgentToolArguments(rawArgs),
		Result:         payload.Text,
		Status:         status,
		OK:             boolToInt(payload.OK),
		Final:          boolToInt(payload.Final),
	}
	if !payload.OK {
		log.Error = payload.Text
	}
	return log
}

func recordTelegramAgentPreparedActionLog(ctx context.Context, action telegramToolAction, result string) {
	recordTelegramAgentToolCallLog(ctx, buildTelegramAgentPreparedActionLog(action, result))
}

func recordTelegramAgentToolActionExecutingLog(ctx context.Context, action telegramToolAction, source string) uint {
	return recordTelegramAgentToolCallLog(ctx, models.TelegramAgentToolCallLog{
		ChatID:         action.ChatID,
		ConversationID: action.ConversationID,
		Source:         source,
		ToolName:       string(action.Kind),
		Status:         telegramAgentToolLogStatusExecuting,
		OK:             0,
		Final:          0,
		ActionKind:     string(action.Kind),
		ActionSummary:  action.Summary,
	})
}

func finishTelegramAgentPreparedActionLog(ctx context.Context, logID uint, action telegramToolAction, result string) {
	log := buildTelegramAgentPreparedActionLog(action, result)
	if logID == 0 {
		recordTelegramAgentToolCallLog(ctx, log)
		return
	}
	updateTelegramAgentToolCallLog(ctx, logID, log)
}

func buildTelegramAgentPreparedActionLog(action telegramToolAction, result string) models.TelegramAgentToolCallLog {
	executedAt := time.Now()

	return models.TelegramAgentToolCallLog{
		ChatID:         action.ChatID,
		ConversationID: action.ConversationID,
		Source:         telegramAgentToolLogSourceToolAction,
		ToolName:       string(action.Kind),
		Result:         result,
		Status:         telegramAgentToolLogStatusExecuted,
		OK:             1,
		Final:          1,
		ActionKind:     string(action.Kind),
		ActionSummary:  action.Summary,
		ExecutedAt:     &executedAt,
	}
}

func recordTelegramAgentToolActionFailureLog(ctx context.Context, action telegramToolAction, err error) {
	if err == nil {
		return
	}
	recordTelegramAgentToolCallLog(ctx, buildTelegramAgentToolActionFailureLog(action, err))
}

func finishTelegramAgentToolActionFailureLog(ctx context.Context, logID uint, action telegramToolAction, err error) {
	if err == nil {
		return
	}
	log := buildTelegramAgentToolActionFailureLog(action, err)
	if logID == 0 {
		recordTelegramAgentToolCallLog(ctx, log)
		return
	}
	updateTelegramAgentToolCallLog(ctx, logID, log)
}

func buildTelegramAgentToolActionFailureLog(action telegramToolAction, err error) models.TelegramAgentToolCallLog {
	return models.TelegramAgentToolCallLog{
		ChatID:         action.ChatID,
		ConversationID: action.ConversationID,
		Source:         telegramAgentToolLogSourceToolAction,
		ToolName:       string(action.Kind),
		Result:         err.Error(),
		Status:         telegramAgentToolLogStatusFailed,
		OK:             0,
		Final:          1,
		ActionKind:     string(action.Kind),
		ActionSummary:  action.Summary,
		Error:          err.Error(),
	}
}

func recordTelegramAgentToolCallLog(ctx context.Context, log models.TelegramAgentToolCallLog) uint {
	if models.DB == nil {
		return 0
	}
	if strings.TrimSpace(log.Status) == "" {
		log.Status = telegramAgentToolLogStatusCompleted
	}
	if strings.TrimSpace(log.ConversationID) == "" && log.ChatID != 0 {
		log.ConversationID = resolveTelegramAgentToolLogConversationID(ctx, log.ChatID)
	}
	if err := models.DB.WithContext(ctx).Create(&log).Error; err != nil {
		return 0
	}
	return log.ID
}

func updateTelegramAgentToolCallLog(ctx context.Context, id uint, log models.TelegramAgentToolCallLog) {
	if models.DB == nil || id == 0 {
		return
	}
	if strings.TrimSpace(log.Status) == "" {
		log.Status = telegramAgentToolLogStatusCompleted
	}
	if strings.TrimSpace(log.ConversationID) == "" && log.ChatID != 0 {
		log.ConversationID = resolveTelegramAgentToolLogConversationID(ctx, log.ChatID)
	}
	values := map[string]any{
		"chat_id":         log.ChatID,
		"conversation_id": log.ConversationID,
		"source":          log.Source,
		"tool_call_id":    log.ToolCallID,
		"tool_name":       log.ToolName,
		"arguments":       log.Arguments,
		"result":          log.Result,
		"status":          log.Status,
		"ok":              log.OK,
		"final":           log.Final,
		"action_kind":     log.ActionKind,
		"action_summary":  log.ActionSummary,
		"error":           log.Error,
		"executed_at":     log.ExecutedAt,
	}
	_ = models.DB.WithContext(ctx).Model(&models.TelegramAgentToolCallLog{}).Where("id = ?", id).Updates(values).Error
}

func resolveTelegramAgentToolLogConversationID(ctx context.Context, chatID int64) string {
	conversationID, err := resolveTelegramActiveConversationID(ctx, chatID, getTelegramSession(chatID))
	if err != nil {
		return ""
	}
	return conversationID
}

func parseTelegramAgentToolResultPayload(raw string) telegramAgentToolResultPayload {
	var payload telegramAgentToolResultPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return telegramAgentToolResultPayload{
			OK:   false,
			Text: strings.TrimSpace(raw),
		}
	}
	payload.Text = strings.TrimSpace(payload.Text)
	return payload
}

func maskTelegramAgentToolArguments(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw
	}
	masked := maskTelegramAgentToolValue("", parsed)
	data, err := json.Marshal(masked)
	if err != nil {
		return raw
	}
	return string(data)
}

func maskTelegramAgentToolValue(key string, value any) any {
	if isTelegramAgentSensitiveLogKey(key) {
		return "已隐藏"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[childKey] = maskTelegramAgentToolValue(childKey, childValue)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, maskTelegramAgentToolValue(key, item))
		}
		return out
	default:
		return value
	}
}

func isTelegramAgentSensitiveLogKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"api_key", "apikey", "token", "secret", "password"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
