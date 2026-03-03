package main

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/handler"
	adminhandler "github.com/racio/orvion/handler/admin"
	codexhandler "github.com/racio/orvion/handler/codex"
	iflowhandler "github.com/racio/orvion/handler/iflow"
	"github.com/racio/orvion/middleware"
)

func buildRouter(token string) *gin.Engine {
	router := gin.Default()
	router.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/v1"})))

	configureTrustedProxies(router)
	registerPublicRoutes(router)

	authOpenAI := middleware.AuthOpenAI(token)
	authAnthropic := middleware.AuthAnthropic(token)

	registerCodexAndIFlowRoutes(router, authOpenAI)
	registerUnifiedRoutes(router, authOpenAI, authAnthropic)
	registerAuthKeySummaryRoutes(router, authOpenAI)
	registerAdminAPIRoutes(router, token)
	setWebUIRoutes(router)

	return router
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

	// 兼容性路由
	router.GET("/healthz", handler.HealthCheck)
	router.GET("/livez", handler.LivenessCheck)
	router.GET("/readyz", handler.ReadinessCheck)

	// API 健康检查接口（无需认证，为了兼容前端）
	router.GET("/api/health/detail", handler.GetSystemHealthDetail)

	// OAuth 回调（无需认证）
	router.GET("/codex/callback", codexhandler.CodexOAuthCallback)
	router.GET("/iflow/callback", iflowhandler.IFlowOAuthCallback)
}

func registerCodexAndIFlowRoutes(router *gin.Engine, authOpenAI gin.HandlerFunc) {
	// codex 官方直连接口
	codexAPI := router.Group("/codex/v1", authOpenAI)
	codexAPI.POST("/responses", codexhandler.CodexAPIResponsesHandler)
	codexAPI.POST("/responses/compact", codexhandler.CodexAPIResponsesCompactHandler)

	// iflow 官方直连接口
	iflowV1 := router.Group("/iflow/v1", authOpenAI)
	iflowV1.POST("/chat/completions", iflowhandler.IFlowAPIChatCompletionsHandler)
}

func registerUnifiedRoutes(router *gin.Engine, authOpenAI gin.HandlerFunc, authAnthropic gin.HandlerFunc) {
	// 统一接口
	v1 := router.Group("/v1")
	v1.GET("/models", authOpenAI, handler.OpenAIModelsHandler)
	v1.POST("/chat/completions", authOpenAI, handler.ChatCompletionsHandler)
	v1.POST("/responses", authOpenAI, handler.ResponsesHandler)
	v1.POST("/embeddings", authOpenAI, handler.EmbeddingsHandler)
	v1.POST("/messages", authAnthropic, handler.Messages)
	v1.POST("/messages/count_tokens", authAnthropic, handler.CountTokens)
}

func registerAuthKeySummaryRoutes(router *gin.Engine, authOpenAI gin.HandlerFunc) {
	// API Key 概览（用于前端在 API Key 登录时展示）
	authKey := router.Group("/auth-key", authOpenAI)
	authKey.GET("/summary", handler.AuthKeySummary)
}

func registerAdminAPIRoutes(router *gin.Engine, token string) {
	api := router.Group("/api")
	api.Use(middleware.Auth(token))

	registerMetricsRoutes(api)
	registerProviderRoutes(api)
	registerModelRoutes(api)
	registerModelProviderRoutes(api)
	registerSystemRoutes(api)
	registerAuthKeyRoutes(api)
	registerCodexRoutes(api)
	registerIFlowRoutes(api)
	registerConfigRoutes(api)
	registerLimiterRoutes(api)
	registerTestRoutes(api)
}

func registerMetricsRoutes(api *gin.RouterGroup) {
	api.GET("/metrics/use/:days", handler.Metrics)
	api.GET("/metrics/summary", handler.MetricsSummary)
	api.GET("/metrics/counts", handler.Counts)
	api.GET("/metrics/projects", handler.ProjectCounts)
	api.GET("/metrics/request-amount", handler.RequestAmountTrend)
	api.GET("/metrics/daily-model-cost", handler.DailyModelCostTrend)
}

func registerProviderRoutes(api *gin.RouterGroup) {
	api.GET("/providers/template", adminhandler.GetProviderTemplates)
	api.GET("/providers", adminhandler.GetProviders)
	api.GET("/providers/models/:id", adminhandler.GetProviderModels)
	api.POST("/providers", adminhandler.CreateProvider)
	api.PUT("/providers/:id", adminhandler.UpdateProvider)
	api.DELETE("/providers/:id", adminhandler.DeleteProvider)
}

func registerModelRoutes(api *gin.RouterGroup) {
	api.GET("/models", adminhandler.GetModels)
	api.GET("/models/select", adminhandler.GetModelList)
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
	api.GET("/version", handler.GetVersion)
	api.GET("/logs", adminhandler.GetRequestLogs)
	api.GET("/logs/:id/chat-io", adminhandler.GetChatIO)
	api.GET("/user-agents", adminhandler.GetUserAgents)
	api.POST("/logs/cleanup", adminhandler.CleanLogs)
}

func registerAuthKeyRoutes(api *gin.RouterGroup) {
	api.GET("/auth-keys", handler.GetAuthKeys)
	api.GET("/auth-keys/list", handler.GetAuthKeysList)
	api.POST("/auth-keys", handler.CreateAuthKey)
	api.PUT("/auth-keys/:id", handler.UpdateAuthKey)
	api.PATCH("/auth-keys/:id/status", handler.ToggleAuthKeyStatus)
	api.DELETE("/auth-keys/:id", handler.DeleteAuthKey)
}

func registerCodexRoutes(api *gin.RouterGroup) {
	api.GET("/codex/oauth/start", codexhandler.StartCodexSubscriptionOAuth)
	api.GET("/codex/oauth/status", codexhandler.GetCodexSubscriptionOAuthStatus)
	api.GET("/codex/subscriptions", codexhandler.ListCodexSubscriptions)
	api.DELETE("/codex/subscriptions/:id", codexhandler.DeleteCodexSubscription)
	api.GET("/codex/subscriptions/:id/models", codexhandler.GetCodexSubscriptionModels)
	api.GET("/codex/subscriptions/:id/quota", codexhandler.GetCodexSubscriptionQuota)
}

func registerIFlowRoutes(api *gin.RouterGroup) {
	api.GET("/iflow/oauth/start", iflowhandler.StartIFlowOAuth)
	api.GET("/iflow/oauth/status", iflowhandler.GetIFlowOAuthStatus)
	api.GET("/iflow/subscriptions", iflowhandler.ListIFlowSubscriptions)
	api.GET("/iflow/subscriptions/:id/models", iflowhandler.GetIFlowSubscriptionModels)
	api.POST("/iflow/subscriptions/cookie", iflowhandler.AddIFlowSubscriptionByCookie)
	api.DELETE("/iflow/subscriptions/:id", iflowhandler.DeleteIFlowSubscription)
}

func registerConfigRoutes(api *gin.RouterGroup) {
	api.GET("/config/:key", adminhandler.GetConfigByKey)
	api.PUT("/config/:key", adminhandler.UpdateConfigByKey)
}

func registerLimiterRoutes(api *gin.RouterGroup) {
	api.GET("/limiter/stats", handler.GetLimiterStats)
	api.GET("/limiter/health", handler.GetLimiterHealth)
	api.POST("/providers/stats", handler.GetProvidersStats)
}

func registerTestRoutes(api *gin.RouterGroup) {
	api.GET("/test/:id", handler.ProviderTestHandler)
	api.GET("/test/react/:id", handler.TestReactHandler)
	api.GET("/test/count_tokens", handler.TestCountTokens)
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
	router.StaticFS("/assets", http.FS(subFS))

	router.NoRoute(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet && shouldServeSPA(c.Request.URL.Path) {
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
			return
		}
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", []byte("404 Not Found"))
	})
}

func shouldServeSPA(path string) bool {
	switch {
	case path == "/api", strings.HasPrefix(path, "/api/"):
		return false
	case path == "/v1", strings.HasPrefix(path, "/v1/"):
		return false
	case path == "/codex", strings.HasPrefix(path, "/codex/"):
		return false
	case path == "/iflow", strings.HasPrefix(path, "/iflow/"):
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
