package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

type WrapLog struct {
	models.ChatLog
	KeyName string `json:"key_name"`
}

type chatLogQueryFilter struct {
	ProviderName string
	Name         string
	Status       string
	Style        string
	AuthKeyID    string
	StartAt      *time.Time
	EndAt        *time.Time
}

// GetRequestLogs 获取最近的请求日志（支持分页和筛选）
func GetRequestLogs(c *gin.Context) {
	// 解析分页参数
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	// 获取筛选参数
	providerName := c.Query("provider_name")
	name := c.Query("name")
	status := c.Query("status")
	style := c.Query("style")
	authKeyID := c.Query("auth_key_id")
	startAtRaw := strings.TrimSpace(c.Query("start_at"))
	endAtRaw := strings.TrimSpace(c.Query("end_at"))

	filter := chatLogQueryFilter{
		ProviderName: providerName,
		Name:         name,
		Status:       status,
		Style:        style,
		AuthKeyID:    authKeyID,
	}
	if startAtRaw != "" {
		startAt, err := parseLogQueryTime(startAtRaw)
		if err != nil {
			common.BadRequest(c, "Invalid start_at format")
			return
		}
		filter.StartAt = &startAt
	}
	if endAtRaw != "" {
		endAt, err := parseLogQueryTime(endAtRaw)
		if err != nil {
			common.BadRequest(c, "Invalid end_at format")
			return
		}
		filter.EndAt = &endAt
	}

	logs, total, err := queryRequestLogsByMonthlyTables(c.Request.Context(), params, filter)
	if err != nil {
		common.InternalServerError(c, "Failed to query logs: "+err.Error())
		return
	}

	keys, err := gorm.G[models.AuthKey](models.DB).Where("id IN ?", lo.Map(logs, func(log models.ChatLog, _ int) uint { return log.AuthKeyID })).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	keyMap := lo.KeyBy(keys, func(key models.AuthKey) uint { return key.ID })

	wrapLogs := make([]WrapLog, 0)
	for _, log := range logs {
		var keyName string
		if key, ok := keyMap[log.AuthKeyID]; ok {
			keyName = key.Name
		}
		if log.AuthKeyID == 0 {
			keyName = "管理员"
		}
		wrapLogs = append(wrapLogs, WrapLog{
			ChatLog: log,
			KeyName: keyName,
		})
	}

	// 返回分页响应
	response := common.NewPaginationResponse(wrapLogs, total, params)
	common.Success(c, response)
}

func parseLogQueryTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time format")
}

func queryRequestLogsByMonthlyTables(ctx context.Context, params common.PaginationParams, filter chatLogQueryFilter) ([]models.ChatLog, int64, error) {
	columns := models.ChatLogColumnsSQL()
	filterSQL, filterArgs := buildChatLogFilterSQL(filter)
	offset := (params.Page - 1) * params.PageSize
	return models.QueryChatLogListPage(
		ctx,
		models.ChatLogQueryScope{StartAt: filter.StartAt, EndAt: filter.EndAt},
		columns,
		filterSQL,
		"created_at DESC, id DESC",
		params.PageSize,
		offset,
		filterArgs...,
	)
}

func buildChatLogFilterSQL(filter chatLogQueryFilter) (string, []any) {
	clauses := make([]string, 0, 7)
	args := make([]any, 0, 7)
	if filter.ProviderName != "" {
		clauses = append(clauses, "provider_name = ?")
		args = append(args, filter.ProviderName)
	}
	if filter.Name != "" {
		clauses = append(clauses, "name = ?")
		args = append(args, filter.Name)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.Style != "" {
		clauses = append(clauses, "style = ?")
		args = append(args, filter.Style)
	}
	if filter.AuthKeyID != "" {
		clauses = append(clauses, "auth_key_id = ?")
		args = append(args, filter.AuthKeyID)
	}
	if filter.StartAt != nil {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, *filter.StartAt)
	}
	if filter.EndAt != nil {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, *filter.EndAt)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return strings.Join(clauses, " AND "), args
}

// GetChatIO 查询指定日志的输入输出记录
func GetChatIO(c *gin.Context) {
	id := c.Param("id")

	query := gorm.G[models.ChatIO](models.DB).Where("log_uuid = ?", id)
	if _, parseErr := strconv.ParseUint(strings.TrimSpace(id), 10, 64); parseErr == nil {
		query = gorm.G[models.ChatIO](models.DB).Where("log_uuid = ? OR log_id = ?", id, id)
	}
	chatIO, err := query.First(c.Request.Context())
	if err != nil {
		common.NotFound(c, "ChatIO not found")
		return
	}

	mode := strings.ToLower(strings.TrimSpace(c.Query("mode")))
	if mode == "" || mode == "full" {
		common.Success(c, chatIO)
		return
	}

	inputLimit := parsePositiveInt(c.Query("input_limit"), 20000)
	outputLimit := parsePositiveInt(c.Query("output_limit"), 20000)
	outputItemsLimit := parsePositiveInt(c.Query("output_items_limit"), 50)

	summary := buildChatIOSummary(chatIO, inputLimit, outputLimit, outputItemsLimit)
	common.Success(c, summary)
}

type chatIOSummary struct {
	ID                   uint     `json:"ID"`
	LogId                uint     `json:"LogId"`
	Input                string   `json:"Input"`
	OutputString         string   `json:"OutputString,omitempty"`
	OutputStringArray    []string `json:"OutputStringArray,omitempty"`
	InputBytes           int      `json:"input_bytes"`
	OutputBytes          int      `json:"output_bytes"`
	OutputItems          int      `json:"output_items"`
	Summary              bool     `json:"summary"`
	TruncatedInput       bool     `json:"truncated_input"`
	TruncatedOutput      bool     `json:"truncated_output"`
	TruncatedOutputItems bool     `json:"truncated_output_items"`
}

func buildChatIOSummary(chatIO models.ChatIO, inputLimit, outputLimit, outputItemsLimit int) chatIOSummary {
	input, inputTruncated := truncateString(chatIO.Input, inputLimit)
	outputBytes := len(chatIO.OutputString)
	outputItems := 0
	outputTruncated := false
	outputItemsTruncated := false
	outputString := ""
	var outputArray []string

	parsed, totalItems, itemsTruncated := parseChatIOStringArray(chatIO.OutputStringArray, outputItemsLimit)
	if len(parsed) > 0 {
		outputItems = totalItems
		outputItemsTruncated = itemsTruncated
		outputArray = make([]string, 0, len(parsed))
		for _, entry := range parsed {
			truncatedEntry, truncated := truncateString(entry, outputLimit)
			if truncated {
				outputTruncated = true
			}
			outputArray = append(outputArray, truncatedEntry)
		}
		outputBytes = len(chatIO.OutputStringArray)
	} else if chatIO.OutputString != "" {
		outputItems = 1
		outputString, outputTruncated = truncateString(chatIO.OutputString, outputLimit)
	}

	return chatIOSummary{
		ID:                   chatIO.ID,
		LogId:                chatIO.LogId,
		Input:                input,
		OutputString:         outputString,
		OutputStringArray:    outputArray,
		InputBytes:           len(chatIO.Input),
		OutputBytes:          outputBytes,
		OutputItems:          outputItems,
		Summary:              true,
		TruncatedInput:       inputTruncated,
		TruncatedOutput:      outputTruncated,
		TruncatedOutputItems: outputItemsTruncated,
	}
}

func parseChatIOStringArray(raw string, limit int) ([]string, int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, 0, false
	}
	if limit <= 0 {
		limit = 1
	}
	const maxParseBytes = 2 * 1024 * 1024
	if len(trimmed) > maxParseBytes {
		return []string{trimmed}, 1, len(trimmed) > maxParseBytes
	}
	var parsed []string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return []string{trimmed}, 1, false
	}
	if len(parsed) <= limit {
		return parsed, len(parsed), false
	}
	return parsed[:limit], len(parsed), true
}

func truncateString(raw string, limit int) (string, bool) {
	if limit <= 0 {
		return raw, false
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw, false
	}
	return string(runes[:limit]) + "\n...(已截断)", true
}

func parsePositiveInt(raw string, defaultValue int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return defaultValue
	}
	return value
}

// GetUserAgents 获取所有不重复的用户代理种类
func GetUserAgents(c *gin.Context) {
	userAgents, err := models.QueryChatLogDistinctUserAgents(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to query user agents: "+err.Error())
		return
	}

	common.Success(c, userAgents)
}

// CleanLogsRequest 清理日志请求
type CleanLogsRequest struct {
	Type  string `json:"type"`  // "count" 或 "days"
	Value int    `json:"value"` // 数量或天数
}

// CleanLogs 清理日志
func CleanLogs(c *gin.Context) {
	var req CleanLogsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if req.Value <= 0 {
		common.BadRequest(c, "Value must be greater than 0")
		return
	}

	var deletedCount int64

	switch req.Type {
	case "count":
		logs, err := fetchLogsForCleanup(c.Request.Context(), req.Type, req.Value)
		if err != nil {
			common.InternalServerError(c, "Failed to query logs: "+err.Error())
			return
		}
		if len(logs) == 0 {
			common.Success(c, map[string]any{"deleted_count": 0})
			return
		}

		if err := deleteLogsByRefs(c.Request.Context(), logs); err != nil {
			common.InternalServerError(c, "Failed to delete monthly logs: "+err.Error())
			return
		}
		deletedCount = int64(len(logs))
		if err := deleteChatIOByLogRefs(c.Request.Context(), logs); err != nil {
			common.InternalServerError(c, "Failed to delete chat IO: "+err.Error())
			return
		}

	case "days":
		logs, err := fetchLogsForCleanup(c.Request.Context(), req.Type, req.Value)
		if err != nil {
			common.InternalServerError(c, "Failed to query logs: "+err.Error())
			return
		}
		if len(logs) == 0 {
			common.Success(c, map[string]any{"deleted_count": 0})
			return
		}
		if err := deleteLogsByRefs(c.Request.Context(), logs); err != nil {
			common.InternalServerError(c, "Failed to delete monthly logs: "+err.Error())
			return
		}
		if err := deleteChatIOByLogRefs(c.Request.Context(), logs); err != nil {
			common.InternalServerError(c, "Failed to delete chat IO: "+err.Error())
			return
		}
		deletedCount = int64(len(logs))

	default:
		common.BadRequest(c, "Invalid type: must be 'count' or 'days'")
		return
	}

	common.Success(c, map[string]any{"deleted_count": deletedCount})
}

type cleanupLogRef = models.ChatLogCleanupRefRow

const logCleanupDeleteBatchSize = 500

func fetchLogsForCleanup(ctx context.Context, cleanType string, value int) ([]cleanupLogRef, error) {
	now := time.Now()
	switch cleanType {
	case "count":
		return models.QueryChatLogCleanupRefsByCount(ctx, value)
	case "days":
		cutoff := now.AddDate(0, 0, -value)
		return models.QueryChatLogCleanupRefsBefore(ctx, cutoff)
	default:
		return nil, nil
	}
}

func deleteLogsByRefs(ctx context.Context, refs []cleanupLogRef) error {
	uuidsByTable := make(map[string][]string)
	for _, ref := range refs {
		if ref.UUID == "" {
			continue
		}
		tableName := models.ChatLogMonthlyTableName(ref.CreatedAt)
		if strings.TrimSpace(ref.TableName) != "" {
			tableName = ref.TableName
		}
		if !models.IsChatLogMonthlyTableName(tableName) {
			continue
		}
		uuidsByTable[tableName] = append(uuidsByTable[tableName], ref.UUID)
	}

	for tableName, uuids := range uuidsByTable {
		for start := 0; start < len(uuids); start += logCleanupDeleteBatchSize {
			end := start + logCleanupDeleteBatchSize
			if end > len(uuids) {
				end = len(uuids)
			}
			if err := models.DB.WithContext(ctx).
				Table(tableName).
				Where("uuid IN ?", uuids[start:end]).
				Delete(&models.ChatLog{}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteChatIOByLogRefs(ctx context.Context, refs []cleanupLogRef) error {
	uuids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.UUID != "" {
			uuids = append(uuids, ref.UUID)
		}
	}
	if len(uuids) == 0 {
		return nil
	}
	for start := 0; start < len(uuids); start += logCleanupDeleteBatchSize {
		end := start + logCleanupDeleteBatchSize
		if end > len(uuids) {
			end = len(uuids)
		}
		if err := models.DB.WithContext(ctx).
			Where("log_uuid IN ?", uuids[start:end]).
			Delete(&models.ChatIO{}).Error; err != nil {
			return err
		}
	}
	return nil
}
