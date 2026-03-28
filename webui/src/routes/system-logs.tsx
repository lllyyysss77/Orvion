import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { clearSystemLogs, getSystemLogs, type SystemLogSnapshot } from "@/lib/api";
import { Clock3, Copy, FileTerminal, Pause, Play, RefreshCw, Trash2 } from "lucide-react";

const POLL_INTERVAL_MS = 3_000;
const DEFAULT_LINE_LIMIT = 200;

const formatTime = (value?: string) => {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  return date.toLocaleString("zh-CN", { hour12: false });
};

const formatBytes = (bytes: number) => {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024*1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024*1024)).toFixed(1)} MB`;
};

const getLineTone = (line: string) => {
  if (line.includes("level=ERROR")) {
    return "text-rose-300";
  }
  if (line.includes("level=WARN")) {
    return "text-amber-300";
  }
  if (line.includes("level=DEBUG")) {
    return "text-sky-300";
  }
  return "text-slate-200";
};

export default function SystemLogsPage() {
  const [snapshot, setSnapshot] = useState<SystemLogSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [clearing, setClearing] = useState(false);
  const viewerRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);

  const fetchSnapshot = useCallback(async (initial = false) => {
    if (typeof document !== "undefined" && document.visibilityState === "hidden") {
      return;
    }

    if (initial) {
      setLoading(true);
    } else {
      setRefreshing(true);
    }

    try {
      const data = await getSystemLogs(DEFAULT_LINE_LIMIT);
      setSnapshot(data);
    } catch (error) {
      console.error("获取系统日志失败", error);
      if (initial) {
        toast.error(error instanceof Error ? error.message : "获取系统日志失败");
      }
    } finally {
      if (initial) {
        setLoading(false);
      } else {
        setRefreshing(false);
      }
    }
  }, []);

  useEffect(() => {
    void fetchSnapshot(true);
  }, [fetchSnapshot]);

  useEffect(() => {
    if (!autoRefresh) {
      return undefined;
    }

    const timer = window.setInterval(() => {
      void fetchSnapshot(false);
    }, POLL_INTERVAL_MS);

    return () => window.clearInterval(timer);
  }, [autoRefresh, fetchSnapshot]);

  const lines = useMemo(() => {
    const content = snapshot?.content ?? "";
    if (!content.trim()) {
      return [];
    }
    return content.split("\n");
  }, [snapshot?.content]);

  useEffect(() => {
    const viewer = viewerRef.current;
    if (!viewer || !stickToBottomRef.current) {
      return;
    }
    viewer.scrollTop = viewer.scrollHeight;
  }, [lines]);

  const handleViewerScroll = () => {
    const viewer = viewerRef.current;
    if (!viewer) {
      return;
    }
    const distanceToBottom = viewer.scrollHeight - viewer.scrollTop - viewer.clientHeight;
    stickToBottomRef.current = distanceToBottom < 80;
  };

  const handleCopy = async () => {
    if (!snapshot?.content) {
      toast.warning("当前没有可复制的日志内容");
      return;
    }
    await navigator.clipboard.writeText(snapshot.content);
    toast.success("系统日志已复制");
  };

  const handleClearLogs = async () => {
    try {
      setClearing(true);
      await clearSystemLogs();
      setClearDialogOpen(false);
      stickToBottomRef.current = true;
      toast.success("系统日志已清空");
      await fetchSnapshot(false);
    } catch (error) {
      console.error("清空系统日志失败", error);
      toast.error(error instanceof Error ? error.message : "清空系统日志失败");
    } finally {
      setClearing(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 p-1">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="flex size-9 items-center justify-center rounded-2xl bg-primary/10 text-primary">
              <FileTerminal className="size-4" />
            </span>
            <div>
              <h2 className="text-2xl font-bold tracking-tight">系统日志</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                实时查看后端服务写入的运行日志
              </p>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="outline" className="rounded-full px-3 py-1">
            最近 {DEFAULT_LINE_LIMIT} 行
          </Badge>
          <Button
            variant="outline"
            size="sm"
            className="rounded-full"
            onClick={() => setAutoRefresh((value) => !value)}
          >
            {autoRefresh ? <Pause className="size-4" /> : <Play className="size-4" />}
            {autoRefresh ? "暂停刷新" : "恢复刷新"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="rounded-full"
            onClick={() => void fetchSnapshot(false)}
          >
            <RefreshCw className={cn("size-4", refreshing && "animate-spin")} />
            刷新
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="rounded-full"
            onClick={() => void handleCopy()}
          >
            <Copy className="size-4" />
            复制
          </Button>
          <Button
            variant="destructive"
            size="sm"
            className="rounded-full"
            onClick={() => setClearDialogOpen(true)}
          >
            <Trash2 className="size-4" />
            删除日志
          </Button>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-3">
        <div className="rounded-2xl border border-border/60 bg-card/90 px-4 py-3 shadow-sm">
          <div className="text-xs text-muted-foreground">日志文件</div>
          <div className="mt-1 truncate text-sm font-medium" title={snapshot?.path || "-"}>
            {snapshot?.path || "-"}
          </div>
        </div>
        <div className="rounded-2xl border border-border/60 bg-card/90 px-4 py-3 shadow-sm">
          <div className="text-xs text-muted-foreground">最后更新时间</div>
          <div className="mt-1 inline-flex items-center gap-1 text-sm font-medium">
            <Clock3 className="size-3.5 text-muted-foreground" />
            {formatTime(snapshot?.updated_at)}
          </div>
        </div>
        <div className="rounded-2xl border border-border/60 bg-card/90 px-4 py-3 shadow-sm">
          <div className="text-xs text-muted-foreground">文件大小 / 行数</div>
          <div className="mt-1 text-sm font-medium">
            {formatBytes(snapshot?.size ?? 0)} / {snapshot?.lines ?? 0} 行
          </div>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden rounded-[28px] border border-border/70 bg-card/88 shadow-[0_18px_50px_rgba(98,71,47,0.08)]">
        {loading ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            正在加载系统日志...
          </div>
        ) : !snapshot?.exists ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            当前还没有找到日志文件，服务写入日志后这里会自动显示。
          </div>
        ) : lines.length === 0 ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            日志文件已存在，但最近没有可显示的内容。
          </div>
        ) : (
          <div
            ref={viewerRef}
            onScroll={handleViewerScroll}
            className="h-full overflow-y-auto bg-[#161616] px-4 py-4 font-mono text-xs leading-6"
          >
            {lines.map((line, index) => (
              <div key={`${index}-${line.slice(0, 24)}`} className="flex gap-3">
                <span className="w-10 shrink-0 select-none text-right text-slate-500">
                  {index + 1}
                </span>
                <span className={cn("min-w-0 flex-1 break-all", getLineTone(line))}>
                  {line || " "}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      <AlertDialog open={clearDialogOpen} onOpenChange={setClearDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确定要清空系统日志吗？</AlertDialogTitle>
            <AlertDialogDescription>
              这会清空当前日志文件中的全部内容，但不会删除日志文件本身。清空后页面会自动刷新。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={clearing}>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => void handleClearLogs()} disabled={clearing}>
              {clearing ? "清空中..." : "确认清空"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
