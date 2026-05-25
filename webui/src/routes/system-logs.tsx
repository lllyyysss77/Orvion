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
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import {
  clearSystemLogs,
  deleteImageCacheItem,
  getDatabaseTableRows,
  getDatabaseTables,
  getImageCache,
  getImageCacheBlob,
  getSystemLogs,
  type DatabaseTableInfo,
  type DatabaseTableRowsResponse,
  type ImageCacheSnapshot,
  type SystemLogSnapshot,
} from "@/lib/api";
import { Activity, ChevronLeft, ChevronRight, Cpu, Database, FileTerminal, GitBranch, HardDrive, Image as ImageIcon, RefreshCw, Table2, Trash2 } from "lucide-react";

const POLL_INTERVAL_MS = 3_000;
const DEFAULT_LINE_LIMIT = 200;
const DATABASE_TABLE_PAGE_SIZE = 30;

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

const formatPercent = (value?: number) => {
  if (!Number.isFinite(value as number) || value == null || value < 0) {
    return "--";
  }
  return `${value.toFixed(1)}%`;
};

const formatCount = (value?: number) => {
  if (!Number.isFinite(value as number) || value == null || value < 0) {
    return "--";
  }
  return Math.round(value).toLocaleString();
};

const formatDateTime = (value?: string) => {
  if (!value) {
    return "--";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "--";
  }
  return date.toLocaleString();
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

const formatDatabaseCellValue = (value: unknown) => {
  if (value == null) {
    return "NULL";
  }
  if (typeof value === "object") {
    try {
      return JSON.stringify(value);
    } catch {
      return String(value);
    }
  }
  return String(value);
};

const getSlowSQLRateTone = (value?: number) => {
  if (!Number.isFinite(value as number) || value == null) {
    return "border-muted bg-muted text-muted-foreground";
  }
  if (value >= 80) {
    return "border-emerald-300/70 bg-emerald-50 text-emerald-700 dark:border-emerald-400/30 dark:bg-emerald-500/10 dark:text-emerald-300";
  }
  if (value >= 50) {
    return "border-amber-300/70 bg-amber-50 text-amber-700 dark:border-amber-400/30 dark:bg-amber-500/10 dark:text-amber-300";
  }
  return "border-rose-300/70 bg-rose-50 text-rose-700 dark:border-rose-400/30 dark:bg-rose-500/10 dark:text-rose-300";
};

export default function SystemLogsPage() {
  const [snapshot, setSnapshot] = useState<SystemLogSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [tablesDialogOpen, setTablesDialogOpen] = useState(false);
  const [databaseTables, setDatabaseTables] = useState<DatabaseTableInfo[]>([]);
  const [tablesLoading, setTablesLoading] = useState(false);
  const [tableRowsLoading, setTableRowsLoading] = useState(false);
  const [selectedTableName, setSelectedTableName] = useState("");
  const [tableRows, setTableRows] = useState<DatabaseTableRowsResponse | null>(null);
  const [imageCacheDialogOpen, setImageCacheDialogOpen] = useState(false);
  const [imageCache, setImageCache] = useState<ImageCacheSnapshot | null>(null);
  const [imageCacheLoading, setImageCacheLoading] = useState(false);
  const [imageCacheDeletingId, setImageCacheDeletingId] = useState<number | null>(null);
  const [imagePreviewUrls, setImagePreviewUrls] = useState<Record<number, string>>({});
  const [keyword, setKeyword] = useState("");
  const [levelFilter, setLevelFilter] = useState<LogLevelFilter>("all");
  const viewerRef = useRef<HTMLDivElement | null>(null);
  const stickToBottomRef = useRef(true);
  const imagePreviewUrlsRef = useRef<Record<number, string>>({});

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
    const timer = window.setInterval(() => {
      void fetchSnapshot(false);
    }, POLL_INTERVAL_MS);

    return () => window.clearInterval(timer);
  }, [fetchSnapshot]);

  const clearImagePreviewUrls = useCallback(() => {
    Object.values(imagePreviewUrlsRef.current).forEach((url) => URL.revokeObjectURL(url));
    imagePreviewUrlsRef.current = {};
    setImagePreviewUrls({});
  }, []);

  useEffect(() => {
    return () => clearImagePreviewUrls();
  }, [clearImagePreviewUrls]);

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

  const fetchDatabaseTableContent = useCallback(async (tableName: string, page = 1) => {
    const normalizedName = tableName.trim();
    if (!normalizedName) {
      setSelectedTableName("");
      setTableRows(null);
      return;
    }

    try {
      setSelectedTableName(normalizedName);
      setTableRowsLoading(true);
      const data = await getDatabaseTableRows(normalizedName, page, DATABASE_TABLE_PAGE_SIZE);
      setTableRows(data);
    } catch (error) {
      console.error("获取数据库表内容失败", error);
      toast.error(error instanceof Error ? error.message : "获取数据库表内容失败");
    } finally {
      setTableRowsLoading(false);
    }
  }, []);

  const fetchDatabaseTables = useCallback(async () => {
    try {
      setTablesLoading(true);
      const data = await getDatabaseTables();
      setDatabaseTables(data.tables);
      const firstTableName = data.tables[0]?.name ?? "";
      if (firstTableName) {
        await fetchDatabaseTableContent(firstTableName, 1);
      } else {
        setSelectedTableName("");
        setTableRows(null);
      }
    } catch (error) {
      console.error("获取数据库表失败", error);
      toast.error(error instanceof Error ? error.message : "获取数据库表失败");
    } finally {
      setTablesLoading(false);
    }
  }, [fetchDatabaseTableContent]);

  useEffect(() => {
    if (!tablesDialogOpen) {
      return;
    }
    void fetchDatabaseTables();
  }, [tablesDialogOpen, fetchDatabaseTables]);

  const fetchImageCache = useCallback(async () => {
    try {
      setImageCacheLoading(true);
      const data = await getImageCache();
      const nextPreviewUrls: Record<number, string> = {};
      await Promise.all(data.items.map(async (item) => {
        try {
          const blob = await getImageCacheBlob(item.id);
          nextPreviewUrls[item.id] = URL.createObjectURL(blob);
        } catch (error) {
          console.error("读取图片缓存预览失败", error);
        }
      }));
      clearImagePreviewUrls();
      imagePreviewUrlsRef.current = nextPreviewUrls;
      setImagePreviewUrls(nextPreviewUrls);
      setImageCache(data);
    } catch (error) {
      console.error("获取图片缓存失败", error);
      toast.error(error instanceof Error ? error.message : "获取图片缓存失败");
    } finally {
      setImageCacheLoading(false);
    }
  }, [clearImagePreviewUrls]);

  useEffect(() => {
    if (!imageCacheDialogOpen) {
      clearImagePreviewUrls();
      return;
    }
    void fetchImageCache();
  }, [imageCacheDialogOpen, fetchImageCache, clearImagePreviewUrls]);

  const handleTablePageChange = (nextPage: number) => {
    if (!selectedTableName || tableRowsLoading) {
      return;
    }
    const totalPages = Math.max(tableRows?.pages ?? 1, 1);
    const normalizedPage = Math.min(Math.max(nextPage, 1), totalPages);
    void fetchDatabaseTableContent(selectedTableName, normalizedPage);
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

  const handleDeleteImageCache = async (id: number) => {
    try {
      setImageCacheDeletingId(id);
      await deleteImageCacheItem(id);
      toast.success("图片缓存已删除");
      await fetchImageCache();
    } catch (error) {
      console.error("删除图片缓存失败", error);
      toast.error(error instanceof Error ? error.message : "删除图片缓存失败");
    } finally {
      setImageCacheDeletingId(null);
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
              <h2 className="text-2xl font-bold tracking-tight">系统状态</h2>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="rounded-full"
            onClick={() => setTablesDialogOpen(true)}
          >
            <Database className="size-4" />
            当前表
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="rounded-full"
            onClick={() => setImageCacheDialogOpen(true)}
          >
            <ImageIcon className="size-4" />
            图片缓存
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
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
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
        <div className="rounded-2xl border border-border/60 bg-card/90 px-4 py-3 shadow-sm">
          <div className="text-xs text-muted-foreground">当前协程数</div>
          <div className="mt-1 inline-flex items-center gap-1 text-sm font-medium">
            <GitBranch className="size-3.5 text-muted-foreground" />
            {formatCount(snapshot?.process?.goroutines)}
          </div>
        </div>
        <div className="rounded-2xl border border-border/60 bg-card/90 px-4 py-3 shadow-sm">
          <div className="text-xs text-muted-foreground">累计 GC 次数</div>
          <div className="mt-1 inline-flex items-center gap-1 text-sm font-medium">
            <Activity className="size-3.5 text-muted-foreground" />
            {formatCount(snapshot?.process?.gc_count)}
          </div>
        </div>
      </div>

      <div className="rounded-xl border border-border/60 bg-card/80 p-3">
        <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto_240px]">
          <Input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder="关键字过滤（支持任意文本）"
            className="h-8 text-xs"
          />
          <Select value={levelFilter} onValueChange={(value) => setLevelFilter(value as LogLevelFilter)}>
            <SelectTrigger className="h-8 w-28 text-xs">
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
          <Button
            variant="destructive"
            size="sm"
            className="h-8 rounded-md text-xs"
            onClick={() => setClearDialogOpen(true)}
          >
            <Trash2 className="size-4" />
            删除日志
          </Button>
          <div
            className={cn(
              "flex h-8 items-center justify-between gap-3 rounded-md border px-3 text-xs",
              getSlowSQLRateTone(snapshot?.slow_sql?.normal_rate)
            )}
            title={`统计最近 ${formatCount(snapshot?.slow_sql?.window_size)} 条 SQL，慢 SQL 阈值：${snapshot?.slow_sql?.threshold_ms ?? 0}ms`}
          >
            <span className="shrink-0 font-medium">SQL 正常率</span>
            <span className="min-w-0 truncate text-right font-semibold">{formatPercent(snapshot?.slow_sql?.normal_rate)}</span>
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

      <Dialog open={imageCacheDialogOpen} onOpenChange={setImageCacheDialogOpen}>
        <DialogContent className="flex max-h-[86vh] w-[92vw] !max-w-5xl flex-col overflow-hidden p-0">
          <DialogHeader className="border-b border-border/70 px-5 py-4 pr-12">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <DialogTitle className="flex items-center gap-2">
                  <ImageIcon className="size-4 text-muted-foreground" />
                  图片缓存
                </DialogTitle>
                <DialogDescription>
                  {imageCache ? `${imageCache.total}/${imageCache.capacity} 张，${formatBytes(imageCache.bytes)}` : "查看当前进程内缓存的状态图片"}
                </DialogDescription>
              </div>
              <Button
                variant="outline"
                size="sm"
                className="rounded-full"
                disabled={imageCacheLoading}
                onClick={() => void fetchImageCache()}
              >
                <RefreshCw className={cn("size-4", imageCacheLoading && "animate-spin")} />
                刷新
              </Button>
            </div>
          </DialogHeader>

          <div className="min-h-0 flex-1 overflow-y-auto p-4">
            {imageCacheLoading && !imageCache ? (
              <div className="flex min-h-64 items-center justify-center text-sm text-muted-foreground">
                正在加载图片缓存...
              </div>
            ) : !imageCache || imageCache.items.length === 0 ? (
              <div className="flex min-h-64 items-center justify-center px-6 text-center text-sm text-muted-foreground">
                当前没有缓存的图片。
              </div>
            ) : (
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                {imageCache.items.map((item) => {
                  const previewUrl = imagePreviewUrls[item.id];
                  const deleting = imageCacheDeletingId === item.id;
                  return (
                    <div key={item.id} className="overflow-hidden rounded-xl border border-border/70 bg-card/90">
                      <div className="flex aspect-[4/3] items-center justify-center bg-muted/40">
                        {previewUrl ? (
                          <img
                            src={previewUrl}
                            alt={item.file_name || `图片缓存 ${item.index}`}
                            className="h-full w-full object-contain"
                          />
                        ) : (
                          <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
                            预览不可用
                          </div>
                        )}
                      </div>
                      <div className="space-y-3 p-3">
                        <div className="min-w-0">
                          <div className="truncate text-sm font-semibold" title={item.file_name}>
                            {item.file_name || `图片缓存 ${item.index}`}
                          </div>
                          <div className="mt-1 text-xs text-muted-foreground">
                            {formatBytes(item.size)} · {item.mime_type || "unknown"} · {formatDateTime(item.cached_at)}
                          </div>
                        </div>
                        <div className="line-clamp-2 break-all text-xs text-muted-foreground" title={item.source}>
                          {item.source || "无来源地址"}
                        </div>
                        <div className="flex justify-end">
                          <Button
                            variant="destructive"
                            size="sm"
                            className="rounded-full"
                            disabled={deleting || imageCacheLoading}
                            onClick={() => void handleDeleteImageCache(item.id)}
                          >
                            <Trash2 className="size-4" />
                            {deleting ? "删除中..." : "删除"}
                          </Button>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={tablesDialogOpen} onOpenChange={setTablesDialogOpen}>
        <DialogContent className="flex h-[88vh] w-[94vw] !max-w-[94vw] flex-col overflow-hidden p-0 sm:!max-w-[94vw]">
          <DialogHeader className="border-b border-border/70 px-5 py-4 pr-12">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <DialogTitle className="flex items-center gap-2">
                  <Database className="size-4 text-muted-foreground" />
                  当前数据库表
                </DialogTitle>
                <DialogDescription>
                  共 {databaseTables.length.toLocaleString()} 张表
                </DialogDescription>
              </div>
              <Button
                variant="outline"
                size="sm"
                className="rounded-full"
                disabled={tablesLoading}
                onClick={() => void fetchDatabaseTables()}
              >
                <RefreshCw className={cn("size-4", tablesLoading && "animate-spin")} />
                刷新
              </Button>
            </div>
          </DialogHeader>

          <div className="grid min-h-0 flex-1 overflow-hidden md:grid-cols-[270px_minmax(0,1fr)]">
            <div className="min-h-0 border-b border-border/70 bg-muted/20 md:border-r md:border-b-0">
              <div className="flex h-full min-h-0 flex-col">
                <div className="border-b border-border/60 px-4 py-3 text-xs font-medium text-muted-foreground">
                  数据表
                </div>
                <div className="min-h-0 flex-1 overflow-y-auto p-2">
                  {tablesLoading && databaseTables.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                      正在加载...
                    </div>
                  ) : databaseTables.length === 0 ? (
                    <div className="flex h-full items-center justify-center px-4 text-center text-sm text-muted-foreground">
                      当前数据库没有可显示的表。
                    </div>
                  ) : (
                    <div className="space-y-1">
                      {databaseTables.map((table) => {
                        const selected = table.name === selectedTableName;
                        return (
                          <button
                            key={table.name}
                            type="button"
                            className={cn(
                              "flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors",
                              selected
                                ? "bg-primary text-primary-foreground"
                                : "text-foreground hover:bg-background"
                            )}
                            onClick={() => void fetchDatabaseTableContent(table.name, 1)}
                          >
                            <Table2 className="size-4 shrink-0 opacity-80" />
                            <span className="min-w-0 flex-1 truncate font-medium">{table.name}</span>
                            <span
                              className={cn(
                                "shrink-0 rounded-full px-2 py-0.5 text-[11px]",
                                selected ? "bg-primary-foreground/15" : "bg-muted text-muted-foreground"
                              )}
                            >
                              {table.kind}
                            </span>
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              </div>
            </div>

            <div className="flex min-h-0 flex-col overflow-hidden">
              <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border/60 px-4 py-3">
                <div className="min-w-0">
                  <div className="truncate text-sm font-semibold">
                    {selectedTableName || "未选择表"}
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {tableRows ? `${tableRows.total.toLocaleString()} 行记录` : "--"}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-full"
                    disabled={!tableRows || tableRowsLoading || tableRows.page <= 1}
                    onClick={() => handleTablePageChange((tableRows?.page ?? 1) - 1)}
                  >
                    <ChevronLeft className="size-4" />
                    上一页
                  </Button>
                  <span className="min-w-20 text-center text-xs text-muted-foreground">
                    {tableRows ? `${tableRows.page}/${Math.max(tableRows.pages, 1)}` : "--"}
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-full"
                    disabled={!tableRows || tableRowsLoading || tableRows.page >= Math.max(tableRows.pages, 1)}
                    onClick={() => handleTablePageChange((tableRows?.page ?? 1) + 1)}
                  >
                    下一页
                    <ChevronRight className="size-4" />
                  </Button>
                </div>
              </div>

              <div className="min-h-0 flex-1 overflow-hidden">
                {tableRowsLoading ? (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    正在读取表内容...
                  </div>
                ) : !tableRows ? (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    请选择左侧数据表。
                  </div>
                ) : tableRows.columns.length === 0 ? (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    当前表没有可显示的列。
                  </div>
                ) : tableRows.rows.length === 0 ? (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    当前表暂无数据。
                  </div>
                ) : (
                  <div className="h-full overflow-auto">
                    <Table className="min-w-max text-xs">
                      <TableHeader className="sticky top-0 z-10 bg-background">
                        <TableRow>
                          <TableHead className="w-14 bg-background text-right text-muted-foreground">#</TableHead>
                          {tableRows.columns.map((column) => (
                            <TableHead key={column} className="bg-background">
                              {column}
                            </TableHead>
                          ))}
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {tableRows.rows.map((row, rowIndex) => (
                          <TableRow key={`${tableRows.page}-${rowIndex}`}>
                            <TableCell className="text-right text-muted-foreground">
                              {(tableRows.page - 1) * tableRows.page_size + rowIndex + 1}
                            </TableCell>
                            {tableRows.columns.map((column) => {
                              const cellText = formatDatabaseCellValue(row[column]);
                              const isNull = row[column] == null;
                              return (
                                <TableCell
                                  key={column}
                                  className={cn(
                                    "max-w-[280px] truncate",
                                    isNull && "font-mono text-muted-foreground"
                                  )}
                                  title={cellText}
                                >
                                  {cellText}
                                </TableCell>
                              );
                            })}
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
