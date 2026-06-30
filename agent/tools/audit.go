package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/racio/orvion/models"
)

const (
	ToolLogSourceFunctionCall = "function_call"
	ToolLogSourceToolAction   = "tool_action"

	ToolLogStatusExecuting = "executing"
	ToolLogStatusCompleted = "completed"
	ToolLogStatusExecuted  = "executed"
	ToolLogStatusFailed    = "failed"
)

const (
	telegramAgentToolLogSourceFunctionCall = ToolLogSourceFunctionCall
	telegramAgentToolLogSourceToolAction   = ToolLogSourceToolAction

	telegramAgentToolLogStatusExecuting = ToolLogStatusExecuting
	telegramAgentToolLogStatusCompleted = ToolLogStatusCompleted
	telegramAgentToolLogStatusExecuted  = ToolLogStatusExecuted
	telegramAgentToolLogStatusFailed    = ToolLogStatusFailed
)

func RecordFunctionToolCallExecutingLog(ctx context.Context, runtime Runtime, chatID int64, toolCall FunctionCall, rawArgs string) uint {
	return recordTelegramAgentToolCallLog(ctx, runtime, models.TelegramAgentToolCallLog{
		ChatID:         chatID,
		ConversationID: runtime.conversationID(ctx, chatID),
		Source:         telegramAgentToolLogSourceFunctionCall,
		ToolCallID:     strings.TrimSpace(toolCall.ID),
		ToolName:       strings.TrimSpace(toolCall.Name),
		Arguments:      maskTelegramAgentToolArguments(rawArgs),
		Status:         telegramAgentToolLogStatusExecuting,
		OK:             0,
		Final:          0,
	})
}

func FinishFunctionToolCallLog(ctx context.Context, runtime Runtime, logID uint, chatID int64, toolCall FunctionCall, rawArgs string, toolResult string) {
	log := buildTelegramAgentFunctionToolCallLog(ctx, runtime, chatID, toolCall, rawArgs, toolResult)
	if logID == 0 {
		recordTelegramAgentToolCallLog(ctx, runtime, log)
		return
	}
	updateTelegramAgentToolCallLog(ctx, runtime, logID, log)
}

func buildTelegramAgentFunctionToolCallLog(ctx context.Context, runtime Runtime, chatID int64, toolCall FunctionCall, rawArgs string, toolResult string) models.TelegramAgentToolCallLog {
	payload := parseTelegramAgentToolResultPayload(toolResult)
	toolName := strings.TrimSpace(toolCall.Name)
	resultText := payload.Text
	if toolName == NameRunTerminalCommand {
		resultText = terminalCommandOutputForLog(payload.Text)
	}
	status := telegramAgentToolLogStatusCompleted
	if !payload.OK {
		status = telegramAgentToolLogStatusFailed
	} else if payload.Final {
		status = telegramAgentToolLogStatusExecuted
	}

	log := models.TelegramAgentToolCallLog{
		ChatID:         chatID,
		ConversationID: runtime.conversationID(ctx, chatID),
		Source:         telegramAgentToolLogSourceFunctionCall,
		ToolCallID:     strings.TrimSpace(toolCall.ID),
		ToolName:       toolName,
		Arguments:      maskTelegramAgentToolArguments(rawArgs),
		Result:         resultText,
		Status:         status,
		OK:             boolToInt(payload.OK),
		Final:          boolToInt(payload.Final),
	}
	if !payload.OK {
		log.Error = resultText
	}
	return log
}

func recordTelegramAgentToolActionExecutingLog(ctx context.Context, action telegramToolAction, source string) uint {
	return recordTelegramAgentToolCallLog(ctx, Runtime{}, models.TelegramAgentToolCallLog{
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

func RecordToolActionExecutingLog(ctx context.Context, action Action, source string) uint {
	return recordTelegramAgentToolActionExecutingLog(ctx, action, source)
}

func finishTelegramAgentPreparedActionLog(ctx context.Context, logID uint, action telegramToolAction, result string) {
	log := buildTelegramAgentPreparedActionLog(action, result)
	if logID == 0 {
		recordTelegramAgentToolCallLog(ctx, Runtime{}, log)
		return
	}
	updateTelegramAgentToolCallLog(ctx, Runtime{}, logID, log)
}

func FinishPreparedActionLog(ctx context.Context, logID uint, action Action, result string) {
	finishTelegramAgentPreparedActionLog(ctx, logID, action, result)
}

func buildTelegramAgentPreparedActionLog(action telegramToolAction, result string) models.TelegramAgentToolCallLog {
	executedAt := time.Now()
	resultText := result
	if action.Kind == telegramToolActionRunTerminalCommand {
		resultText = terminalCommandOutputForLog(result)
	}

	return models.TelegramAgentToolCallLog{
		ChatID:         action.ChatID,
		ConversationID: action.ConversationID,
		Source:         telegramAgentToolLogSourceToolAction,
		ToolName:       string(action.Kind),
		Result:         resultText,
		Status:         telegramAgentToolLogStatusExecuted,
		OK:             1,
		Final:          1,
		ActionKind:     string(action.Kind),
		ActionSummary:  action.Summary,
		ExecutedAt:     &executedAt,
	}
}

func finishTelegramAgentToolActionFailureLog(ctx context.Context, logID uint, action telegramToolAction, err error) {
	if err == nil {
		return
	}
	log := buildTelegramAgentToolActionFailureLog(action, err)
	if logID == 0 {
		recordTelegramAgentToolCallLog(ctx, Runtime{}, log)
		return
	}
	updateTelegramAgentToolCallLog(ctx, Runtime{}, logID, log)
}

func buildTelegramAgentToolActionFailureLog(action telegramToolAction, err error) models.TelegramAgentToolCallLog {
	resultText := err.Error()
	if action.Kind == telegramToolActionRunTerminalCommand {
		resultText = terminalCommandOutputForLog(resultText)
	}
	return models.TelegramAgentToolCallLog{
		ChatID:         action.ChatID,
		ConversationID: action.ConversationID,
		Source:         telegramAgentToolLogSourceToolAction,
		ToolName:       string(action.Kind),
		Result:         resultText,
		Status:         telegramAgentToolLogStatusFailed,
		OK:             0,
		Final:          1,
		ActionKind:     string(action.Kind),
		ActionSummary:  action.Summary,
		Error:          resultText,
	}
}

func BuildToolActionFailureLog(action Action, err error) models.TelegramAgentToolCallLog {
	return buildTelegramAgentToolActionFailureLog(action, err)
}

func terminalCommandOutputForLog(raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) == 0 {
		return ""
	}

	sections := make([]string, 0, 2)
	for index := 0; index < len(lines); index++ {
		label := strings.TrimSpace(lines[index])
		if label != "stdout：" && label != "stderr：" {
			continue
		}

		contentLines := make([]string, 0)
		for next := index + 1; next < len(lines); next++ {
			nextLabel := strings.TrimSpace(lines[next])
			if isTelegramCommandResultHeaderLine(nextLabel) {
				break
			}
			contentLines = append(contentLines, lines[next])
			index = next
		}
		content := strings.TrimSpace(strings.Join(contentLines, "\n"))
		if content == "" {
			continue
		}
		sections = append(sections, label+"\n"+content)
	}

	if len(sections) > 0 {
		return strings.Join(sections, "\n")
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "输出：无"
	}
	return trimmed
}

func isTelegramCommandResultHeaderLine(line string) bool {
	switch {
	case line == "已执行命令":
		return true
	case strings.HasPrefix(line, "命令："):
		return true
	case strings.HasPrefix(line, "工作目录："):
		return true
	case strings.HasPrefix(line, "退出码："):
		return true
	case strings.HasPrefix(line, "Skill："):
		return true
	case strings.HasPrefix(line, "脚本："):
		return true
	case line == "stdout：":
		return true
	case line == "stderr：":
		return true
	case strings.HasPrefix(line, "输出："):
		return true
	default:
		return false
	}
}

func RecordToolCallLog(ctx context.Context, runtime Runtime, log models.TelegramAgentToolCallLog) uint {
	return recordTelegramAgentToolCallLog(ctx, runtime, log)
}

func recordTelegramAgentToolCallLog(ctx context.Context, runtime Runtime, log models.TelegramAgentToolCallLog) uint {
	if models.DB == nil {
		return 0
	}
	if strings.TrimSpace(log.Status) == "" {
		log.Status = telegramAgentToolLogStatusCompleted
	}
	if strings.TrimSpace(log.ConversationID) == "" && log.ChatID != 0 {
		log.ConversationID = runtime.conversationID(ctx, log.ChatID)
	}
	if err := models.DB.WithContext(ctx).Create(&log).Error; err != nil {
		return 0
	}
	return log.ID
}

func UpdateToolCallLog(ctx context.Context, runtime Runtime, id uint, log models.TelegramAgentToolCallLog) {
	updateTelegramAgentToolCallLog(ctx, runtime, id, log)
}

func updateTelegramAgentToolCallLog(ctx context.Context, runtime Runtime, id uint, log models.TelegramAgentToolCallLog) {
	if models.DB == nil || id == 0 {
		return
	}
	if strings.TrimSpace(log.Status) == "" {
		log.Status = telegramAgentToolLogStatusCompleted
	}
	if strings.TrimSpace(log.ConversationID) == "" && log.ChatID != 0 {
		log.ConversationID = runtime.conversationID(ctx, log.ChatID)
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

func ParseResultPayload(raw string) ResultPayload {
	return parseTelegramAgentToolResultPayload(raw)
}

func parseTelegramAgentToolResultPayload(raw string) ResultPayload {
	var payload ResultPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ResultPayload{
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
