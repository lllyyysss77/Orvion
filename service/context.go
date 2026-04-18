package service

import (
	"context"
	"sync"
)

// 进程级 root context,由 main 在启动时通过 SetRootContext 注入。
// 请求路径上的 fire-and-forget goroutine(如异步写日志、自动熔断触发)
// 使用它派生的 context,以便在进程优雅关闭时一并停止。
var (
	rootCtxMu sync.RWMutex
	rootCtx   context.Context = context.Background()
)

// SetRootContext 设置进程 root context。main 在启动后台任务前调用一次。
func SetRootContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	rootCtxMu.Lock()
	rootCtx = ctx
	rootCtxMu.Unlock()
}

// RootContext 返回当前 root context。未注入时返回 context.Background()。
func RootContext() context.Context {
	rootCtxMu.RLock()
	defer rootCtxMu.RUnlock()
	return rootCtx
}
