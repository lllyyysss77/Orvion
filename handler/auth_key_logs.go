package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

type authKeyWrapLog struct {
	models.ChatLog
	KeyName string `json:"key_name"`
}

// GetAuthKeyRequestLogs 获取当前 API Key 对应的请求日志（支持分页与筛选）。
func GetAuthKeyRequestLogs(c *gin.Context) {
	authKeyID, ok := c.Request.Context().Value(consts.ContextKeyAuthKeyID).(uint)
	if !ok || authKeyID == 0 {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden, "auth key required")
		return
	}

	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	name := strings.TrimSpace(c.Query("name"))
	status := strings.TrimSpace(c.Query("status"))
	style := strings.TrimSpace(c.Query("style"))
	startAtRaw := strings.TrimSpace(c.Query("start_at"))
	endAtRaw := strings.TrimSpace(c.Query("end_at"))

	var startAt *time.Time
	var endAt *time.Time
	if startAtRaw != "" {
		parsed, parseErr := parseAuthKeyLogQueryTime(startAtRaw)
		if parseErr != nil {
			common.BadRequest(c, "Invalid start_at format")
			return
		}
		startAt = &parsed
	}
	if endAtRaw != "" {
		parsed, parseErr := parseAuthKeyLogQueryTime(endAtRaw)
		if parseErr != nil {
			common.BadRequest(c, "Invalid end_at format")
			return
		}
		endAt = &parsed
	}

	clauses := []string{"auth_key_id = ?"}
	args := []any{authKeyID}
	if name != "" {
		clauses = append(clauses, "name = ?")
		args = append(args, name)
	}
	if status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if style != "" {
		clauses = append(clauses, "style = ?")
		args = append(args, style)
	}
	if startAt != nil {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, *startAt)
	}
	if endAt != nil {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, *endAt)
	}

	whereSQL := strings.Join(clauses, " AND ")
	offset := (params.Page - 1) * params.PageSize
	logs, total, err := models.QueryChatLogsPage(
		c.Request.Context(),
		models.ChatLogQueryScope{StartAt: startAt, EndAt: endAt},
		models.ChatLogColumnsSQL(),
		whereSQL,
		"created_at DESC, id DESC",
		params.PageSize,
		offset,
		args...,
	)
	if err != nil {
		common.InternalServerError(c, "Failed to query logs: "+err.Error())
		return
	}

	authKey, err := gorm.G[models.AuthKey](models.DB).
		Select("id", "name").
		Where("id = ?", authKeyID).
		First(c.Request.Context())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "auth key not found")
			return
		}
		common.InternalServerError(c, "Failed to query auth key: "+err.Error())
		return
	}

	wrapLogs := make([]authKeyWrapLog, 0, len(logs))
	for _, log := range logs {
		log.ProviderName = ""
		log.ProviderModel = ""
		wrapLogs = append(wrapLogs, authKeyWrapLog{
			ChatLog: log,
			KeyName: authKey.Name,
		})
	}

	response := common.NewPaginationResponse(wrapLogs, total, params)
	common.Success(c, response)
}

// GetAuthKeyChatIO 查询当前 API Key 对应日志的输入输出记录。
func GetAuthKeyChatIO(c *gin.Context) {
	authKeyID, ok := c.Request.Context().Value(consts.ContextKeyAuthKeyID).(uint)
	if !ok || authKeyID == 0 {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden, "auth key required")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		common.BadRequest(c, "Invalid log ID")
		return
	}

	clauses := []string{"auth_key_id = ?", "uuid = ?"}
	args := []any{authKeyID, id}
	if numericID, parseErr := strconv.ParseUint(id, 10, 64); parseErr == nil {
		clauses = []string{"auth_key_id = ?", "(uuid = ? OR id = ?)"}
		args = []any{authKeyID, id, numericID}
	}
	logFound, err := models.QueryChatLogExists(c.Request.Context(), models.ChatLogQueryScope{}, "id, uuid, auth_key_id", strings.Join(clauses, " AND "), args...)
	if err != nil {
		common.InternalServerError(c, "Failed to verify log ownership: "+err.Error())
		return
	}
	if !logFound {
		common.NotFound(c, "chat io not found")
		return
	}

	query := gorm.G[models.ChatIO](models.DB).Where("log_uuid = ?", id)
	if _, parseErr := strconv.ParseUint(id, 10, 64); parseErr == nil {
		query = gorm.G[models.ChatIO](models.DB).Where("log_uuid = ? OR log_id = ?", id, id)
	}
	chatIO, err := query.First(c.Request.Context())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "chat io not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve chat io: "+err.Error())
		return
	}

	common.Success(c, chatIO)
}

func parseAuthKeyLogQueryTime(raw string) (time.Time, error) {
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
