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

type telegramAgentScheduledTaskPayload struct {
	Name               string `json:"name"`
	Prompt             string `json:"prompt"`
	Enabled            *bool  `json:"enabled"`
	ScheduleType       string `json:"schedule_type"`
	IntervalMinutes    int    `json:"interval_minutes"`
	TimeOfDay          string `json:"time_of_day"`
	Timezone           string `json:"timezone"`
	PushToConversation *bool  `json:"push_to_conversation"`
	ChatID             int64  `json:"chat_id"`
}

type telegramAgentScheduledTaskStatusPayload struct {
	Enabled bool `json:"enabled"`
}

type telegramAgentScheduledTaskResponse struct {
	ID                 uint    `json:"id"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	Name               string  `json:"name"`
	Prompt             string  `json:"prompt"`
	Enabled            bool    `json:"enabled"`
	ScheduleType       string  `json:"schedule_type"`
	IntervalMinutes    int     `json:"interval_minutes"`
	TimeOfDay          string  `json:"time_of_day"`
	Timezone           string  `json:"timezone"`
	PushToConversation bool    `json:"push_to_conversation"`
	ChatID             int64   `json:"chat_id"`
	Running            bool    `json:"running"`
	NextRunAt          *string `json:"next_run_at,omitempty"`
	LastRunAt          *string `json:"last_run_at,omitempty"`
	LastStatus         string  `json:"last_status"`
	LastResult         string  `json:"last_result"`
	LastError          string  `json:"last_error"`
	RunCount           int64   `json:"run_count"`
	FailureCount       int64   `json:"failure_count"`
}

func GetTelegramAgentScheduledTasks(c *gin.Context) {
	if models.DB == nil {
		common.InternalServerError(c, "数据库未初始化")
		return
	}

	var rows []models.TelegramAgentScheduledTask
	if err := models.DB.WithContext(c.Request.Context()).
		Order("enabled DESC").
		Order("next_run_at ASC").
		Order("id DESC").
		Find(&rows).Error; err != nil {
		common.InternalServerError(c, "读取 TG Agent 定时任务失败: "+err.Error())
		return
	}

	result := make([]telegramAgentScheduledTaskResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, telegramAgentScheduledTaskResponseFromModel(row))
	}
	common.Success(c, result)
}

func CreateTelegramAgentScheduledTask(c *gin.Context) {
	if models.DB == nil {
		common.InternalServerError(c, "数据库未初始化")
		return
	}

	var req telegramAgentScheduledTaskPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "请求参数无效: "+err.Error())
		return
	}

	task := models.TelegramAgentScheduledTask{
		Enabled:            1,
		ScheduleType:       agent.TelegramAgentScheduleTypeInterval,
		IntervalMinutes:    60,
		Timezone:           "Local",
		PushToConversation: 0,
	}
	applyTelegramAgentScheduledTaskPayload(&task, req)
	if err := agent.NormalizeTelegramAgentScheduledTaskForSave(&task, time.Now(), true); err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	if err := models.DB.WithContext(c.Request.Context()).Create(&task).Error; err != nil {
		common.InternalServerError(c, "创建 TG Agent 定时任务失败: "+err.Error())
		return
	}
	common.Success(c, telegramAgentScheduledTaskResponseFromModel(task))
}

func UpdateTelegramAgentScheduledTask(c *gin.Context) {
	task, ok := loadTelegramAgentScheduledTaskByID(c)
	if !ok {
		return
	}

	var req telegramAgentScheduledTaskPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "请求参数无效: "+err.Error())
		return
	}

	applyTelegramAgentScheduledTaskPayload(&task, req)
	if err := agent.NormalizeTelegramAgentScheduledTaskForSave(&task, time.Now(), true); err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	task.Running = 0
	if err := models.DB.WithContext(c.Request.Context()).Save(&task).Error; err != nil {
		common.InternalServerError(c, "更新 TG Agent 定时任务失败: "+err.Error())
		return
	}
	common.Success(c, telegramAgentScheduledTaskResponseFromModel(task))
}

func UpdateTelegramAgentScheduledTaskStatus(c *gin.Context) {
	task, ok := loadTelegramAgentScheduledTaskByID(c)
	if !ok {
		return
	}

	var req telegramAgentScheduledTaskStatusPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "请求参数无效: "+err.Error())
		return
	}

	task.Enabled = boolToIntForTelegramAgentSchedule(req.Enabled)
	task.Running = 0
	if req.Enabled {
		if err := agent.NormalizeTelegramAgentScheduledTaskForSave(&task, time.Now(), true); err != nil {
			common.BadRequest(c, err.Error())
			return
		}
	}
	if err := models.DB.WithContext(c.Request.Context()).Save(&task).Error; err != nil {
		common.InternalServerError(c, "更新 TG Agent 定时任务状态失败: "+err.Error())
		return
	}
	common.Success(c, telegramAgentScheduledTaskResponseFromModel(task))
}

func DeleteTelegramAgentScheduledTask(c *gin.Context) {
	task, ok := loadTelegramAgentScheduledTaskByID(c)
	if !ok {
		return
	}
	if err := models.DB.WithContext(c.Request.Context()).Delete(&task).Error; err != nil {
		common.InternalServerError(c, "删除 TG Agent 定时任务失败: "+err.Error())
		return
	}
	common.Success(c, gin.H{"id": task.ID})
}

func loadTelegramAgentScheduledTaskByID(c *gin.Context) (models.TelegramAgentScheduledTask, bool) {
	var task models.TelegramAgentScheduledTask
	if models.DB == nil {
		common.InternalServerError(c, "数据库未初始化")
		return task, false
	}

	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 {
		common.BadRequest(c, "任务 ID 无效")
		return task, false
	}
	err = models.DB.WithContext(c.Request.Context()).Where("id = ?", uint(id)).First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "未找到 TG Agent 定时任务")
			return task, false
		}
		common.InternalServerError(c, "读取 TG Agent 定时任务失败: "+err.Error())
		return task, false
	}
	return task, true
}

func applyTelegramAgentScheduledTaskPayload(task *models.TelegramAgentScheduledTask, req telegramAgentScheduledTaskPayload) {
	task.Name = req.Name
	task.Prompt = req.Prompt
	task.ScheduleType = req.ScheduleType
	task.IntervalMinutes = req.IntervalMinutes
	task.TimeOfDay = req.TimeOfDay
	task.Timezone = req.Timezone
	task.ChatID = req.ChatID
	if req.Enabled != nil {
		task.Enabled = boolToIntForTelegramAgentSchedule(*req.Enabled)
	}
	if req.PushToConversation != nil {
		task.PushToConversation = boolToIntForTelegramAgentSchedule(*req.PushToConversation)
	}
}

func telegramAgentScheduledTaskResponseFromModel(row models.TelegramAgentScheduledTask) telegramAgentScheduledTaskResponse {
	return telegramAgentScheduledTaskResponse{
		ID:                 row.ID,
		CreatedAt:          formatTelegramAgentScheduleTime(row.CreatedAt),
		UpdatedAt:          formatTelegramAgentScheduleTime(row.UpdatedAt),
		Name:               row.Name,
		Prompt:             row.Prompt,
		Enabled:            row.Enabled == 1,
		ScheduleType:       row.ScheduleType,
		IntervalMinutes:    row.IntervalMinutes,
		TimeOfDay:          row.TimeOfDay,
		Timezone:           row.Timezone,
		PushToConversation: row.PushToConversation == 1,
		ChatID:             row.ChatID,
		Running:            row.Running == 1,
		NextRunAt:          formatTelegramAgentScheduleTimePtr(row.NextRunAt),
		LastRunAt:          formatTelegramAgentScheduleTimePtr(row.LastRunAt),
		LastStatus:         row.LastStatus,
		LastResult:         row.LastResult,
		LastError:          row.LastError,
		RunCount:           row.RunCount,
		FailureCount:       row.FailureCount,
	}
}

func formatTelegramAgentScheduleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func formatTelegramAgentScheduleTimePtr(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	formatted := value.Format(time.RFC3339)
	return &formatted
}

func boolToIntForTelegramAgentSchedule(value bool) int {
	if value {
		return 1
	}
	return 0
}
