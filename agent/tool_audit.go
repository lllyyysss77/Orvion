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
	telegramAgentToolLogSourceConfirmation = "confirmation"

	telegramAgentToolLogStatusCompleted           = "completed"
	telegramAgentToolLogStatusPendingConfirmation = "pending_confirmation"
	telegramAgentToolLogStatusExecuted            = "executed"
	telegramAgentToolLogStatusFailed              = "failed"
	telegramAgentToolLogStatusCancelled           = "cancelled"
)

func recordTelegramAgentFunctionToolCallLog(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, toolCall telegramAgentOpenAIToolCall, rawArgs string, toolResult string) {
	payload := parseTelegramAgentToolResultPayload(toolResult)
	status := telegramAgentToolLogStatusCompleted
	if !payload.OK {
		status = telegramAgentToolLogStatusFailed
	} else if payload.Final && telegramAgentRequiresToolConfirmation(cfg) && strings.Contains(payload.Text, "待确认操作") {
		status = telegramAgentToolLogStatusPendingConfirmation
	} else if payload.Final {
		status = telegramAgentToolLogStatusExecuted
	}

	log := models.TelegramAgentToolCallLog{
		ChatID:               chatID,
		Source:               telegramAgentToolLogSourceFunctionCall,
		ToolCallID:           strings.TrimSpace(toolCall.ID),
		ToolName:             strings.TrimSpace(toolCall.Function.Name),
		Arguments:            maskTelegramAgentToolArguments(rawArgs),
		Result:               payload.Text,
		Status:               status,
		OK:                   boolToInt(payload.OK),
		Final:                boolToInt(payload.Final),
		RequiresConfirmation: boolToInt(telegramAgentRequiresToolConfirmation(cfg)),
	}
	if !payload.OK {
		log.Error = payload.Text
	}
	recordTelegramAgentToolCallLog(ctx, log)
}

func recordTelegramAgentPreparedActionLog(ctx context.Context, action telegramToolAction, result string, requireConfirmation bool) {
	status := telegramAgentToolLogStatusExecuted
	executedAt := time.Now()
	var executedAtPtr *time.Time = &executedAt
	if requireConfirmation {
		status = telegramAgentToolLogStatusPendingConfirmation
		executedAtPtr = nil
	}

	recordTelegramAgentToolCallLog(ctx, models.TelegramAgentToolCallLog{
		ChatID:               action.ChatID,
		Source:               telegramAgentToolLogSourceToolAction,
		ToolName:             string(action.Kind),
		Result:               result,
		Status:               status,
		OK:                   1,
		Final:                1,
		RequiresConfirmation: boolToInt(requireConfirmation),
		ActionKind:           string(action.Kind),
		ActionSummary:        action.Summary,
		ExecutedAt:           executedAtPtr,
	})
}

func recordTelegramAgentToolActionFailureLog(ctx context.Context, action telegramToolAction, err error, requireConfirmation bool) {
	if err == nil {
		return
	}
	recordTelegramAgentToolCallLog(ctx, models.TelegramAgentToolCallLog{
		ChatID:               action.ChatID,
		Source:               telegramAgentToolLogSourceToolAction,
		ToolName:             string(action.Kind),
		Result:               err.Error(),
		Status:               telegramAgentToolLogStatusFailed,
		OK:                   0,
		Final:                1,
		RequiresConfirmation: boolToInt(requireConfirmation),
		ActionKind:           string(action.Kind),
		ActionSummary:        action.Summary,
		Error:                err.Error(),
	})
}

func recordTelegramAgentConfirmationLog(ctx context.Context, action telegramToolAction, status string, result string, err error) {
	now := time.Now()
	log := models.TelegramAgentToolCallLog{
		ChatID:               action.ChatID,
		Source:               telegramAgentToolLogSourceConfirmation,
		ToolName:             string(action.Kind),
		Result:               strings.TrimSpace(result),
		Status:               status,
		OK:                   1,
		Final:                1,
		RequiresConfirmation: 1,
		ActionKind:           string(action.Kind),
		ActionSummary:        action.Summary,
	}
	switch status {
	case telegramAgentToolLogStatusExecuted:
		log.ConfirmedAt = &now
		log.ExecutedAt = &now
	case telegramAgentToolLogStatusCancelled:
		log.CancelledAt = &now
	case telegramAgentToolLogStatusFailed:
		log.ConfirmedAt = &now
		log.OK = 0
	}
	if err != nil {
		log.Error = err.Error()
		if log.Result == "" {
			log.Result = err.Error()
		}
	}
	recordTelegramAgentToolCallLog(ctx, log)
}

func recordTelegramAgentToolCallLog(ctx context.Context, log models.TelegramAgentToolCallLog) {
	if models.DB == nil {
		return
	}
	if strings.TrimSpace(log.Status) == "" {
		log.Status = telegramAgentToolLogStatusCompleted
	}
	_ = models.DB.WithContext(ctx).Create(&log).Error
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
