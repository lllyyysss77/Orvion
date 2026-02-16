package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/limiter"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service"
	"github.com/racio/orvion/service/subscription"
	_ "golang.org/x/crypto/x509roots/fallback"
)

func init() {
	// 加载 .env 文件（如果存在）
	_ = godotenv.Load()
	initLogging()

	ctx := context.Background()
	// 数据库默认使用 SQLite，支持 PostgreSQL
	// - DATABASE_DSN：连接串（支持 SQLite 路径 / sqlite:// / file: / Postgres URL / key=value）
	dsn := strings.TrimSpace(os.Getenv("DATABASE_DSN"))
	models.Init(ctx, dsn)

	// 初始化首次部署时间（持久化到数据库 configs 表），用于跨重启统计系统总运行时间。
	if _, err := service.GetOrInitFirstDeployTime(ctx); err != nil {
		slog.Warn("Failed to init first deploy time, will fallback at runtime", "error", err)
	}
	// 初始化累计消费金额（持久化到 configs 表），用于日志删除后仍可保留历史累计金额。
	if _, err := service.GetOrInitTotalConsumedAmount(ctx); err != nil {
		slog.Warn("Failed to init total consumed amount, will fallback at runtime", "error", err)
	}

	// 初始化Redis客户端（可选）
	var redisClient *redis.Client
	redisURL := os.Getenv("REDIS_URL")
	if redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			slog.Warn("Failed to parse Redis URL, using memory storage", "error", err)
		} else {
			// 限流相关 Redis 调用是“尽力而为”，不应阻塞主请求太久：缩短超时并关闭重试，避免网络抖动导致卡顿。
			opt.MaxRetries = 0
			opt.MinRetryBackoff = 0
			opt.MaxRetryBackoff = 0
			opt.DialTimeout = 1 * time.Second
			opt.ReadTimeout = 1 * time.Second
			opt.WriteTimeout = 1 * time.Second

			redisClient = redis.NewClient(opt)
			// 测试Redis连接
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := redisClient.Ping(pingCtx).Err(); err != nil {
				slog.Warn("Redis connection failed, using memory storage", "error", err)
				redisClient = nil
			} else {
				slog.Info("Redis connected successfully")
			}
		}
	}

	// 初始化限流管理器
	limiterManager := limiter.NewManager(redisClient)
	service.SetLimiterManager(limiterManager)

	slog.Info("TZ", "time.Local", time.Local.String())
}

func initLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))

	if level > slog.LevelDebug {
		gin.SetMode(gin.ReleaseMode)
	}
}

func main() {
	token := os.Getenv("TOKEN")
	router := buildRouter(token)

	port := os.Getenv("LLMIO_SERVER_PORT")
	if port == "" {
		port = consts.DefaultPort
	}

	addr := ":" + port
	server := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	startBackgroundWorkers(rootCtx)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server startup failed", "error", err, "addr", addr)
		}
		return
	case <-rootCtx.Done():
		slog.Info("Received shutdown signal, starting graceful shutdown")
	}

	shutdownTimeout := resolveShutdownTimeout()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("Graceful shutdown failed, forcing close", "error", err)
		if closeErr := server.Close(); closeErr != nil {
			slog.Warn("Force close failed", "error", closeErr)
		}
	}

	select {
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server stopped with error", "error", err)
		}
	case <-time.After(2 * time.Second):
	}

	slog.Info("Server shutdown completed")
}

func resolveShutdownTimeout() time.Duration {
	const defaultTimeout = 10 * time.Second
	raw := strings.TrimSpace(os.Getenv("LLMIO_SHUTDOWN_TIMEOUT_SECONDS"))
	if raw == "" {
		return defaultTimeout
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		slog.Warn("Invalid LLMIO_SHUTDOWN_TIMEOUT_SECONDS, using default", "value", raw, "default", int(defaultTimeout/time.Second))
		return defaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func startBackgroundWorkers(ctx context.Context) {
	service.StartPriceSync(ctx)
	subscription.StartCodexOAuthWorker(ctx)
	subscription.StartCodexOAuthCallbackForwarder(ctx)
}
