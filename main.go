package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/handler"
	"github.com/racio/orvion/limiter"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg/logutil"
	"github.com/racio/orvion/service"
	_ "golang.org/x/crypto/x509roots/fallback"
)

func init() {
	// 加载 .env 文件（如果存在）
	_ = godotenv.Load()
	initLogging()

	ctx := context.Background()
	// 数据库使用 SQLite。
	// - DATABASE_DSN：SQLite 文件路径、sqlite:// 路径或 file: DSN；空值默认 ./data/llmio.db。
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

	// 初始化限流管理器
	limiterManager := limiter.NewManager()
	service.SetLimiterManager(limiterManager)
	// 初始化熔断告警（Telegram，可选配置）
	service.InitBreakerAlertNotifier()

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

	logWriter := resolveLogWriter()
	gin.DefaultWriter = logWriter
	gin.DefaultErrorWriter = logWriter

	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: level,
	})))

	if level > slog.LevelDebug {
		gin.SetMode(gin.ReleaseMode)
	}
}

func resolveLogWriter() io.Writer {
	return logutil.NewSystemLogWriter(os.Stdout)
}

func main() {
	token := os.Getenv("TOKEN")
	if strings.TrimSpace(token) == "" {
		slog.Warn("TOKEN 未配置: /api/* 管理接口将完全跳过鉴权,生产环境请务必设置")
	}
	router := buildRouter(token)

	port := os.Getenv("ORVION_SERVER_PORT")
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

	// 让 service 层内的 fire-and-forget goroutine 感知进程退出。
	service.SetRootContext(rootCtx)

	startBackgroundWorkers(rootCtx)

	serverErrCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("server goroutine panic", "recover", r, "stack", string(debug.Stack()))
				serverErrCh <- fmt.Errorf("server panic: %v", r)
			}
		}()
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

	// server.Shutdown 已返回,所有在途请求都结束了——此时触发 AuthKey 聚合 flusher
	// 做最后一次落库,并等其退出。这样即使 Shutdown 期间产生的记账也不会丢失。
	service.ShutdownAuthKeyFlusher()
	service.WaitAuthKeyFlusher(shutdownTimeout)

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
	raw := strings.TrimSpace(os.Getenv("ORVION_SHUTDOWN_TIMEOUT_SECONDS"))
	if raw == "" {
		return defaultTimeout
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		slog.Warn("Invalid ORVION_SHUTDOWN_TIMEOUT_SECONDS, using default", "value", raw, "default", int(defaultTimeout/time.Second))
		return defaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func startBackgroundWorkers(ctx context.Context) {
	service.StartChatLogMonthlyPartitionWorker(ctx)
	handler.StartGitHubVersionUpdateRefresher(ctx)
	service.StartAuthKeyFlusher(ctx)
	service.StartPriceSync(ctx)
	service.StartSystemLogCleanup(ctx)
	service.StartModelProviderAutoRecovery(ctx)
	service.StartTelegramCommandBot(ctx)
	service.StartTelegramDailyUsageReport(ctx)
}
