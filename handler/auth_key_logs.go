package handler

import (
	"errors"
	"fmt"
	"net/http"
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

	providerName := strings.TrimSpace(c.Query("provider_name"))
	name := strings.TrimSpace(c.Query("name"))
	status := strings.TrimSpace(c.Query("status"))
	style := strings.TrimSpace(c.Query("style"))
	startAtRaw := strings.TrimSpace(c.Query("start_at"))
	endAtRaw := strings.TrimSpace(c.Query("end_at"))

	query := models.DB.Model(&models.ChatLog{}).Where("auth_key_id = ?", authKeyID)
	if providerName != "" {
		query = query.Where("provider_name = ?", providerName)
	}
	if name != "" {
		query = query.Where("name = ?", name)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if style != "" {
		query = query.Where("style = ?", style)
	}
	if startAtRaw != "" {
		startAt, parseErr := parseAuthKeyLogQueryTime(startAtRaw)
		if parseErr != nil {
			common.BadRequest(c, "Invalid start_at format")
			return
		}
		query = query.Where("created_at >= ?", startAt)
	}
	if endAtRaw != "" {
		endAt, parseErr := parseAuthKeyLogQueryTime(endAtRaw)
		if parseErr != nil {
			common.BadRequest(c, "Invalid end_at format")
			return
		}
		query = query.Where("created_at <= ?", endAt)
	}

	logs := make([]models.ChatLog, 0)
	total, err := common.PaginateQuery(query.Order("id DESC"), params, &logs)
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

	logRecord, err := gorm.G[models.ChatLog](models.DB).
		Select("id", "auth_key_id").
		Where("id = ? AND auth_key_id = ?", id, authKeyID).
		First(c.Request.Context())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "log not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve log: "+err.Error())
		return
	}

	chatIO, err := gorm.G[models.ChatIO](models.DB).
		Where("log_id = ?", logRecord.ID).
		First(c.Request.Context())
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
