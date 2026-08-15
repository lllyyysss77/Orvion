package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/handler"
	adminhandler "github.com/racio/orvion/handler/admin"
	"github.com/racio/orvion/middleware"
)

func buildRouter(token string) *gin.Engine {
	router := gin.New()
	router.Use(
		gin.LoggerWithConfig(gin.LoggerConfig{
			Formatter: formatAccessLog,
			Skip:      shouldSkipAccessLog,
		}),
		gin.Recovery(),
	)
	router.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/v1"})))

	configureTrustedProxies(router)
	registerPublicRoutes(router)

	authOpenAI := middleware.AuthOpenAI(token)
	authOpenAIOptional := middleware.AuthOpenAIOptional(token)
	authAnthropic := middleware.AuthAnthropic(token)

	registerUnifiedRoutes(router, authOpenAI, authOpenAIOptional, authAnthropic)
	registerAuthKeySummaryRoutes(router, authOpenAI)
	registerAdminAPIRoutes(router, token)
	setWebUIRoutes(router)

	return router
}

func formatAccessLog(param gin.LogFormatterParams) string {
	level := "INFO"
	if param.StatusCode >= http.StatusInternalServerError {
		level = "ERROR"
	} else if param.StatusCode >= http.StatusBadRequest {
		level = "WARN"
	}

	path := param.Path
	if path == "" && param.Request != nil && param.Request.URL != nil {
		path = param.Request.URL.Path
	}

	errorMessage := sanitizeAccessLogValue(param.ErrorMessage)
	errorPart := ""
	if errorMessage != "" {
		errorPart = fmt.Sprintf(" error=%q", errorMessage)
	}

	return fmt.Sprintf(
		"time=%s level=%s msg=%q status=%d method=%s path=%q%s\n",
		param.TimeStamp.Format(time.RFC3339),
		level,
		"接口调用",
		param.StatusCode,
		param.Method,
		path,
		errorPart,
	)
}

func sanitizeAccessLogValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func shouldSkipAccessLog(c *gin.Context) bool {
	path := c.Request.URL.Path

	if path == "" {
		return true
	}

	return !strings.HasPrefix(path, "/v1")
}

func configureTrustedProxies(router *gin.Engine) {
	// 为了获取真实客户端 IP：
	// - 默认不信任任何代理（避免客户端伪造 X-Forwarded-For）
	// - 如你在 Nginx/CF 等反代后面部署，请通过 TRUSTED_PROXIES 显式配置可信代理 IP/CIDR
	//   示例：TRUSTED_PROXIES=127.0.0.1,::1,10.0.0.0/8
	trustedProxiesEnv := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if trustedProxiesEnv == "" {
		if err := router.SetTrustedProxies(nil); err != nil {
			slog.Warn("Failed to disable trusted proxies", "error", err)
		}
		return
	}

	parts := strings.Split(trustedProxiesEnv, ",")
	proxies := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		proxies = append(proxies, p)
	}
	if err := router.SetTrustedProxies(proxies); err != nil {
		slog.Warn("Failed to set trusted proxies", "error", err, "TRUSTED_PROXIES", trustedProxiesEnv)
	}
}

func registerPublicRoutes(router *gin.Engine) {
	// 健康检查接口（无需认证）
	router.GET("/health", handler.HealthCheck)
	router.GET("/health/live", handler.LivenessCheck)
	router.GET("/health/ready", handler.ReadinessCheck)
	router.GET("/health/detail", handler.GetSystemHealthDetail)

	// API 健康检查接口（无需认证）
	router.GET("/api/health/detail", handler.GetSystemHealthDetail)
	// 当前版本用于登录页和外部探活，不包含敏感信息，无需认证。
	router.GET("/api/version", handler.GetVersion)

}

func registerUnifiedRoutes(router *gin.Engine, authOpenAI gin.HandlerFunc, authOpenAIOptional gin.HandlerFunc, authAnthropic gin.HandlerFunc) {
	// 统一接口
	v1 := router.Group("/v1")
	v1.Use(middleware.LimitRequestBody(middleware.ResolveMaxRequestBodyBytes()))
	v1.GET("/models", authOpenAIOptional, handler.OpenAIModelsHandler)
	v1.POST("/chat/completions", authOpenAI, handler.ChatCompletionsHandler)
	v1.POST("/responses", authOpenAI, handler.ResponsesHandler)
	v1.POST("/responses/compact", authOpenAI, handler.ResponsesCompactHandler)
	v1.POST("/embeddings", authOpenAI, handler.EmbeddingsHandler)
	v1.POST("/rerank", authOpenAI, handler.RerankHandler)
	v1.POST("/images/generations", authOpenAI, handler.ImagesGenerationsHandler)
	v1.POST("/images/edits", authOpenAI, handler.ImagesEditsHandler)
	v1.POST("/messages", authAnthropic, handler.Messages)
	v1.POST("/messages/count_tokens", authAnthropic, handler.CountTokens)
}

func registerAuthKeySummaryRoutes(router *gin.Engine, authOpenAI gin.HandlerFunc) {
	// API Key 概览（用于前端在 API Key 登录时展示）
	authKey := router.Group("/auth-key", authOpenAI)
	authKey.GET("/summary", handler.AuthKeySummary)
	authKey.GET("/logs", handler.GetAuthKeyRequestLogs)
	authKey.GET("/logs/:id/chat-io", handler.GetAuthKeyChatIO)
}

func registerAdminAPIRoutes(router *gin.Engine, token string) {
	api := router.Group("/api")
	api.Use(middleware.Auth(token))

	registerMetricsRoutes(api)
	registerProxyRoutes(api)
	registerProviderRoutes(api)
	registerModelRoutes(api)
	registerModelProviderRoutes(api)
	registerSystemRoutes(api)
	registerTelegramAgentRoutes(api)
	registerSkillRoutes(api)
	registerAuthKeyRoutes(api)
	registerConfigRoutes(api)
	registerLimiterRoutes(api)
	registerTestRoutes(api)
}

func registerProxyRoutes(api *gin.RouterGroup) {
	api.GET("/proxies", adminhandler.GetProxies)
	api.POST("/proxies", adminhandler.CreateProxy)
	api.PUT("/proxies/:id", adminhandler.UpdateProxy)
	api.POST("/proxies/:id/region-check", adminhandler.CheckProxyRegion)
	api.DELETE("/proxies/:id", adminhandler.DeleteProxy)
}

func registerMetricsRoutes(api *gin.RouterGroup) {
	api.GET("/metrics/use/:days", handler.Metrics)
	api.GET("/metrics/summary", handler.MetricsSummary)
	api.GET("/metrics/counts", handler.Counts)
	api.GET("/metrics/projects", handler.ProjectCounts)
	api.GET("/metrics/request-amount", handler.RequestAmountTrend)
	api.GET("/metrics/model-usage", handler.ModelUsageSummary)
	api.GET("/metrics/daily-model-cost", handler.DailyModelCostTrend)
}

func registerProviderRoutes(api *gin.RouterGroup) {
	api.GET("/providers/template", adminhandler.GetProviderTemplates)
	api.GET("/providers", adminhandler.GetProviders)
	api.GET("/providers/models/:id", adminhandler.GetProviderModels)
	api.POST("/providers", adminhandler.CreateProvider)
	api.PUT("/providers/:id", adminhandler.UpdateProvider)
	api.PATCH("/providers/:id/status", adminhandler.UpdateProviderStatus)
	api.DELETE("/providers/:id", adminhandler.DeleteProvider)
}

func registerModelRoutes(api *gin.RouterGroup) {
	api.GET("/models", adminhandler.GetModels)
	api.GET("/models/select", adminhandler.GetModelList)
	api.POST("/models/:id/connectivity", adminhandler.TestModelConnectivity)
	api.POST("/models", adminhandler.CreateModel)
	api.PUT("/models/:id", adminhandler.UpdateModel)
	api.PATCH("/models/:id/status", adminhandler.UpdateModelStatus)
	api.DELETE("/models/:id", adminhandler.DeleteModel)
}

func registerModelProviderRoutes(api *gin.RouterGroup) {
	api.GET("/model-providers", adminhandler.GetModelProviders)
	api.GET("/model-providers/status", adminhandler.GetModelProviderStatus)
	api.POST("/model-providers", adminhandler.CreateModelProvider)
	api.PUT("/model-providers/:id", adminhandler.UpdateModelProvider)
	api.PATCH("/model-providers/:id/status", adminhandler.UpdateModelProviderStatus)
	api.DELETE("/model-providers/:id", adminhandler.DeleteModelProvider)
}

func registerSystemRoutes(api *gin.RouterGroup) {
	api.GET("/version/update-check", handler.GetVersionUpdateCheck)
	api.GET("/logs", adminhandler.GetRequestLogs)
	api.GET("/logs/:id/chat-io", adminhandler.GetChatIO)
	api.GET("/system-logs", adminhandler.GetSystemLogs)
	api.POST("/system-logs/clear", adminhandler.ClearSystemLogs)
	api.GET("/system/image-cache", adminhandler.GetImageCache)
	api.GET("/system/image-cache/:id/content", adminhandler.GetImageCacheContent)
	api.DELETE("/system/image-cache/:id", adminhandler.DeleteImageCache)
	api.GET("/system/tables", adminhandler.GetDatabaseTables)
	api.GET("/system/tables/:name/rows", adminhandler.GetDatabaseTableRows)
	api.POST("/system/sql/execute", adminhandler.ExecuteDatabaseSQL)
	api.GET("/user-agents", adminhandler.GetUserAgents)
	api.POST("/logs/cleanup", adminhandler.CleanLogs)
}

func registerTelegramAgentRoutes(api *gin.RouterGroup) {
	api.GET("/tg-agent/tool-call-logs", adminhandler.GetTelegramAgentToolCallLogs)
	api.GET("/tg-agent/scheduled-tasks", adminhandler.GetTelegramAgentScheduledTasks)
	api.POST("/tg-agent/scheduled-tasks", adminhandler.CreateTelegramAgentScheduledTask)
	api.PUT("/tg-agent/scheduled-tasks/:id", adminhandler.UpdateTelegramAgentScheduledTask)
	api.PATCH("/tg-agent/scheduled-tasks/:id/status", adminhandler.UpdateTelegramAgentScheduledTaskStatus)
	api.DELETE("/tg-agent/scheduled-tasks/:id", adminhandler.DeleteTelegramAgentScheduledTask)
	api.DELETE("/tg-agent/sessions", adminhandler.DeleteTelegramAgentSession)
	api.DELETE("/tg-agent/sessions/:conversation_id", adminhandler.DeleteTelegramAgentSession)
}

func registerSkillRoutes(api *gin.RouterGroup) {
	api.GET("/skills", adminhandler.GetSkills)
	api.GET("/skills/detail/:name/files", adminhandler.GetSkillFiles)
	api.GET("/skills/detail/:name/file-content", adminhandler.GetSkillFileContent)
	api.GET("/skills/detail/:name", adminhandler.GetSkill)
	api.POST("/skills/detail/:name/review", adminhandler.ReviewSkillSecurity)
	api.PUT("/skills/detail/:name/file-content", adminhandler.UpdateSkillFileContent)
	api.DELETE("/skills/detail/:name", adminhandler.DeleteSkill)
	api.POST("/skills/reload", adminhandler.ReloadSkills)
	api.POST("/skills/upload", adminhandler.UploadSkill)
	api.PATCH("/skills/:name/status", adminhandler.UpdateSkillStatus)
}

func registerAuthKeyRoutes(api *gin.RouterGroup) {
	api.GET("/auth-keys", handler.GetAuthKeys)
	api.GET("/auth-keys/list", handler.GetAuthKeysList)
	api.POST("/auth-keys", handler.CreateAuthKey)
	api.PUT("/auth-keys/:id", handler.UpdateAuthKey)
	api.PATCH("/auth-keys/:id/status", handler.ToggleAuthKeyStatus)
	api.DELETE("/auth-keys/:id", handler.DeleteAuthKey)
}

func registerConfigRoutes(api *gin.RouterGroup) {
	api.GET("/config/:key", adminhandler.GetConfigByKey)
	api.PUT("/config/:key", adminhandler.UpdateConfigByKey)
	api.POST("/config/telegram-agent/models", adminhandler.GetTelegramAgentDirectModels)
	api.POST("/config/network-forwarding/proxy-test", adminhandler.ProviderProxyTestHandler)
	api.POST("/config/model-price-sync/run", adminhandler.RunModelPriceSync)
	api.POST("/config/breaker-alert-tg/test", adminhandler.RunTelegramBreakerAlertTest)
}

func registerLimiterRoutes(api *gin.RouterGroup) {
	api.GET("/limiter/stats", handler.GetLimiterStats)
	api.GET("/limiter/health", handler.GetLimiterHealth)
}

func registerTestRoutes(api *gin.RouterGroup) {
	api.GET("/test/:id", handler.ProviderTestHandler)
	api.POST("/test/chat/:id", handler.ModelChatTestHandler)
}

//go:embed webui/dist
var distFiles embed.FS

//go:embed webui/dist/index.html
var indexHTML []byte

func setWebUIRoutes(router *gin.Engine) {
	subFS, err := fs.Sub(distFiles, "webui/dist/assets")
	if err != nil {
		panic(err)
	}
	assets := router.Group("/assets")
	assets.Use(cacheWebUIAssets)
	assets.StaticFS("", http.FS(subFS))

	router.NoRoute(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet && shouldServeSPA(c.Request.URL.Path) {
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
			return
		}
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte("404 Not Found"))
	})
}

func cacheWebUIAssets(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Next()
}

func shouldServeSPA(path string) bool {
	switch {
	case path == "/api", strings.HasPrefix(path, "/api/"):
		return false
	case path == "/v1", strings.HasPrefix(path, "/v1/"):
		return false
	case path == "/v1models":
		return false
	case path == "/auth-key", strings.HasPrefix(path, "/auth-key/"):
		return false
	case path == "/health", strings.HasPrefix(path, "/health/"):
		return false
	case path == "/healthz", path == "/livez", path == "/readyz":
		return false
	default:
		return true
	}
}
