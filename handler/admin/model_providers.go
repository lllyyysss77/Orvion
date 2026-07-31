package admin

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

// GetModelProviders 获取模型的提供商关联列表
func GetModelProviders(c *gin.Context) {
	modelIDStr := c.Query("model_id")
	if modelIDStr == "" {
		common.BadRequest(c, "model_id query parameter is required")
		return
	}

	modelID, err := strconv.ParseUint(modelIDStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid model_id format")
		return
	}

	modelProviders, err := gorm.G[models.ModelWithProvider](models.DB).Where("model_id = ?", modelID).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	common.Success(c, modelProviders)
}

// GetModelProviderStatus 获取提供商状态信息
func GetModelProviderStatus(c *gin.Context) {
	providerIDStr := c.Query("provider_id")
	modelName := c.Query("model_name")
	providerModel := c.Query("provider_model")

	if providerIDStr == "" || modelName == "" || providerModel == "" {
		common.BadRequest(c, "provider_id, model_name and provider_model query parameters are required")
		return
	}

	providerID, err := strconv.ParseUint(providerIDStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid provider_id format")
		return
	}

	// 获取提供商信息
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", providerID).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve provider: "+err.Error())
		return
	}

	// 获取最近10次请求状态
	logs := make([]models.ChatLog, 0, 10)
	logs, _, err = models.QueryChatLogsPage(
		c.Request.Context(),
		models.ChatLogQueryScope{},
		"created_at, provider_name, provider_model, name, status",
		"provider_name = ? AND provider_model = ? AND name = ?",
		"created_at DESC",
		10,
		0,
		provider.Name,
		providerModel,
		modelName,
	)
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve chat log: "+err.Error())
		return
	}

	status := make([]bool, 0)
	for _, log := range logs {
		status = append(status, log.Status == "success")
	}
	slices.Reverse(status)
	common.Success(c, status)
}

// CreateModelProvider 创建模型提供商关联
func CreateModelProvider(c *gin.Context) {
	var req ModelWithProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// 将 CustomerHeaders 转换为 JSON 字符串
	customerHeadersJSON := ""
	if req.CustomerHeaders != nil && len(req.CustomerHeaders) > 0 {
		if jsonBytes, err := json.Marshal(req.CustomerHeaders); err == nil {
			customerHeadersJSON = string(jsonBytes)
		}
	}

	// 将 bool 转换为 int (0/1)
	withHeader := 0
	if req.WithHeader {
		withHeader = 1
	}

	modelProvider := models.ModelWithProvider{
		ModelID:         req.ModelID,
		ProviderModel:   req.ProviderModel,
		ProviderID:      req.ProviderID,
		WithHeader:      withHeader,
		CustomerHeaders: customerHeadersJSON,
		Weight:          req.Weight,
		Status:          1, // 默认启用
	}

	err := gorm.G[models.ModelWithProvider](models.DB).Create(c.Request.Context(), &modelProvider)
	if err != nil {
		common.InternalServerError(c, "Failed to create model-provider association: "+err.Error())
		return
	}

	common.Success(c, modelProvider)
}

// UpdateModelProvider 更新模型提供商关联
func UpdateModelProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelWithProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	slog.Info("UpdateModelProvider")

	// 将 CustomerHeaders 转换为 JSON 字符串
	customerHeadersJSON := ""
	if req.CustomerHeaders != nil && len(req.CustomerHeaders) > 0 {
		if jsonBytes, err := json.Marshal(req.CustomerHeaders); err == nil {
			customerHeadersJSON = string(jsonBytes)
		}
	}

	// 将 bool 转换为 int (0/1)
	withHeader := 0
	if req.WithHeader {
		withHeader = 1
	}

	// Check if model-provider association exists
	_, err = gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model-provider association not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	updateMap := map[string]any{
		"model_id":         req.ModelID,
		"provider_id":      req.ProviderID,
		"provider_model":   req.ProviderModel,
		"with_header":      withHeader,
		"customer_headers": customerHeadersJSON,
		"weight":           req.Weight,
	}
	if err := models.DB.WithContext(c.Request.Context()).
		Model(&models.ModelWithProvider{}).
		Where("id = ?", id).
		Updates(updateMap).Error; err != nil {
		common.InternalServerError(c, "Failed to update model-provider association: "+err.Error())
		return
	}

	// Get updated model-provider association
	updatedModelProvider, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model-provider association: "+err.Error())
		return
	}

	common.Success(c, updatedModelProvider)
}

// UpdateModelProviderStatus 切换模型提供商关联启用状态
func UpdateModelProviderStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelProviderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	existing, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model-provider association not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve model-provider association: "+err.Error())
		return
	}

	var status int
	if req.Status {
		status = 1
	} else {
		status = 0
	}
	if err := models.DB.WithContext(c.Request.Context()).
		Model(&models.ModelWithProvider{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":              status,
			"auto_disabled_until": nil,
		}).Error; err != nil {
		common.InternalServerError(c, "Failed to update status: "+err.Error())
		return
	}

	existing.Status = status
	existing.AutoDisabledUntil = nil
	common.Success(c, existing)
}

// DeleteModelProvider 删除模型提供商关联
func DeleteModelProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete model-provider association: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Model-provider association not found")
		return
	}

	common.Success(c, nil)
}
