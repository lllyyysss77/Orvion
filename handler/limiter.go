package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/racio/llmio/common"
	"github.com/racio/llmio/service"
)

// GetLimiterStats 获取限流器统计信息
func GetLimiterStats(c *gin.Context) {
	ctx := c.Request.Context()
	stats := service.GetRPMStats(ctx)
	common.Success(c, stats)
}

type ProviderStatsRequest struct {
	ProviderIDs []uint `json:"provider_ids"`
}

type ProviderStatsItem struct {
	ProviderID uint `json:"provider_id"`
	RPMCount   int  `json:"rpm_count"`
	RPMLoaded  bool `json:"rpm_loaded"`
}

// GetProvidersStats 批量获取提供商 RPM 状态
func GetProvidersStats(c *gin.Context) {
	var req ProviderStatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if len(req.ProviderIDs) == 0 {
		common.Success(c, []ProviderStatsItem{})
		return
	}

	ctx := c.Request.Context()
	results := make([]ProviderStatsItem, 0, len(req.ProviderIDs))
	for _, providerID := range req.ProviderIDs {
		rpmCount, rpmErr := service.GetCurrentRPMCount(ctx, providerID)
		rpmLoaded := rpmErr == nil
		if rpmErr != nil {
			rpmCount = 0
		}

		results = append(results, ProviderStatsItem{
			ProviderID: providerID,
			RPMCount:   rpmCount,
			RPMLoaded:  rpmLoaded,
		})
	}

	common.Success(c, results)
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
