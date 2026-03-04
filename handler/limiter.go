package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/service"
)

// GetLimiterStats 获取限流器统计信息
func GetLimiterStats(c *gin.Context) {
	ctx := c.Request.Context()
	stats := service.GetRPMStats(ctx)
	common.Success(c, stats)
}

// GetLimiterHealth 获取限流器健康状态
func GetLimiterHealth(c *gin.Context) {
	ctx := c.Request.Context()
	stats := service.GetRPMStats(ctx)

	health := gin.H{
		"status":    "healthy",
		"timestamp": gin.H{},
		"limiter":   stats,
	}

	// 检查限流器是否启用
	if enabled, ok := stats["enabled"].(bool); ok && !enabled {
		health["status"] = "disabled"
	}

	common.Success(c, health)
}
