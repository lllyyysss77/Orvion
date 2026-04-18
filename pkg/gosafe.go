package pkg

import (
	"log/slog"
	"runtime/debug"
)

// GoSafe 在新 goroutine 里执行 fn,捕获 panic 并以 name 作为标签记录日志,
// 避免单个后台任务崩溃打爆整个进程。
//
// 约定:name 用于日志定位,使用稳定、可搜索的短字符串(如 "telegram.poll_loop")。
func GoSafe(name string, fn func()) {
	go RunSafe(name, fn)
}

// RunSafe 同步执行 fn 并捕获 panic。用于已有 goroutine 内部包裹。
func RunSafe(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("goroutine panic recovered",
				"where", name,
				"recover", r,
				"stack", string(debug.Stack()),
			)
		}
	}()
	fn()
}
