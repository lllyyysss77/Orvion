import { useState, useEffect, useMemo, useCallback, useRef, memo, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import Loading from "@/components/loading";
import { getLogs, type ChatLog, cleanLogs, getProviders, getAuthKeysList, getChatIO, type Provider, type AuthKeyItem } from "@/lib/api";
import { getStoredAuthToken, getStoredAuthTokenMode } from "@/lib/auth";
import { buildReplayCurlSnippet, inferGatewayEndpoint, maskAuthToken } from "@/lib/curl";
import { ChevronLeft, ChevronRight, RefreshCw, Trash2, Eye, EyeOff, Timer, Zap, ArrowDown, ArrowUp, Database, Coins, Copy } from "lucide-react";
import { resolveModelIcon } from "@/lib/model-icon";

// 格式化耗时显示（后端字段单位为毫秒）
const formatDurationMs = (milliseconds: number): string => {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return "-";
  if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`;
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(2)} s`;
  const minutes = Math.floor(milliseconds / 60_000);
  const seconds = Math.floor((milliseconds % 60_000) / 1000);
  return `${minutes} 分 ${seconds} 秒`;
};

const toFiniteNumber = (value: unknown): number | null => {
  if (typeof value === "number") return Number.isFinite(value) ? value : null;
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) return null;
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
};

// 兼容不同字段命名（例如旧版前端/后端字段不一致时），统一得到毫秒数。
const getLogDurationsMs = (log: ChatLog) => {
  const raw = log as unknown as Record<string, unknown>;
  const proxy = toFiniteNumber(raw.ProxyTimeMs) ?? toFiniteNumber(raw.proxy_time_ms) ?? toFiniteNumber(raw.ProxyTime) ?? 0;
  const first = toFiniteNumber(raw.FirstChunkTimeMs) ?? toFiniteNumber(raw.first_chunk_time_ms) ?? toFiniteNumber(raw.FirstChunkTime) ?? 0;
  const chunk = toFiniteNumber(raw.ChunkTimeMs) ?? toFiniteNumber(raw.chunk_time_ms) ?? toFiniteNumber(raw.ChunkTime) ?? 0;
  return { proxy, first, chunk, total: proxy + first + chunk };
};

// 格式化字节大小显示
const formatBytes = (bytes: number): string => {
  if (bytes === 0) return '0 B';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(2)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
};

type DetailCardProps = {
  label: string;
  value: ReactNode;
  mono?: boolean;
};

const DetailCard = ({ label, value, mono = false }: DetailCardProps) => (
  <div className="rounded-md border bg-muted/20 p-3 space-y-1">
    <p className="text-[11px] text-muted-foreground uppercase tracking-wide">{label}</p>
    <div className={`text-sm break-words ${mono ? 'font-mono text-xs' : ''}`}>
      {value ?? '-'}
    </div>
  </div>
);


const formatDurationValue = (value?: number) => (typeof value === "number" ? formatDurationMs(value) : "-");
const formatTokenValue = (value?: number) => {
  if (typeof value !== "number" || !Number.isFinite(value)) return "-";
  const absValue = Math.abs(value);
  if (absValue > 1000) {
    const kValue = value / 1000;
    const absK = Math.abs(kValue);
    const precision = absK >= 100 ? 0 : absK >= 10 ? 1 : 2;
    return `${kValue.toFixed(precision).replace(/\.?0+$/, "")}k`;
  }
  return value.toLocaleString();
};
const formatTpsValue = (value?: number) => (typeof value === "number" ? value.toFixed(2) : "-");
const formatCostValue = (value?: number) => {
  if (typeof value !== "number" || !Number.isFinite(value)) return "-";
  const trimmed = value.toFixed(6).replace(/\.?0+$/, "");
  return `$${trimmed}`;
};

type LogFilterState = {
  providerName: string;
  status: string;
  authKeyId: string;
};

const defaultLogFilters: LogFilterState = {
  providerName: "",
  status: "",
  authKeyId: "",
};

type LogCardProps = {
  log: ChatLog;
  onOpenDetail: (log: ChatLog) => void;
  onViewChatIO: (log: ChatLog) => void;
};

type AlignedMetricItemProps = {
  icon: ReactNode;
  label: string;
  value: string;
};

const AlignedMetricItem = ({ icon, label, value }: AlignedMetricItemProps) => (
  <div className="min-w-0 whitespace-nowrap text-xs leading-none">
    <span className="inline-flex h-3.5 w-3.5 shrink-0 items-center justify-center align-middle text-muted-foreground/85">
      {icon}
    </span>
    <span className="ml-1 text-muted-foreground align-middle">{label}</span>{" "}
    <span className="tabular-nums font-medium text-foreground align-middle">{value}</span>
  </div>
);

type LogMetric = {
  key: string;
  icon: ReactNode;
  label: string;
  value: string;
};

const MetricsGrid = ({ items }: { items: LogMetric[] }) => (
  <div className="mt-2 grid w-full grid-cols-2 gap-x-2 gap-y-1.5 sm:grid-cols-3 lg:grid-cols-[9.5rem_9.5rem_7.5rem_7.5rem_7.5rem_7.5rem]">
    {items.map((item) => (
      <div key={item.key} className="justify-self-start">
        <AlignedMetricItem icon={item.icon} label={item.label} value={item.value} />
      </div>
    ))}
  </div>
);

const LogCard = memo(({ log, onOpenDetail, onViewChatIO }: LogCardProps) => {
  const durations = getLogDurationsMs(log);
  const statusText = log.Status === "success" ? "成功" : "错误";
  const statusClass = log.Status === "success"
    ? "bg-emerald-100 text-emerald-700"
    : "bg-rose-100 text-rose-700";
  const createdAt = new Date(log.CreatedAt).toLocaleString();
  const canViewChatIO = log.Status === "success" && log.ChatIO;
  const metrics: LogMetric[] = [
    {
      key: "first",
      icon: <Timer className="h-3.5 w-3.5 text-sky-500" />,
      label: "首字",
      value: formatDurationValue(durations.first)
    },
    {
      key: "total",
      icon: <Zap className="h-3.5 w-3.5 text-amber-500" />,
      label: "总耗时",
      value: formatDurationValue(durations.total)
    },
    {
      key: "input",
      icon: <ArrowDown className="h-3.5 w-3.5 text-emerald-500" />,
      label: "输入",
      value: formatTokenValue(log.prompt_tokens)
    },
    {
      key: "output",
      icon: <ArrowUp className="h-3.5 w-3.5 text-violet-500" />,
      label: "输出",
      value: formatTokenValue(log.completion_tokens)
    },
    {
      key: "cache",
      icon: <Database className="h-3.5 w-3.5 text-cyan-600" />,
      label: "缓存",
      value: formatTokenValue(getCachedTokensFromLog(log))
    },
    {
      key: "price",
      icon: <Coins className="h-3.5 w-3.5 text-emerald-600" />,
      label: "价格",
      value: formatCostValue(log.total_cost)
    },
  ];

  return (
    <div className="rounded-2xl border border-border/60 bg-card/90 shadow-sm px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="flex flex-1 items-start gap-3 min-w-0">
          <ModelIcon name={log.Name || ""} />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2 min-w-0">
              <span className="font-semibold truncate max-w-[26ch]" title={log.Name}>
                {log.Name}
              </span>
              <span className="text-xs text-muted-foreground">-&gt;</span>
              <span className="inline-flex items-center rounded-full bg-muted/70 px-2 py-0.5 text-xs text-muted-foreground">
                {log.ProviderName || "-"}
              </span>
              <span className="text-xs text-muted-foreground truncate max-w-[26ch]" title={log.ProviderModel || "-"}>
                {log.ProviderModel || "-"}
              </span>
              <span className="text-[11px] text-muted-foreground">{createdAt}</span>
              {log.key_name ? (
                <span className="rounded-full bg-background/70 px-2 py-0.5 text-[11px] text-muted-foreground">
                  {log.key_name}
                </span>
              ) : null}
              <span className={`text-[11px] font-medium rounded-full px-2 py-0.5 ${statusClass}`}>
                {statusText}
              </span>
            </div>
            <MetricsGrid items={metrics} />
          </div>
        </div>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            className="h-8 w-8"
            onClick={() => onOpenDetail(log)}
            aria-label="查看详情"
            title="查看详情"
          >
            <Eye className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-8 w-8"
            onClick={() => onViewChatIO(log)}
            disabled={!canViewChatIO}
            aria-label="查看 IO"
            title="查看 IO"
          >
            <EyeOff className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </div>
  );
});

const parsePromptTokensDetails = (value: ChatLog["prompt_tokens_details"]) => {
  if (!value) return { cached_tokens: 0 };
  if (typeof value === "object") return value as { cached_tokens: number };
  if (typeof value !== "string") return { cached_tokens: 0 };
  const trimmed = value.trim();
  if (!trimmed) return { cached_tokens: 0 };
  try {
    const parsed = JSON.parse(trimmed) as { cached_tokens?: number };
    return { cached_tokens: typeof parsed?.cached_tokens === "number" ? parsed.cached_tokens : 0 };
  } catch {
    return { cached_tokens: 0 };
  }
};

const getCachedTokensFromLog = (log: ChatLog) => {
  const raw = log as unknown as Record<string, unknown>;
  const directValue = toFiniteNumber(raw.cached_tokens);
  if (typeof directValue === "number" && directValue > 0) {
    return directValue;
  }
  const details = parsePromptTokensDetails(log.prompt_tokens_details);
  if (typeof details.cached_tokens === "number" && Number.isFinite(details.cached_tokens)) {
    return details.cached_tokens;
  }
  return 0;
};

const ModelIcon = ({ name }: { name: string }) => {
  const config = resolveModelIcon(name);
  const fallback = (name || "M").slice(0, 2).toUpperCase();

  if (!config) {
    return (
      <div className="size-10 rounded-2xl bg-muted/60 text-muted-foreground flex items-center justify-center font-semibold text-xs">
        {fallback}
      </div>
    );
  }

  return (
    <div className="size-10 rounded-2xl bg-muted/60 flex items-center justify-center">
      <img src={config.src} alt={config.alt} className="size-5" />
    </div>
  );
};

export default function LogsPage() {
  const isAuthKeyMode = getStoredAuthTokenMode() === "auth_key";
  const [logs, setLogs] = useState<ChatLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(0);
  const [providerOptions, setProviderOptions] = useState<Provider[]>([]);
  const [authKeyOptions, setAuthKeyOptions] = useState<AuthKeyItem[]>([]);
  const [filters, setFilters] = useState<LogFilterState>(defaultLogFilters);
  const [draftFilters, setDraftFilters] = useState<LogFilterState>(defaultLogFilters);
  const navigate = useNavigate();
  // 详情弹窗
  const [selectedLog, setSelectedLog] = useState<ChatLog | null>(null);
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  // 清理弹窗
  const [cleanType, setCleanType] = useState<'count' | 'days'>('count');
  const [cleanValue, setCleanValue] = useState<string>('1000');
  const [isCleanDialogOpen, setIsCleanDialogOpen] = useState(false);
  const [cleanLoading, setCleanLoading] = useState(false);
  const [copyingCurlVariant, setCopyingCurlVariant] = useState<"masked" | "raw" | null>(null);
  const fetchFilterOptions = useCallback(async () => {
    if (isAuthKeyMode) {
      setProviderOptions([]);
      setAuthKeyOptions([]);
      return;
    }
    try {
      const [providers, authKeys] = await Promise.all([
        getProviders(),
        getAuthKeysList(),
      ]);
      setProviderOptions(providers);
      setAuthKeyOptions(authKeys);
    } catch (error) {
      console.error("Error fetching log filter options:", error);
    }
  }, [isAuthKeyMode]);

  const fetchLogs = useCallback(async () => {
    setLoading(true);
    try {
      const result = await getLogs(page, pageSize, {
        providerName: filters.providerName || undefined,
        status: filters.status || undefined,
        authKeyId: !isAuthKeyMode ? (filters.authKeyId || undefined) : undefined,
      });
      setLogs(result.data);
      setTotal(result.total);
      setPages(result.pages);
    } catch (error) {
      console.error("Error fetching logs:", error);
      toast.error("获取日志失败");
    } finally {
      setLoading(false);
    }
  }, [isAuthKeyMode, page, pageSize, filters]);

  useEffect(() => {
    void fetchLogs();
  }, [fetchLogs]);

  useEffect(() => {
    void fetchFilterOptions();
  }, [fetchFilterOptions]);

  const applyFilters = () => {
    setPage(1);
    setFilters(draftFilters);
  };

  const resetFilters = () => {
    setPage(1);
    setDraftFilters(defaultLogFilters);
    setFilters(defaultLogFilters);
  };

  const handlePageChange = (newPage: number) => {
    if (newPage >= 1 && newPage <= pages) setPage(newPage);
  };
  const handlePageSizeChange = (size: number) => {
    if (size === pageSize) return;
    setPage(1);
    setPageSize(size);
  };
  const handleRefresh = () => {
    void fetchLogs();
  };
  const handleCleanTypeChange = (type: 'count' | 'days') => {
    setCleanType(type);
    setCleanValue(type === 'count' ? '1000' : '30');
  };
  const handleCleanLogs = async () => {
    const value = parseInt(cleanValue);
    if (isNaN(value) || value <= 0) return;

    setCleanLoading(true);
    try {
      const result = await cleanLogs({ type: cleanType, value });
      toast.success(`已清理 ${result.deleted_count} 条日志`);
      await fetchLogs();
    } catch (error) {
      console.error("Error cleaning logs:", error);
      toast.error('清理失败');
    } finally {
      setCleanLoading(false);
      setIsCleanDialogOpen(false);
    }
  };
  const openDetailDialog = useCallback((log: ChatLog) => {
    setSelectedLog(log);
    setIsDialogOpen(true);
  }, []);
  const handleViewChatIO = useCallback((log: ChatLog) => {
    if (log.Status !== 'success' || !log.ChatIO) return;
    navigate(`/logs/${log.ID}/chat-io`, {
      state: {
        style: log.Style,
      },
    });
  }, [navigate]);
  const handleCopyReplayCurl = useCallback(async (log: ChatLog, masked: boolean) => {
    if (copyingCurlVariant) return;
    if (log.Status !== "success" || !log.ChatIO) {
      toast.error("该日志未记录请求输入，无法复制 cURL");
      return;
    }

    setCopyingCurlVariant(masked ? "masked" : "raw");
    try {
      const chatIO = await getChatIO(log.ID, { mode: "full" });
      const requestBody = (chatIO.Input ?? "").trim();
      if (!requestBody) {
        toast.error("请求输入为空，无法生成 cURL");
        return;
      }

      const endpoint = inferGatewayEndpoint(log.Style, requestBody);
      const rawAuthToken = getStoredAuthToken();
      const authToken = masked ? maskAuthToken(rawAuthToken) : (rawAuthToken || "YOUR_AUTH_TOKEN");
      const baseUrl = window.location.origin;
      const curlSnippet = buildReplayCurlSnippet({
        baseUrl,
        endpoint,
        authToken,
        requestBody,
      });

      await navigator.clipboard.writeText(curlSnippet);
      toast.success(masked ? "已复制 cURL（掩码版）" : "已复制 cURL（真实版）");
    } catch (error) {
      const message = error instanceof Error ? error.message : "未知错误";
      toast.error(`复制 cURL 失败: ${message}`);
    } finally {
      setCopyingCurlVariant(null);
    }
  }, [copyingCurlVariant]);
  const handleCopyReplayCurlFromDetail = useCallback((masked: boolean) => {
    if (!selectedLog) return;
    void handleCopyReplayCurl(selectedLog, masked);
  }, [handleCopyReplayCurl, selectedLog]);
  const logsList = useMemo(() => logs ?? [], [logs]);
  const listRef = useRef<HTMLDivElement | null>(null);
  const rowVirtualizer = useVirtualizer({
    count: logsList.length,
    getScrollElement: () => listRef.current,
    estimateSize: () => 220,
    overscan: 6,
  });
  // 布局开始
  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      {/* 顶部标题和刷新 */}
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">请求日志</h2>
          </div>
          <div className="flex w-full flex-wrap items-center justify-end gap-2 sm:w-auto">
            {!isAuthKeyMode ? (
              <Button
                onClick={() => setIsCleanDialogOpen(true)}
                variant="outline"
                size="icon"
                className="shrink-0"
                aria-label="清理日志"
                title="清理日志"
              >
                <Trash2 className="size-4" />
              </Button>
            ) : null}
            <Button
              onClick={handleRefresh}
              variant="outline"
              size="icon"
              className="shrink-0"
              aria-label="刷新列表"
              title="刷新列表"
            >
              <RefreshCw className="size-4" />
            </Button>
          </div>
        </div>
        <div className="rounded-xl border border-border/60 bg-card/80 p-3">
          <div className="flex flex-wrap items-center gap-1.5">
            <Select
              value={draftFilters.providerName || "all"}
              onValueChange={(value) => setDraftFilters((prev) => ({ ...prev, providerName: value === "all" ? "" : value }))}
            >
              <SelectTrigger className="h-8 w-[150px] text-xs">
                <SelectValue placeholder="提供商" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部提供商</SelectItem>
                {providerOptions.map((provider) => (
                  <SelectItem key={provider.ID} value={provider.Name}>
                    {provider.Name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={draftFilters.status || "all"}
              onValueChange={(value) => setDraftFilters((prev) => ({ ...prev, status: value === "all" ? "" : value }))}
            >
              <SelectTrigger className="h-8 w-[120px] text-xs">
                <SelectValue placeholder="状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                <SelectItem value="success">成功</SelectItem>
                <SelectItem value="error">错误</SelectItem>
              </SelectContent>
            </Select>
            {!isAuthKeyMode ? (
              <Select
                value={draftFilters.authKeyId || "all"}
                onValueChange={(value) => setDraftFilters((prev) => ({ ...prev, authKeyId: value === "all" ? "" : value }))}
              >
                <SelectTrigger className="h-8 w-[170px] text-xs">
                  <SelectValue placeholder="AuthKey" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部 AuthKey</SelectItem>
                  {authKeyOptions.map((authKey) => (
                    <SelectItem key={authKey.id} value={String(authKey.id)}>
                      {authKey.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : null}
            <Button variant="outline" size="sm" className="h-8 text-xs" onClick={resetFilters}>
              重置
            </Button>
            <Button size="sm" className="h-8 text-xs" onClick={applyFilters}>
              应用筛选
            </Button>
          </div>
        </div>
      </div>
      {/* 列表区域 */}
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载日志数据" />
          </div>
        ) : logsList.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            暂无请求日志
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div ref={listRef} className="flex-1 overflow-y-auto p-3">
              <div className="relative w-full" style={{ height: rowVirtualizer.getTotalSize() }}>
                {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                  const log = logsList[virtualRow.index];
                  if (!log) return null;
                  return (
                    <div
                      key={log.ID}
                      data-index={virtualRow.index}
                      ref={rowVirtualizer.measureElement}
                      className="absolute left-0 top-0 w-full pb-3"
                      style={{ transform: `translateY(${virtualRow.start}px)` }}
                    >
                      <LogCard log={log} onOpenDetail={openDetailDialog} onViewChatIO={handleViewChatIO} />
                    </div>
                  );
                })}
              </div>
            </div>
          </div>
        )}
      </div>
      {/* 分页区域 */}

      <div className="flex flex-wrap items-center justify-between gap-3 flex-shrink-0 border-t pt-2">
        <div className="text-sm text-muted-foreground whitespace-nowrap">
          共 {total} 条，第 {page} / {pages} 页
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Select value={String(pageSize)} onValueChange={(value) => handlePageSizeChange(Number(value))}>
              <SelectTrigger className="h-8 text-xs">
                <SelectValue placeholder="条数" />
              </SelectTrigger>
              <SelectContent>
                {[10, 20, 50].map((size) => (
                  <SelectItem key={size} value={String(size)}>
                    {size}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="icon"
              onClick={() => handlePageChange(page - 1)}
              disabled={page === 1}
              aria-label="上一页"
            >
              <ChevronLeft className="size-4" />
            </Button>
            <Button
              variant="outline"
              size="icon"
              onClick={() => handlePageChange(page + 1)}
              disabled={page === pages}
              aria-label="下一页"
            >
              <ChevronRight className="size-4" />
            </Button>
          </div>
        </div>
      </div>
      {/* 详情弹窗 */}
      {selectedLog && (
        <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
          <DialogContent className="w-[92vw] sm:w-auto sm:max-w-2xl max-h-[95vh] p-0 flex flex-col">
            <div className="px-5 py-4 border-b">
              <DialogHeader className="p-0">
                <DialogTitle className="flex items-center gap-2">
                  日志详情
                  <span className="text-xs text-muted-foreground font-normal">#{selectedLog.ID}</span>
                </DialogTitle>
              </DialogHeader>
              <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span>{new Date(selectedLog.CreatedAt).toLocaleString()}</span>
                <span className={selectedLog.Status === "success" ? "text-emerald-600" : "text-rose-600"}>
                  {selectedLog.Status}
                </span>
                {selectedLog.key_name ? (
                  <span className="rounded-full bg-muted/60 px-2 py-0.5">{selectedLog.key_name}</span>
                ) : null}
              </div>
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  className="h-8"
                  onClick={() => handleCopyReplayCurlFromDetail(true)}
                  disabled={copyingCurlVariant !== null || selectedLog.Status !== "success" || !selectedLog.ChatIO}
                >
                  <Copy className="size-3.5" />
                  复制 cURL（掩码）
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="h-8"
                  onClick={() => handleCopyReplayCurlFromDetail(false)}
                  disabled={copyingCurlVariant !== null || selectedLog.Status !== "success" || !selectedLog.ChatIO}
                >
                  <Copy className="size-3.5" />
                  复制 cURL（真实）
                </Button>
              </div>
            </div>
            <div className="overflow-y-auto px-5 py-4 flex-1 space-y-4 text-sm">
              {selectedLog.Error && (
                <div className="rounded-xl border border-destructive/40 bg-destructive/10 p-3">
                  <p className="text-xs text-destructive uppercase tracking-wide mb-1">错误信息</p>
                  <div className="text-destructive whitespace-pre-wrap break-words text-sm">
                    {selectedLog.Error}
                  </div>
                </div>
              )}

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <DetailCard label="模型名称" value={selectedLog.Name} />
                <DetailCard label="提供商" value={selectedLog.ProviderName || "-"} />
                <DetailCard label="提供商模型" value={selectedLog.ProviderModel || "-"} mono />
                <DetailCard label="响应大小" value={selectedLog.Size ? formatBytes(selectedLog.Size) : "-"} />
              </div>

              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                {(() => {
                  const d = getLogDurationsMs(selectedLog);
                  return (
                    <>
                      <DetailCard label="首包耗时" value={formatDurationValue(d.first)} />
                      <DetailCard label="完成耗时" value={formatDurationValue(d.chunk)} />
                      <DetailCard label="TPS" value={formatTpsValue(selectedLog.Tps)} />
                      <DetailCard label="价格" value={formatCostValue(selectedLog.total_cost)} />
                    </>
                  );
                })()}
              </div>

              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                {(() => {
                  const cachedTokens = getCachedTokensFromLog(selectedLog);
                  return (
                    <>
                      <DetailCard label="输入" value={formatTokenValue(selectedLog.prompt_tokens)} />
                      <DetailCard label="输出" value={formatTokenValue(selectedLog.completion_tokens)} />
                      <DetailCard label="总计" value={formatTokenValue(selectedLog.total_tokens)} />
                      <DetailCard label="缓存" value={formatTokenValue(cachedTokens)} />
                    </>
                  );
                })()}
              </div>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <DetailCard label="远端 IP" value={selectedLog.RemoteIP || "-"} mono />
                <DetailCard label="用户代理" value={selectedLog.UserAgent || "-"} mono />
                <DetailCard label="记录 IO" value={selectedLog.ChatIO ? "是" : "否"} />
                <DetailCard label="重试次数" value={selectedLog.Retry ?? 0} />
              </div>
            </div>
          </DialogContent>
        </Dialog>
      )}
      {/* 清理日志弹窗 */}
      {!isAuthKeyMode ? (
        <Dialog open={isCleanDialogOpen} onOpenChange={setIsCleanDialogOpen}>
          <DialogContent className="w-[92vw] sm:max-w-md">
            <DialogHeader>
              <DialogTitle>清理日志</DialogTitle>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="flex gap-2">
                <Button
                  variant={cleanType === 'count' ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => handleCleanTypeChange('count')}
                  className="flex-1"
                >
                  保留条数
                </Button>
                <Button
                  variant={cleanType === 'days' ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => handleCleanTypeChange('days')}
                  className="flex-1"
                >
                  保留天数
                </Button>
              </div>
              <div className="flex items-center gap-2">
                <Input
                  type="number"
                  min="1"
                  value={cleanValue}
                  onChange={(e) => setCleanValue(e.target.value)}
                  placeholder={cleanType === 'count' ? '输入保留条数' : '输入保留天数'}
                  className="h-10"
                />
                <span className="text-sm text-muted-foreground whitespace-nowrap">
                  {cleanType === 'count' ? '条' : '天'}
                </span>
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setIsCleanDialogOpen(false)}>
                取消
              </Button>
              <Button
                variant="destructive"
                onClick={handleCleanLogs}
                disabled={cleanLoading || !cleanValue || parseInt(cleanValue) <= 0}
              >
                {cleanLoading ? '清理中...' : '确定清理'}
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      ) : null}
    </div>
  );
}
