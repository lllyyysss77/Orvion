package admin

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/agent"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	defaultTelegramAgentLogLimit = 120
	maxTelegramAgentLogLimit     = 500
)

type telegramAgentToolLogResponse struct {
	Summary  telegramAgentToolLogSummary   `json:"summary"`
	Sessions []telegramAgentSessionSummary `json:"sessions"`
	Steps    []telegramAgentToolLogStep    `json:"steps"`
}

type telegramAgentToolLogSummary struct {
	Total                int64  `json:"total"`
	Returned             int    `json:"returned"`
	Executing            int    `json:"executing"`
	Completed            int    `json:"completed"`
	Executed             int    `json:"executed"`
	Pending              int    `json:"pending"`
	Failed               int    `json:"failed"`
	Cancelled            int    `json:"cancelled"`
	ActiveChats          int    `json:"active_chats"`
	LatestAt             string `json:"latest_at,omitempty"`
	LatestChatID         int64  `json:"latest_chat_id,omitempty"`
	LatestConversationID string `json:"latest_conversation_id,omitempty"`
	LatestToolName       string `json:"latest_tool_name,omitempty"`
	LatestStatus         string `json:"latest_status,omitempty"`
}

type telegramAgentSessionSummary struct {
	ChatID         int64  `json:"chat_id"`
	ConversationID string `json:"conversation_id"`
	TotalSteps     int    `json:"total_steps"`
	Executing      int    `json:"executing"`
	Completed      int    `json:"completed"`
	Executed       int    `json:"executed"`
	Pending        int    `json:"pending"`
	Failed         int    `json:"failed"`
	Cancelled      int    `json:"cancelled"`
	LatestAt       string `json:"latest_at,omitempty"`
	LatestToolName string `json:"latest_tool_name,omitempty"`
	LatestStatus   string `json:"latest_status,omitempty"`
}

type telegramAgentToolLogStep struct {
	ID                   uint    `json:"id"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	ChatID               int64   `json:"chat_id"`
	ConversationID       string  `json:"conversation_id"`
	Source               string  `json:"source"`
	ToolCallID           string  `json:"tool_call_id"`
	ToolName             string  `json:"tool_name"`
	Arguments            string  `json:"arguments"`
	Result               string  `json:"result"`
	Status               string  `json:"status"`
	OK                   bool    `json:"ok"`
	Final                bool    `json:"final"`
	RequiresConfirmation bool    `json:"requires_confirmation"`
	ActionKind           string  `json:"action_kind"`
	ActionSummary        string  `json:"action_summary"`
	Error                string  `json:"error"`
	ConfirmedAt          *string `json:"confirmed_at,omitempty"`
	ExecutedAt           *string `json:"executed_at,omitempty"`
	CancelledAt          *string `json:"cancelled_at,omitempty"`
}

type telegramAgentDeleteSessionResponse struct {
	ConversationID string  `json:"conversation_id"`
	ChatIDs        []int64 `json:"chat_ids"`
	MessageRows    int64   `json:"message_rows"`
	LogRows        int64   `json:"log_rows"`
	SessionRows    int64   `json:"session_rows"`
	PendingRows    int64   `json:"pending_rows"`
}

func GetTelegramAgentToolCallLogs(c *gin.Context) {
	if models.DB == nil {
		common.InternalServerError(c, "数据库未初始化")
		return
	}

	limit := parseTelegramAgentLogLimit(c.Query("limit"))
	query := models.DB.WithContext(c.Request.Context()).Model(&models.TelegramAgentToolCallLog{})
	query = applyTelegramAgentToolLogFilters(c, query)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.InternalServerError(c, "统计 TG Agent 日志失败: "+err.Error())
		return
	}

	var rows []models.TelegramAgentToolCallLog
	if err := query.Order("created_at DESC").Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		common.InternalServerError(c, "读取 TG Agent 日志失败: "+err.Error())
		return
	}

	steps := make([]telegramAgentToolLogStep, 0, len(rows))
	for _, row := range rows {
		steps = append(steps, telegramAgentToolLogStepFromModel(row))
	}

	summary, sessions := buildTelegramAgentToolLogSummary(total, steps)
	common.Success(c, telegramAgentToolLogResponse{
		Summary:  summary,
		Sessions: sessions,
		Steps:    steps,
	})
}

func DeleteTelegramAgentSession(c *gin.Context) {
	if models.DB == nil {
		common.InternalServerError(c, "数据库未初始化")
		return
	}

	conversationID := strings.TrimSpace(c.Param("conversation_id"))
	if conversationID == "" {
		conversationID = strings.TrimSpace(c.Query("conversation_id"))
	}
	if conversationID == "all" {
		common.BadRequest(c, "会话 ID 不能为空")
		return
	}
	chatID, err := parseTelegramAgentOptionalChatID(c.Query("chat_id"))
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	if conversationID == "" && chatID == 0 {
		common.BadRequest(c, "未记录会话需要 chat_id")
		return
	}

	var response telegramAgentDeleteSessionResponse
	response.ConversationID = conversationID
	if err := models.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		chatIDs, err := collectTelegramAgentSessionChatIDs(tx, conversationID, chatID)
		if err != nil {
			return err
		}
		response.ChatIDs = chatIDs

		logResult := withTelegramAgentConversationScope(tx, conversationID, chatID).Delete(&models.TelegramAgentToolCallLog{})
		if logResult.Error != nil {
			return logResult.Error
		}
		response.LogRows = logResult.RowsAffected

		messageResult := withTelegramAgentConversationScope(tx, conversationID, chatID).Delete(&models.TelegramAgentMessage{})
		if messageResult.Error != nil {
			return messageResult.Error
		}
		response.MessageRows = messageResult.RowsAffected

		sessionResult := withTelegramAgentConversationScope(tx, conversationID, chatID).Delete(&models.TelegramAgentSession{})
		if sessionResult.Error != nil {
			return sessionResult.Error
		}
		response.SessionRows = sessionResult.RowsAffected

		if len(chatIDs) > 0 {
			pendingResult := tx.Where("chat_id IN ?", chatIDs).Delete(&models.TelegramAgentPendingAction{})
			if pendingResult.Error != nil {
				return pendingResult.Error
			}
			response.PendingRows = pendingResult.RowsAffected
		}
		return nil
	}); err != nil {
		common.InternalServerError(c, "删除 TG Agent 会话失败: "+err.Error())
		return
	}

	if response.LogRows == 0 && response.MessageRows == 0 && response.SessionRows == 0 {
		common.NotFound(c, "未找到 TG Agent 会话")
		return
	}
	for _, chatID := range response.ChatIDs {
		agent.ForgetTelegramConversation(chatID, conversationID)
	}
	common.Success(c, response)
}

func parseTelegramAgentOptionalChatID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	chatID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || chatID <= 0 {
		return 0, errTelegramAgentInvalidChatID
	}
	return chatID, nil
}

var errTelegramAgentInvalidChatID = errors.New("chat_id 无效")

func collectTelegramAgentSessionChatIDs(tx *gorm.DB, conversationID string, chatID int64) ([]int64, error) {
	seen := make(map[int64]struct{})
	addRows := func(rows []int64) {
		for _, chatID := range rows {
			if chatID == 0 {
				continue
			}
			seen[chatID] = struct{}{}
		}
	}

	var logChatIDs []int64
	if err := withTelegramAgentConversationScope(tx.Model(&models.TelegramAgentToolCallLog{}), conversationID, chatID).Distinct().Pluck("chat_id", &logChatIDs).Error; err != nil {
		return nil, err
	}
	addRows(logChatIDs)

	var messageChatIDs []int64
	if err := withTelegramAgentConversationScope(tx.Model(&models.TelegramAgentMessage{}), conversationID, chatID).Distinct().Pluck("chat_id", &messageChatIDs).Error; err != nil {
		return nil, err
	}
	addRows(messageChatIDs)

	var sessionChatIDs []int64
	if err := withTelegramAgentConversationScope(tx.Model(&models.TelegramAgentSession{}), conversationID, chatID).Distinct().Pluck("chat_id", &sessionChatIDs).Error; err != nil {
		return nil, err
	}
	addRows(sessionChatIDs)

	result := make([]int64, 0, len(seen))
	for chatID := range seen {
		result = append(result, chatID)
	}
	return result, nil
}

func withTelegramAgentConversationScope(query *gorm.DB, conversationID string, chatID int64) *gorm.DB {
	if chatID > 0 {
		query = query.Where("chat_id = ?", chatID)
	}
	if strings.TrimSpace(conversationID) == "" {
		return query.Where("(conversation_id = ? OR conversation_id IS NULL)", "")
	}
	return query.Where("conversation_id = ?", conversationID)
}

func applyTelegramAgentToolLogFilters(c *gin.Context, query *gorm.DB) *gorm.DB {
	if chatID := strings.TrimSpace(c.Query("chat_id")); chatID != "" {
		if parsed, err := strconv.ParseInt(chatID, 10, 64); err == nil {
			query = query.Where("chat_id = ?", parsed)
		}
	}

	if status := strings.TrimSpace(c.Query("status")); status != "" && status != "all" {
		query = query.Where("status = ?", status)
	}
	if conversationID := strings.TrimSpace(c.Query("conversation_id")); conversationID != "" && conversationID != "all" {
		query = query.Where("conversation_id = ?", conversationID)
	}
	if source := strings.TrimSpace(c.Query("source")); source != "" && source != "all" {
		query = query.Where("source = ?", source)
	}
	if toolName := strings.TrimSpace(c.Query("tool_name")); toolName != "" {
		query = query.Where("tool_name LIKE ?", "%"+toolName+"%")
	}
	if keyword := strings.TrimSpace(c.Query("query")); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where(
			"(tool_name LIKE ? OR arguments LIKE ? OR result LIKE ? OR error LIKE ? OR action_summary LIKE ? OR status LIKE ?)",
			like, like, like, like, like, like,
		)
	}
	if recentMinutes := parseTelegramAgentPositiveInt(c.Query("recent_minutes")); recentMinutes > 0 {
		query = query.Where("created_at >= ?", time.Now().Add(-time.Duration(recentMinutes)*time.Minute))
	}
	return query
}

func parseTelegramAgentLogLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return defaultTelegramAgentLogLimit
	}
	if value > maxTelegramAgentLogLimit {
		return maxTelegramAgentLogLimit
	}
	return value
}

func parseTelegramAgentPositiveInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func telegramAgentToolLogStepFromModel(row models.TelegramAgentToolCallLog) telegramAgentToolLogStep {
	return telegramAgentToolLogStep{
		ID:                   row.ID,
		CreatedAt:            formatTelegramAgentLogTime(row.CreatedAt),
		UpdatedAt:            formatTelegramAgentLogTime(row.UpdatedAt),
		ChatID:               row.ChatID,
		ConversationID:       row.ConversationID,
		Source:               row.Source,
		ToolCallID:           row.ToolCallID,
		ToolName:             row.ToolName,
		Arguments:            row.Arguments,
		Result:               row.Result,
		Status:               row.Status,
		OK:                   row.OK == 1,
		Final:                row.Final == 1,
		RequiresConfirmation: row.RequiresConfirmation == 1,
		ActionKind:           row.ActionKind,
		ActionSummary:        row.ActionSummary,
		Error:                row.Error,
		ConfirmedAt:          formatTelegramAgentLogTimePtr(row.ConfirmedAt),
		ExecutedAt:           formatTelegramAgentLogTimePtr(row.ExecutedAt),
		CancelledAt:          formatTelegramAgentLogTimePtr(row.CancelledAt),
	}
}

func buildTelegramAgentToolLogSummary(total int64, steps []telegramAgentToolLogStep) (telegramAgentToolLogSummary, []telegramAgentSessionSummary) {
	summary := telegramAgentToolLogSummary{Total: total, Returned: len(steps)}
	sessionMap := make(map[string]*telegramAgentSessionSummary)
	sessionOrder := make([]string, 0)

	for index, step := range steps {
		addTelegramAgentStatusCount(step.Status, &summary.Executing, &summary.Completed, &summary.Executed, &summary.Pending, &summary.Failed, &summary.Cancelled)
		if index == 0 {
			summary.LatestAt = step.CreatedAt
			summary.LatestChatID = step.ChatID
			summary.LatestConversationID = step.ConversationID
			summary.LatestToolName = step.ToolName
			summary.LatestStatus = step.Status
		}

		sessionKey := telegramAgentLogSessionKey(step.ChatID, step.ConversationID)
		session := sessionMap[sessionKey]
		if session == nil {
			session = &telegramAgentSessionSummary{
				ChatID:         step.ChatID,
				ConversationID: step.ConversationID,
				LatestAt:       step.CreatedAt,
				LatestToolName: step.ToolName,
				LatestStatus:   step.Status,
			}
			sessionMap[sessionKey] = session
			sessionOrder = append(sessionOrder, sessionKey)
		}
		session.TotalSteps++
		addTelegramAgentStatusCount(step.Status, &session.Executing, &session.Completed, &session.Executed, &session.Pending, &session.Failed, &session.Cancelled)
	}

	summary.ActiveChats = len(sessionMap)
	sessions := make([]telegramAgentSessionSummary, 0, len(sessionOrder))
	for _, key := range sessionOrder {
		sessions = append(sessions, *sessionMap[key])
	}
	return summary, sessions
}

func telegramAgentLogSessionKey(chatID int64, conversationID string) string {
	return strconv.FormatInt(chatID, 10) + ":" + conversationID
}

func addTelegramAgentStatusCount(status string, executing *int, completed *int, executed *int, pending *int, failed *int, cancelled *int) {
	switch status {
	case "executing":
		*executing = *executing + 1
	case "completed":
		*completed = *completed + 1
	case "executed":
		*executed = *executed + 1
	case "pending_confirmation":
		*pending = *pending + 1
	case "failed":
		*failed = *failed + 1
	case "cancelled":
		*cancelled = *cancelled + 1
	}
}

func formatTelegramAgentLogTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func formatTelegramAgentLogTimePtr(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	formatted := value.Format(time.RFC3339)
	return &formatted
}
