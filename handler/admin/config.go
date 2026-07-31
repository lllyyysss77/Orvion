package admin

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetConfigByKey 获取特定配置
func GetConfigByKey(c *gin.Context) {
	key := c.Param("key")
	config, err := gorm.G[models.Config](models.DB).Where(models.ColumnEquals("key"), key).First(c.Request.Context())

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 配置不存在，返回空响应
			common.Success(c, map[string]string{
				"key":   key,
				"value": "",
			})
			return
		}
		common.InternalServerError(c, "Failed to get config: "+err.Error())
		return
	}

	common.Success(c, map[string]string{
		"key":   config.Key,
		"value": config.Value,
	})
}

// UpdateConfigByKey 更新配置
func UpdateConfigByKey(c *gin.Context) {
	key := c.Param("key")

	var req ConfigValueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	now := time.Now()
	config := models.Config{Key: key, Value: req.Value}
	if err := models.DB.WithContext(c.Request.Context()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.Assignments(map[string]any{"value": req.Value, "updated_at": now}),
		}).
		Create(&config).Error; err != nil {
		common.InternalServerError(c, "Failed to update config: "+err.Error())
		return
	}

	common.Success(c, map[string]string{
		"key":   key,
		"value": req.Value,
	})
}

// RunModelPriceSync 立刻拉取模型价格表
func RunModelPriceSync(c *gin.Context) {
	if err := service.TriggerModelPriceSync(c.Request.Context()); err != nil {
		common.InternalServerError(c, "Failed to sync model prices: "+err.Error())
		return
	}
	common.Success(c, map[string]any{
		"status": "ok",
	})
}

// RunTelegramBreakerAlertTest 发送 TG 告警测试消息
func RunTelegramBreakerAlertTest(c *gin.Context) {
	if err := service.SendTelegramBreakerAlertTest(c.Request.Context()); err != nil {
		if errors.Is(err, service.ErrTelegramNotifierNotConfigured) {
			common.BadRequest(c, "TG 告警未启用或配置不完整，请先保存 TG 告警配置")
			return
		}
		common.InternalServerError(c, "Failed to send telegram breaker alert test: "+err.Error())
		return
	}
	common.Success(c, map[string]any{
		"status": "ok",
	})
}
