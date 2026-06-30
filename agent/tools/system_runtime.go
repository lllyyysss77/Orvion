package tools

import (
	"context"
	"errors"
	"sync"
)

// TelegramAgentSystemToolRequest 是 service 注入系统管理工具时接收的参数。
type TelegramAgentSystemToolRequest struct {
	Query         string
	Limit         int
	RecentMinutes int
	Status        string
	CacheID       uint64
	All           bool
	ClearExisting *bool
	Task          string
}

// TelegramAgentSystemToolHooks 由 service 注入，避免 agent 反向依赖 service 造成包循环。
type TelegramAgentSystemToolHooks struct {
	GetSystemStatus       func(context.Context, TelegramAgentSystemToolRequest) (string, error)
	GetPerformanceStats   func(context.Context, TelegramAgentSystemToolRequest) (string, error)
	ListImageCache        func(context.Context, TelegramAgentSystemToolRequest) (string, error)
	DeleteImageCache      func(context.Context, TelegramAgentSystemToolRequest) (string, error)
	RefreshImageCache     func(context.Context, TelegramAgentSystemToolRequest) (string, error)
	GetBackgroundTasks    func(context.Context, TelegramAgentSystemToolRequest) (string, error)
	TriggerBackgroundTask func(context.Context, TelegramAgentSystemToolRequest) (string, error)
}

var (
	telegramAgentSystemToolHooksMu sync.RWMutex
	telegramAgentSystemToolHooks   TelegramAgentSystemToolHooks
)

// SetTelegramAgentSystemToolHooks 设置 TG Agent 系统管理工具实现。
func SetTelegramAgentSystemToolHooks(hooks TelegramAgentSystemToolHooks) {
	telegramAgentSystemToolHooksMu.Lock()
	telegramAgentSystemToolHooks = hooks
	telegramAgentSystemToolHooksMu.Unlock()
}

func getTelegramAgentSystemToolHooks() TelegramAgentSystemToolHooks {
	telegramAgentSystemToolHooksMu.RLock()
	defer telegramAgentSystemToolHooksMu.RUnlock()
	return telegramAgentSystemToolHooks
}

func telegramAgentSystemToolRequestFromArgs(args CallArgs) TelegramAgentSystemToolRequest {
	return TelegramAgentSystemToolRequest{
		Query:         args.Query,
		Limit:         args.Limit,
		RecentMinutes: args.RecentMinutes,
		Status:        args.Status,
		CacheID:       args.CacheID,
		All:           args.All,
		ClearExisting: args.ClearExisting,
		Task:          args.Task,
	}
}

func telegramAgentSystemToolUnavailable() error {
	return errors.New("系统管理工具尚未注册")
}

func callTelegramAgentSystemTool(ctx context.Context, args CallArgs, pick func(TelegramAgentSystemToolHooks) func(context.Context, TelegramAgentSystemToolRequest) (string, error)) (string, error) {
	handler := pick(getTelegramAgentSystemToolHooks())
	if handler == nil {
		return "", telegramAgentSystemToolUnavailable()
	}
	return handler(ctx, telegramAgentSystemToolRequestFromArgs(args))
}
