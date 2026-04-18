import { Component, type ErrorInfo, type ReactNode } from "react";

type ErrorBoundaryProps = {
  children: ReactNode;
  /** 可选自定义 fallback。未提供时使用默认 UI。 */
  fallback?: (error: Error, reset: () => void) => ReactNode;
};

type ErrorBoundaryState = {
  error: Error | null;
};

/**
 * ErrorBoundary 捕获子树的渲染期异常,避免整屏白屏。
 *
 * 注意:React 的 error boundary 只能捕获渲染、生命周期、构造函数中抛出的错误。
 * 事件处理器内的异常、异步 fetch 失败、setTimeout 回调抛错不会被触发——这些
 * 场景仍需在调用点自行 try/catch 或通过 toast 反馈。
 */
export default class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // 保留控制台栈,方便开发者定位;生产环境 source map 配合也能还原。
    console.error("[ErrorBoundary]", error, info.componentStack);
  }

  reset = (): void => {
    this.setState({ error: null });
  };

  render(): ReactNode {
    const { error } = this.state;
    if (!error) return this.props.children;

    if (this.props.fallback) {
      return this.props.fallback(error, this.reset);
    }

    const isDev = import.meta.env.DEV;
    return (
      <div className="min-h-screen flex items-center justify-center bg-background px-6">
        <div className="max-w-xl w-full rounded-lg border bg-card p-6 shadow-sm space-y-4">
          <div className="space-y-1">
            <h1 className="text-lg font-semibold">页面渲染出错</h1>
            <p className="text-sm text-muted-foreground">
              前端在渲染组件时抛出了异常。你可以尝试重试当前视图,或刷新整个页面。
            </p>
          </div>

          <div className="rounded-md bg-muted/40 border border-dashed p-3 text-xs font-mono break-words">
            {error.message || String(error)}
          </div>

          {isDev && error.stack ? (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground select-none">
                查看堆栈(仅开发环境)
              </summary>
              <pre className="mt-2 whitespace-pre-wrap break-words bg-muted/30 rounded-md p-2 font-mono text-[11px] leading-relaxed">
                {error.stack}
              </pre>
            </details>
          ) : null}

          <div className="flex gap-2">
            <button
              type="button"
              onClick={this.reset}
              className="inline-flex h-9 items-center justify-center rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-xs hover:bg-primary/90 transition-colors"
            >
              重试
            </button>
            <button
              type="button"
              onClick={() => window.location.reload()}
              className="inline-flex h-9 items-center justify-center rounded-md border px-4 text-sm font-medium hover:bg-accent hover:text-accent-foreground transition-colors"
            >
              刷新页面
            </button>
          </div>
        </div>
      </div>
    );
  }
}
