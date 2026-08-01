package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func GetModels(c *gin.Context) {
	params, err := common.ParsePaginationWithDefaults(c, 1, 10)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	query := models.DB.Model(&models.Model{})

	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ?", like)
	}

	if strategy := strings.TrimSpace(c.Query("strategy")); strategy != "" {
		switch strategy {
		case consts.BalancerLottery, consts.BalancerRotor:
			query = query.Where("strategy = ?", strategy)
		default:
			common.BadRequest(c, "invalid strategy filter")
			return
		}
	}

	if ioLog := strings.TrimSpace(c.Query("io_log")); ioLog != "" {
		switch ioLog {
		case "true", "false":
			query = query.Where("io_log = ?", ioLog == "true")
		default:
			common.BadRequest(c, "invalid io_log filter")
			return
		}
	}

	if capability := strings.TrimSpace(c.Query("capability")); capability != "" {
		capability = strings.ToLower(capability)
		switch capability {
		case "chat", "vision", "embedding", "rerank":
			query = query.Where("capabilities LIKE ?", fmt.Sprintf("%%\"%s\"%%", capability))
		default:
			common.BadRequest(c, "invalid capability filter")
			return
		}
	}

	list := make([]models.Model, 0)
	total, err := common.PaginateQuery(
		query.Order("LOWER(name) ASC").Order("id ASC"),
		params,
		&list,
	)
	if err != nil {
		common.InternalServerError(c, "Failed to query models: "+err.Error())
		return
	}

	names := make([]string, 0, len(list))
	for _, model := range list {
		name := strings.ToLower(strings.TrimSpace(model.Name))
		if name != "" {
			names = append(names, name)
		}
	}

	priceMap := make(map[string]models.ModelPrice)
	if len(names) > 0 {
		prices := make([]models.ModelPrice, 0, len(names))
		if err := models.DB.WithContext(c.Request.Context()).
			Where("model_id IN ?", names).
			Find(&prices).Error; err != nil {
			common.InternalServerError(c, "Failed to query model prices: "+err.Error())
			return
		}
		for _, price := range prices {
			priceMap[price.ModelID] = price
		}
	}

	withPrices := make([]ModelWithPrice, 0, len(list))
	for _, model := range list {
		key := strings.ToLower(strings.TrimSpace(model.Name))
		var inputPrice *float64
		var outputPrice *float64
		var cacheReadPrice *float64
		var cacheWritePrice *float64
		if price, ok := priceMap[key]; ok {
			input := price.Input
			output := price.Output
			cacheRead := price.CacheRead
			cacheWrite := price.CacheWrite
			inputPrice = &input
			outputPrice = &output
			cacheReadPrice = &cacheRead
			cacheWritePrice = &cacheWrite
		}
		withPrices = append(withPrices, ModelWithPrice{
			Model:           model,
			InputPrice:      inputPrice,
			OutputPrice:     outputPrice,
			CacheReadPrice:  cacheReadPrice,
			CacheWritePrice: cacheWritePrice,
		})
	}

	response := common.NewPaginationResponse(withPrices, total, params)
	common.Success(c, response)
}

// GetModelList 返回所有模型列表用于下拉选择等场景
func GetModelList(c *gin.Context) {
	modelsList, err := gorm.G[models.Model](models.DB).
		Order("LOWER(name) ASC").
		Order("id ASC").
		Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to get models: "+err.Error())
		return
	}

	common.Success(c, modelsList)
}

func validateFallbackModelID(ctx context.Context, currentModelID uint, fallbackModelID uint) error {
	if fallbackModelID == 0 {
		return nil
	}
	if currentModelID > 0 && fallbackModelID == currentModelID {
		return errors.New("回退模型不能选择当前模型自身")
	}
	if _, err := gorm.G[models.Model](models.DB).Where("id = ?", fallbackModelID).First(ctx); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("回退模型不存在")
		}
		return err
	}
	return nil
}

// CreateModel 创建模型
func CreateModel(c *gin.Context) {
	var req ModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if model exists
	count, err := gorm.G[models.Model](models.DB).Where("name = ?", req.Name).Count(c.Request.Context(), "id")
	if err != nil {
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}
	if count > 0 {
		common.BadRequest(c, fmt.Sprintf("Model: %s already exists", req.Name))
		return
	}
	if err := validateFallbackModelID(c.Request.Context(), 0, req.FallbackModelID); err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = consts.BalancerDefault
	}

	ioLog := 0
	if req.IOLog {
		ioLog = 1
	}
	breaker := 0
	if req.Breaker {
		breaker = 1
	}
	capabilities := normalizeModelCapabilities(req.Capabilities)
	if len(capabilities) == 0 {
		capabilities = []string{"chat"}
	}

	model := models.Model{
		Name:            req.Name,
		Remark:          req.Remark,
		MaxRetry:        req.MaxRetry,
		TimeOut:         req.TimeOut,
		IOLog:           ioLog,
		Strategy:        strategy,
		Breaker:         breaker,
		Status:          1,
		FallbackModelID: req.FallbackModelID,
		Capabilities:    models.ModelCapabilities(capabilities),
	}

	if err := gorm.G[models.Model](models.DB).Create(c.Request.Context(), &model); err != nil {
		common.InternalServerError(c, "Failed to create model: "+err.Error())
		return
	}

	if err := upsertModelPrice(c.Request.Context(), model.Name, req.InputPrice, req.OutputPrice, req.CacheRead, req.CacheWrite); err != nil {
		common.InternalServerError(c, "Failed to save model price: "+err.Error())
		return
	}

	common.Success(c, model)
}

// UpdateModel 更新模型
func UpdateModel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	// Check if model exists
	currentModel, err := gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}
	if err := validateFallbackModelID(c.Request.Context(), currentModel.ID, req.FallbackModelID); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	strategy := req.Strategy
	if strategy == "" {
		strategy = consts.BalancerDefault
	}

	// Update fields
	ioLog := 0
	if req.IOLog {
		ioLog = 1
	}
	breaker := 0
	if req.Breaker {
		breaker = 1
	}
	capabilities := normalizeModelCapabilities(req.Capabilities)
	if len(capabilities) == 0 {
		capabilities = []string{"chat"}
	}
	updates := models.Model{
		Name:            req.Name,
		Remark:          req.Remark,
		MaxRetry:        req.MaxRetry,
		TimeOut:         req.TimeOut,
		IOLog:           ioLog,
		Strategy:        strategy,
		Breaker:         breaker,
		FallbackModelID: req.FallbackModelID,
		Capabilities:    models.ModelCapabilities(capabilities),
	}

	// 使用 map 更新，避免 GORM 忽略 0 值（例如 IOLog 关闭）
	updateMap := map[string]any{
		"name":              updates.Name,
		"remark":            updates.Remark,
		"max_retry":         updates.MaxRetry,
		"time_out":          updates.TimeOut,
		"io_log":            updates.IOLog,
		"strategy":          updates.Strategy,
		"breaker":           updates.Breaker,
		"fallback_model_id": updates.FallbackModelID,
		"capabilities":      updates.Capabilities,
	}
	if err := models.DB.WithContext(c.Request.Context()).Model(&models.Model{}).Where("id = ?", id).Updates(updateMap).Error; err != nil {
		common.InternalServerError(c, "Failed to update model: "+err.Error())
		return
	}

	if err := upsertModelPrice(c.Request.Context(), updates.Name, req.InputPrice, req.OutputPrice, req.CacheRead, req.CacheWrite); err != nil {
		common.InternalServerError(c, "Failed to save model price: "+err.Error())
		return
	}

	// Get updated model
	updatedModel, err := gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model: "+err.Error())
		return
	}

	common.Success(c, updatedModel)
}

// UpdateModelStatus 更新模型启用状态
func UpdateModelStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ModelStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	status := 0
	if req.Status {
		status = 1
	}

	if _, err := gorm.G[models.Model](models.DB).Where("id = ?", id).Update(c.Request.Context(), "status", status); err != nil {
		common.InternalServerError(c, "Failed to update model status: "+err.Error())
		return
	}

	updatedModel, err := gorm.G[models.Model](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated model: "+err.Error())
		return
	}

	common.Success(c, updatedModel)
}

// DeleteModel 删除模型
func DeleteModel(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	result, err := gorm.G[models.Model](models.DB).Where("id = ?", id).Delete(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to delete model: "+err.Error())
		return
	}

	if result == 0 {
		common.NotFound(c, "Model not found")
		return
	}

	common.Success(c, nil)
}

func normalizeModelCapabilities(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case "chat", "vision", "embedding", "rerank":
		default:
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func upsertModelPrice(ctx context.Context, modelName string, input, output, cacheRead, cacheWrite *float64) error {
	if modelName == "" {
		return nil
	}
	if input != nil && *input == 0 {
		input = nil
	}
	if output != nil && *output == 0 {
		output = nil
	}
	if cacheRead != nil && *cacheRead == 0 {
		cacheRead = nil
	}
	if cacheWrite != nil && *cacheWrite == 0 {
		cacheWrite = nil
	}
	if input == nil && output == nil && cacheRead == nil && cacheWrite == nil {
		return nil
	}

	key := strings.ToLower(strings.TrimSpace(modelName))
	if key == "" {
		return nil
	}

	var price models.ModelPrice
	exists := true
	if err := models.DB.WithContext(ctx).Where("model_id = ?", key).First(&price).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			exists = false
		} else {
			return err
		}
	}

	updates := models.ModelPrice{
		ModelID:    key,
		Provider:   price.Provider,
		Input:      price.Input,
		Output:     price.Output,
		CacheRead:  price.CacheRead,
		CacheWrite: price.CacheWrite,
	}
	if input != nil {
		updates.Input = *input
	}
	if output != nil {
		updates.Output = *output
	}
	if cacheRead != nil {
		updates.CacheRead = *cacheRead
	}
	if cacheWrite != nil {
		updates.CacheWrite = *cacheWrite
	}

	if !exists {
		return models.DB.WithContext(ctx).Create(&updates).Error
	}
	return models.DB.WithContext(ctx).Model(&models.ModelPrice{}).Where("model_id = ?", key).Updates(map[string]any{
		"input":       updates.Input,
		"output":      updates.Output,
		"cache_read":  updates.CacheRead,
		"cache_write": updates.CacheWrite,
	}).Error
}
