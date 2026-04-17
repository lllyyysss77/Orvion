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
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { clearSystemLogs, getSystemLogs, type SystemLogSnapshot } from "@/lib/api";
import { Copy, Cpu, Download, FileTerminal, HardDrive, Pause, Play, RefreshCw, Trash2 } from "lucide-react";

const POLL_INTERVAL_MS = 3_000;
const DEFAULT_LINE_LIMIT = 200;

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

const formatCpuPercent = (value?: number) => {
  if (!Number.isFinite(value as number) || value == null || value < 0) {
    return "--";
  }
  return `${value.toFixed(2)}%`;
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

type LogLevelFilter = "all" | "error" | "warn" | "info" | "debug";

const getLineLevel = (line: string): Exclude<LogLevelFilter, "all"> | "other" => {
  if (line.includes("level=ERROR")) return "error";
  if (line.includes("level=WARN")) return "warn";
  if (line.includes("level=INFO")) return "info";
  if (line.includes("level=DEBUG")) return "debug";
  return "other";
};

export default function SystemLogsPage() {
  const [snapshot, setSnapshot] = useState<SystemLogSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [keyword, setKeyword] = useState("");
  const [levelFilter, setLevelFilter] = useState<LogLevelFilter>("all");
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

  const filteredLines = useMemo(() => {
    const keywordLower = keyword.trim().toLowerCase();
    return lines.filter((line) => {
      const level = getLineLevel(line);
      const levelMatched = levelFilter === "all" ? true : level === levelFilter;
      const keywordMatched = keywordLower === "" ? true : line.toLowerCase().includes(keywordLower);
      return levelMatched && keywordMatched;
    });
  }, [lines, keyword, levelFilter]);

  useEffect(() => {
    const viewer = viewerRef.current;
    if (!viewer || !stickToBottomRef.current) {
      return;
    }
    viewer.scrollTop = viewer.scrollHeight;
  }, [filteredLines]);

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

  const handleDownload = () => {
    if (!snapshot?.content) {
      toast.warning("当前没有可下载的日志内容");
      return;
    }
    const blob = new Blob([snapshot.content], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const now = new Date();
    const timestamp = `${now.getFullYear()}${String(now.getMonth() + 1).padStart(2, "0")}${String(now.getDate()).padStart(2, "0")}-${String(now.getHours()).padStart(2, "0")}${String(now.getMinutes()).padStart(2, "0")}${String(now.getSeconds()).padStart(2, "0")}`;
    const link = document.createElement("a");
    link.href = url;
    link.download = `system-logs-${timestamp}.log`;
    link.click();
    URL.revokeObjectURL(url);
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
              <h2 className="text-2xl font-bold tracking-tight">系统状态</h2>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
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
            variant="outline"
            size="sm"
            className="rounded-full"
            onClick={handleDownload}
          >
            <Download className="size-4" />
            下载日志
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

      <div className="grid gap-3 md:grid-cols-2">
        <div className="rounded-2xl border border-border/60 bg-card/90 px-4 py-3 shadow-sm">
          <div className="text-xs text-muted-foreground">当前进程内存</div>
          <div className="mt-1 inline-flex items-center gap-1 text-sm font-medium">
            <HardDrive className="size-3.5 text-muted-foreground" />
            {formatBytes(snapshot?.process?.memory_bytes ?? 0)}
          </div>
        </div>
        <div className="rounded-2xl border border-border/60 bg-card/90 px-4 py-3 shadow-sm">
          <div className="text-xs text-muted-foreground">当前进程 CPU</div>
          <div className="mt-1 inline-flex items-center gap-1 text-sm font-medium">
            <Cpu className="size-3.5 text-muted-foreground" />
            {formatCpuPercent(snapshot?.process?.cpu_percent)}
          </div>
        </div>
      </div>

      <div className="rounded-xl border border-border/60 bg-card/80 p-3">
        <div className="grid gap-2 sm:grid-cols-[1fr_220px]">
          <Input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder="关键字过滤（支持任意文本）"
            className="h-8 text-xs"
          />
          <Select value={levelFilter} onValueChange={(value) => setLevelFilter(value as LogLevelFilter)}>
            <SelectTrigger className="h-8 text-xs">
              <SelectValue placeholder="级别筛选" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部级别</SelectItem>
              <SelectItem value="error">ERROR</SelectItem>
              <SelectItem value="warn">WARN</SelectItem>
              <SelectItem value="info">INFO</SelectItem>
              <SelectItem value="debug">DEBUG</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden rounded-[28px] border border-border/70 bg-card/88 shadow-[0_18px_50px_rgba(98,71,47,0.08)]">
        {loading ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            正在加载系统日志...
          </div>
        ) : !snapshot?.exists ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            当前暂无系统日志内容。
          </div>
        ) : lines.length === 0 ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            最近没有可显示的内容。
          </div>
        ) : filteredLines.length === 0 ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            当前筛选条件下没有匹配的日志内容。
          </div>
        ) : (
          <div
            ref={viewerRef}
            onScroll={handleViewerScroll}
            className="font-exempt-log-viewer h-full overflow-y-auto bg-[#161616] px-4 py-4 font-mono text-xs leading-6"
          >
            {filteredLines.map((line, index) => (
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
              这会清空当前日志内容。清空后页面会自动刷新。
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
