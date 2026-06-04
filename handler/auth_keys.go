package handler

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/service"
	"gorm.io/gorm"
)

type AuthKeyRequest struct {
	Name      string   `json:"name" binding:"required"`
	Key       string   `json:"key"`
	Status    *bool    `json:"status"`
	AllowAll  *bool    `json:"allow_all"`
	Models    []string `json:"models"`
	ExpiresAt *string  `json:"expires_at"`
	RpmLimit  *int     `json:"rpm_limit"`
}

// boolPtrToInt 将bool指针转换为int，nil时返回默认值
func boolPtrToInt(b *bool, defaultValue int) int {
	if b == nil {
		return defaultValue
	}
	if *b {
		return 1
	}
	return 0
}

// sanitizeModelsToString 清理模型列表并转换为JSON字符串
func sanitizeModelsToString(modelsList []string) string {
	cleaned := sanitizeModels(modelsList)
	if len(cleaned) == 0 {
		return "[]"
	}
	jsonBytes, err := json.Marshal(cleaned)
	if err != nil {
		return "[]"
	}
	return string(jsonBytes)
}

func GetAuthKeys(c *gin.Context) {
	// 解析分页参数
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	// 构建查询
	query := models.DB.Model(&models.AuthKey{})

	// ID 精确过滤
	if idRaw := strings.TrimSpace(c.Query("id")); idRaw != "" {
		id, parseErr := strconv.ParseUint(idRaw, 10, 64)
		if parseErr != nil {
			common.BadRequest(c, "Invalid id filter")
			return
		}
		query = query.Where("id = ?", id)
	}

	// 搜索过滤
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ? OR key LIKE ?", like, like)
	}

	// 状态过滤
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		switch status {
		case "active":
			query = query.Where("status = ?", 1)
		case "inactive":
			query = query.Where("status = ?", 0)
		default:
			common.BadRequest(c, "Invalid status filter")
			return
		}
	}

	// AllowAll 过滤
	if allowAll := strings.TrimSpace(c.Query("allow_all")); allowAll != "" {
		switch allowAll {
		case "true":
			query = query.Where("allow_all = ?", 1)
		case "false":
			query = query.Where("allow_all = ?", 0)
		default:
			common.BadRequest(c, "Invalid allow_all filter")
			return
		}
	}

	// 执行分页查询
	keys := make([]models.AuthKey, 0)
	total, err := common.PaginateQuery(
		query.Order("id DESC"),
		params,
		&keys,
	)
	if err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	// 返回分页响应
	response := common.NewPaginationResponse(keys, total, params)
	common.Success(c, response)
}

func CreateAuthKey(c *gin.Context) {
	var req AuthKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	if err := validateAuthKeyRequest(req); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	ctx := c.Request.Context()

	key, err := buildAuthKeyValue(req.Key)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	if count, err := gorm.G[models.AuthKey](models.DB).Where("key = ?", key).Count(ctx, "id"); err != nil {
		common.InternalServerError(c, "Failed to check auth key: "+err.Error())
		return
	} else if count > 0 {
		common.BadRequest(c, "API Key 已存在")
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		parsedExpiresAt, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			common.BadRequest(c, "Invalid expires_at format, must be RFC3339")
			return
		}
		expiresAt = &parsedExpiresAt
	}

	authKey := models.AuthKey{
		Name:      req.Name,
		Key:       key,
		Status:    boolPtrToInt(req.Status, 1),   // 默认启用
		AllowAll:  boolPtrToInt(req.AllowAll, 0), // 默认不允许所有模型
		Models:    sanitizeModelsToString(req.Models),
		ExpiresAt: expiresAt,
		RpmLimit:  intPtrWithDefault(req.RpmLimit, 0),
	}

	if err := gorm.G[models.AuthKey](models.DB).Create(ctx, &authKey); err != nil {
		common.InternalServerError(c, "Failed to create auth key: "+err.Error())
		return
	}

	service.InvalidateAuthKeys()

	common.Success(c, authKey)
}

func UpdateAuthKey(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID")
		return
	}

	var req AuthKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	if err := validateAuthKeyRequest(req); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	ctx := c.Request.Context()

	if _, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).First(ctx); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Auth key not found")
			return
		}
		common.InternalServerError(c, "Failed to load auth key: "+err.Error())
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		parsedExpiresAt, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			common.BadRequest(c, "Invalid expires_at format, must be RFC3339")
			return
		}
		expiresAt = &parsedExpiresAt
	}

	update := map[string]any{
		"name":       req.Name,
		"status":     boolPtrToInt(req.Status, 1),
		"allow_all":  boolPtrToInt(req.AllowAll, 0),
		"models":     sanitizeModelsToString(req.Models),
		"expires_at": expiresAt,
		"rpm_limit":  intPtrWithDefault(req.RpmLimit, 0),
	}

	if err := models.DB.WithContext(ctx).Model(&models.AuthKey{}).Where("id = ?", id).Updates(update).Error; err != nil {
		common.InternalServerError(c, "Failed to update auth key: "+err.Error())
		return
	}

	updated, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load updated auth key: "+err.Error())
		return
	}

	service.InvalidateAuthKeys()

	common.Success(c, updated)
}

func DeleteAuthKey(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID")
		return
	}
	ctx := c.Request.Context()
	if _, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).Delete(ctx); err != nil {
		common.InternalServerError(c, "Failed to delete auth key: "+err.Error())
		return
	}
	service.InvalidateAuthKeys()
	common.SuccessWithMessage(c, "Deleted", gin.H{"id": id})
}

// ToggleAuthKeyStatus 切换 AuthKey 状态
func ToggleAuthKeyStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID")
		return
	}

	ctx := c.Request.Context()

	// 获取当前的 AuthKey
	authKey, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Auth key not found")
			return
		}
		common.InternalServerError(c, "Failed to load auth key: "+err.Error())
		return
	}

	// 切换状态
	var newStatus int
	if authKey.Status == 1 {
		newStatus = 0
	} else {
		newStatus = 1
	}
	if _, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).Update(ctx, "status", newStatus); err != nil {
		common.InternalServerError(c, "Failed to update status: "+err.Error())
		return
	}

	service.InvalidateAuthKeys()

	// 返回更新后的记录
	authKey.Status = newStatus
	common.Success(c, authKey)
}

// GetAuthKeysList 获取所有项目（AuthKey）的简化列表（ID 和 Name）
func GetAuthKeysList(c *gin.Context) {
	ctx := c.Request.Context()

	keys, err := gorm.G[models.AuthKey](models.DB).Select("id", "name").Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	// 构建简化的返回结果
	type KeyItem struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}

	result := make([]KeyItem, len(keys))
	for i, key := range keys {
		result[i] = KeyItem{
			ID:   key.ID,
			Name: key.Name,
		}
	}
	result = append(result, KeyItem{ID: 0, Name: "管理员"})

	common.Success(c, result)
}

func validateAuthKeyRequest(req AuthKeyRequest) error {
	if req.AllowAll != nil && !*req.AllowAll && len(req.Models) == 0 {
		return errors.New("请至少选择一个允许的模型或启用允许全部模型")
	}
	if req.RpmLimit != nil && *req.RpmLimit < 0 {
		return errors.New("RPM 限制必须大于等于 0")
	}
	return nil
}

func buildAuthKeyValue(customSuffix string) (string, error) {
	suffix := strings.TrimSpace(customSuffix)
	if suffix == "" {
		randomKey, err := pkg.GenerateRandomCharsKey(36)
		if err != nil {
			return "", errors.New("Failed to generate key: " + err.Error())
		}
		return consts.KeyPrefix + randomKey, nil
	}

	suffix = strings.TrimPrefix(suffix, "sk-")
	if strings.TrimSpace(suffix) == "" {
		return "", errors.New("自定义令牌后缀不能为空")
	}
	if strings.ContainsAny(suffix, " \t\r\n") {
		return "", errors.New("自定义令牌后缀不能包含空白字符")
	}
	if len(suffix) > 128 {
		return "", errors.New("自定义令牌后缀不能超过 128 个字符")
	}
	return "sk-" + suffix, nil
}

func intPtrWithDefault(v *int, defaultValue int) int {
	if v == nil {
		return defaultValue
	}
	return *v
}

func sanitizeModels(modelsList []string) []string {
	result := make([]string, 0, len(modelsList))
	seen := make(map[string]struct{}, len(modelsList))
	for _, name := range modelsList {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
