import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import Loading from "@/components/loading";
import { getSystemHealthDetail, getProviders, type Provider, type SystemHealth, type ProviderHealth, type ModelHealth } from "@/lib/api";
import { toast } from "sonner";
import { CheckCircle2, AlertCircle, XCircle, Activity, ChevronDown } from "lucide-react";

const AUTO_REFRESH_INTERVAL_MS = 10_000;
const TIMELINE_BUCKET_COUNT = 100;
type MonitorView = "providers" | "models";

interface ModelMonitorProviderItem extends ModelHealth {
  providerId: number;
  providerName: string;
}

interface ModelMonitorItem {
  name: string;
  status: ModelHealth["status"];
  providerCount: number;
  totalRequests: number;
  failedRequests: number;
  successRate: number;
  avgResponseTimeMs: number;
  lastCheck: string;
  providers: ModelMonitorProviderItem[];
  requestBlocks: ModelHealth["requestBlocks"];
}

interface TimelineBlock {
  success: boolean;
  successRate: number;
  totalRequests: number;
  failedRequests: number;
  timestamp: string;
}

// 状态指示器组件
const StatusBadge = ({ status }: { status: "healthy" | "degraded" | "unhealthy" | "unknown" }) => {
  const variants = {
    healthy: { icon: <CheckCircle2 className="size-3" />, label: "正常", className: "bg-green-500 text-white hover:bg-green-500/90" },
    degraded: { icon: <AlertCircle className="size-3" />, label: "警告", className: "bg-yellow-500 text-white hover:bg-yellow-500/90" },
    unhealthy: { icon: <XCircle className="size-3" />, label: "异常", className: "bg-red-500 text-white hover:bg-red-500/90" },
    unknown: { icon: <Activity className="size-3" />, label: "未知", className: "bg-gray-500 text-white hover:bg-gray-500/90" }
  };

  const config = variants[status];

  return (
    <Badge className={cn("flex items-center gap-1", config.className)}>
      {config.icon}
      {config.label}
    </Badge>
  );
};

// 格式化响应时间
const formatResponseTime = (ms: number): string => {
  if (ms < 1) return "< 1ms";
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${Math.round(ms)}ms`;
};

type ConsoleLatencyState =
  | { status: "na" }
  | { status: "loading" }
  | { status: "ok"; ms: number; checkedAt: number }
  | { status: "error"; message: string; checkedAt: number };

const withNoCachePing = (rawUrl: string): string | null => {
  const trimmed = rawUrl.trim();
  if (!trimmed) return null;
  try {
    const url = new URL(trimmed);
    url.searchParams.set("_llmio_ping", Date.now().toString());
    return url.toString();
  } catch {
    return null;
  }
};

const probeConsoleLatencyMs = async (consoleUrl: string, signal?: AbortSignal): Promise<number> => {
  const start = performance.now();
  // no-cors：跨域控制台一般没有 CORS，这里只关心“能否连通 + 耗时”，不读取响应内容
  await fetch(consoleUrl, {
    method: "GET",
    mode: "no-cors",
    cache: "no-store",
    credentials: "omit",
    redirect: "follow",
    signal,
  });
  return Math.max(0, Math.round(performance.now() - start));
};

const formatShortTime = (value: string) => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  const mm = String(date.getMonth() + 1).padStart(2, "0");
  const dd = String(date.getDate()).padStart(2, "0");
  const hh = String(date.getHours()).padStart(2, "0");
  const mi = String(date.getMinutes()).padStart(2, "0");
  const ss = String(date.getSeconds()).padStart(2, "0");
  return `${mm}/${dd} ${hh}:${mi}:${ss}`;
};

const formatAutoDisabledUntil = (value?: string) => {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";

  const diffMs = date.getTime() - Date.now();
  if (diffMs <= 0) {
    return "即将恢复";
  }

  const totalMinutes = Math.ceil(diffMs / 60000);
  if (totalMinutes < 60) {
    return `${totalMinutes} 分钟后恢复`;
  }

  const hh = String(date.getHours()).padStart(2, "0");
  const mm = String(date.getMinutes()).padStart(2, "0");
  return `预计 ${hh}:${mm} 恢复`;
};

const calcProviderSuccessRate = (provider: ProviderHealth): number => {
  const raw = typeof provider.errorRate === "number" ? provider.errorRate : 0;
  const errorRatePercent = raw <= 1 ? raw * 100 : raw;
  return Math.max(0, Math.min(100, 100 - errorRatePercent));
};

const pickSuccessRateClass = (successRate: number): string => {
  if (!Number.isFinite(successRate)) return "bg-slate-500/80 text-white";
  if (successRate >= 80) return "bg-emerald-500/90 text-white";
  if (successRate >= 60) return "bg-amber-500/90 text-white";
  return "bg-rose-500/90 text-white";
};

const mergeTimelineBlocks = (blockGroups: TimelineBlock[][], limit: number): TimelineBlock[] => {
  if (blockGroups.length === 0) return [];
  const maxLength = Math.max(...blockGroups.map((blocks) => blocks.length));
  const merged: TimelineBlock[] = [];

  for (let index = 0; index < maxLength; index++) {
    const blocks = blockGroups.map((group) => group[index]).filter((block): block is TimelineBlock => Boolean(block));
    if (blocks.length === 0) continue;
    const totalRequests = blocks.reduce((sum, block) => sum + (block.totalRequests || 0), 0);
    const failedRequests = blocks.reduce((sum, block) => sum + (block.failedRequests || 0), 0);
    const successRate = totalRequests > 0 ? ((totalRequests - failedRequests) / totalRequests) * 100 : 100;
    merged.push({
      timestamp: blocks[0].timestamp,
      success: totalRequests > 0 && failedRequests === 0,
      successRate,
      totalRequests,
      failedRequests,
    });
  }

  if (merged.length > limit) {
    return merged.slice(-limit);
  }
  return merged;
};

const mergeProviderBlocks = (models: ModelHealth[], limit: number) => {
  return mergeTimelineBlocks(models.map((model) => model.requestBlocks), limit);
};

const mergeModelBlocks = (providers: ModelMonitorProviderItem[], limit: number) => {
  return mergeTimelineBlocks(providers.map((model) => model.requestBlocks), limit);
};

const buildBlockSlots = (blocks: TimelineBlock[], limit: number) => {
  if (blocks.length >= limit) return blocks;
  const padding = Array.from({ length: limit - blocks.length }, () => null);
  return [...padding, ...blocks];
};

const pickTimelineBlockClass = (block: TimelineBlock | null): string => {
  if (!block || block.totalRequests <= 0) return "bg-muted/70";
  if (block.successRate >= 90) return "bg-emerald-400";
  if (block.successRate >= 60) return "bg-amber-400";
  return "bg-rose-400";
};

const formatTimelineBlockTitle = (block: TimelineBlock) => {
  if (block.totalRequests <= 0) {
    return `${formatShortTime(block.timestamp)} · 无请求`;
  }
  return `${formatShortTime(block.timestamp)} · 成功率 ${block.successRate.toFixed(0)}% · 请求 ${block.totalRequests}`;
};

const TimelineBlockCell = ({ block }: { block: TimelineBlock | null }) => {
  if (!block) {
    return <div className="h-5 rounded-[3px] bg-muted/70" />;
  }

  return (
    <Tooltip delayDuration={0}>
      <TooltipTrigger asChild>
        <div className={cn("h-5 rounded-[3px]", pickTimelineBlockClass(block))} />
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={4}>
        {formatTimelineBlockTitle(block)}
      </TooltipContent>
    </Tooltip>
  );
};

const pickWorstStatus = (items: ModelMonitorProviderItem[]): ModelHealth["status"] => {
  if (items.some((item) => item.status === "unhealthy")) return "unhealthy";
  if (items.some((item) => item.status === "degraded")) return "degraded";
  if (items.some((item) => item.status === "healthy")) return "healthy";
  return "unknown";
};

const buildModelMonitorItems = (providerList: ProviderHealth[]): ModelMonitorItem[] => {
  const grouped = new Map<string, ModelMonitorProviderItem[]>();
  for (const provider of providerList) {
    for (const model of provider.models) {
      const name = model.modelName || model.providerModel || "未命名模型";
      const item: ModelMonitorProviderItem = {
        ...model,
        providerId: provider.id,
        providerName: provider.name,
      };
      grouped.set(name, [...(grouped.get(name) ?? []), item]);
    }
  }

  return Array.from(grouped.entries())
    .map(([name, items]) => {
      const totalRequests = items.reduce((sum, item) => sum + item.totalRequests, 0);
      const failedRequests = items.reduce((sum, item) => sum + item.failedRequests, 0);
      const visibleProviders = items.filter((item) => item.totalRequests > 0);
      const weightedLatency = items.reduce((sum, item) => sum + item.avgResponseTimeMs * Math.max(item.totalRequests, 1), 0);
      const latencyWeight = items.reduce((sum, item) => sum + Math.max(item.totalRequests, 1), 0);
      const latest = items.reduce((current, item) => {
        const currentTime = new Date(current).getTime();
        const nextTime = new Date(item.lastCheck).getTime();
        if (Number.isNaN(currentTime)) return item.lastCheck;
        if (Number.isNaN(nextTime)) return current;
        return nextTime > currentTime ? item.lastCheck : current;
      }, "");
      const requestBlocks = mergeModelBlocks(items, TIMELINE_BUCKET_COUNT);

      return {
        name,
        status: pickWorstStatus(items),
        providerCount: new Set(visibleProviders.map((item) => item.providerId)).size,
        totalRequests,
        failedRequests,
        successRate: totalRequests > 0 ? ((totalRequests - failedRequests) / totalRequests) * 100 : 100,
        avgResponseTimeMs: latencyWeight > 0 ? weightedLatency / latencyWeight : 0,
        lastCheck: latest,
        providers: visibleProviders.sort((a, b) => a.providerName.localeCompare(b.providerName)),
        requestBlocks,
      } satisfies ModelMonitorItem;
    })
    .sort((a, b) => b.totalRequests - a.totalRequests || a.name.localeCompare(b.name));
};

const HealthProviderRow = ({
  provider,
  expanded,
  onToggle,
}: {
  provider: ProviderHealth;
  expanded: boolean;
  onToggle: () => void;
}) => {
  const successRate = calcProviderSuccessRate(provider);
  const blocks = mergeProviderBlocks(provider.models, TIMELINE_BUCKET_COUNT);
  const slots = buildBlockSlots(blocks, TIMELINE_BUCKET_COUNT);
  const startTime = blocks.length > 0 ? formatShortTime(blocks[0].timestamp) : "-";
  const endTime = blocks.length > 0 ? formatShortTime(blocks[blocks.length - 1].timestamp) : "-";
  const rateClass = pickSuccessRateClass(successRate);

  return (
    <div className="rounded-2xl border border-border/60 bg-card/70 px-4 py-3">
      <div className="flex flex-col gap-3 md:flex-row md:items-center">
        <div className="w-full md:w-56 shrink-0">
          <div className="flex items-center justify-between gap-2">
            <span className="rounded-full border border-border/60 bg-background/70 px-3 py-1 text-xs font-semibold">
              {provider.name.toUpperCase()}
            </span>
            <span className={cn("w-12 shrink-0 rounded-full px-2.5 py-1 text-center text-xs font-semibold", rateClass)}>
              {successRate.toFixed(0)}%
            </span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
            <span>{provider.models.length} 个模型 / {provider.totalRequests.toLocaleString()} 次请求</span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-6 px-2 text-[11px] font-medium"
              onClick={onToggle}
              aria-expanded={expanded}
            >
              {expanded ? "收起模型" : "查看模型"}
              <ChevronDown className={cn("ml-1 size-3 transition-transform", expanded ? "rotate-180" : "")} />
            </Button>
          </div>
        </div>
        <div className="flex-1 min-w-0">
          <div className="grid gap-[2px]" style={{ gridTemplateColumns: `repeat(${TIMELINE_BUCKET_COUNT}, minmax(0, 1fr))` }}>
            {slots.map((block, idx) => (
              <TimelineBlockCell key={idx} block={block} />
            ))}
          </div>
          <div className="mt-2 flex items-center justify-between text-[10px] text-muted-foreground">
            <span>{startTime}</span>
            <span>{endTime}</span>
          </div>
        </div>
      </div>
      {expanded && (
        <div className="mt-3 border-t border-border/50 pt-3">
          {provider.models.length > 0 ? (
            <div className="space-y-2">
              {provider.models.map((model) => {
                const modelRate = typeof model.successRate === "number" ? model.successRate : 0;
                const modelRateClass = pickSuccessRateClass(modelRate);
                return (
                  <div
                    key={`${provider.id}-${model.modelName}-${model.providerModel}`}
                    className="rounded-xl border border-border/60 bg-background/70 px-3 py-2"
                  >
                    <div className="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
                      <div className="min-w-0">
                        <div className="text-sm font-semibold truncate">{model.modelName || "未命名模型"}</div>
                        <div className="text-[11px] text-muted-foreground truncate">
                          提供商模型：{model.providerModel || "-"}
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-2 text-[11px]">
                        <StatusBadge status={model.status} />
                        {model.autoDisabledUntil ? (
                          <span className="rounded-full bg-amber-500/15 px-2 py-1 text-[11px] font-semibold text-amber-700 ring-1 ring-amber-500/20">
                            自动熔断中
                          </span>
                        ) : null}
                        <span className={cn("rounded-full px-2 py-1 text-[11px] font-semibold", modelRateClass)}>
                          {modelRate.toFixed(0)}%
                        </span>
                        <span className="text-muted-foreground">请求 {model.totalRequests.toLocaleString()}</span>
                        <span className="text-muted-foreground">均值 {formatResponseTime(model.avgResponseTimeMs)}</span>
                        <span className="text-muted-foreground">最近 {formatShortTime(model.lastCheck)}</span>
                      </div>
                    </div>
                    {model.autoDisabledUntil ? (
                      <div className="mt-1 text-[11px] text-amber-700/90">
                        {formatAutoDisabledUntil(model.autoDisabledUntil)}
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="rounded-xl border border-dashed border-border/60 bg-muted/20 px-3 py-4 text-center text-xs text-muted-foreground">
              暂无模型健康数据
            </div>
          )}
        </div>
      )}
    </div>
  );
};

const HealthModelRow = ({
  model,
  expanded,
  onToggle,
}: {
  model: ModelMonitorItem;
  expanded: boolean;
  onToggle: () => void;
}) => {
  const slots = buildBlockSlots(model.requestBlocks, TIMELINE_BUCKET_COUNT);
  const startTime = model.requestBlocks.length > 0 ? formatShortTime(model.requestBlocks[0].timestamp) : "-";
  const endTime = model.requestBlocks.length > 0 ? formatShortTime(model.requestBlocks[model.requestBlocks.length - 1].timestamp) : "-";
  const rateClass = pickSuccessRateClass(model.successRate);

  return (
    <div
      className="cursor-pointer rounded-2xl border border-border/60 bg-card/70 px-4 py-2.5 transition-colors hover:bg-card"
      role="button"
      tabIndex={0}
      aria-expanded={expanded}
      onClick={onToggle}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onToggle();
        }
      }}
    >
      <div className="flex flex-col gap-2.5 lg:flex-row lg:items-start">
        <div className="w-full lg:w-56 shrink-0">
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">{model.name}</div>
            </div>
            <span className={cn("w-12 shrink-0 rounded-full px-2.5 py-1 text-center text-xs font-semibold", rateClass)}>
              {model.successRate.toFixed(0)}%
            </span>
          </div>
        </div>
        <div className="flex-1 min-w-0">
          <div className="grid gap-[2px]" style={{ gridTemplateColumns: `repeat(${TIMELINE_BUCKET_COUNT}, minmax(0, 1fr))` }}>
            {slots.map((block, idx) => (
              <TimelineBlockCell key={idx} block={block} />
            ))}
          </div>
          <div className="mt-2 flex items-center justify-between text-[10px] text-muted-foreground">
            <span>{startTime}</span>
            <span>{endTime}</span>
          </div>
        </div>
      </div>
      {expanded ? (
        <div className="mt-3 grid gap-2 border-t border-border/50 pt-3 md:grid-cols-2 xl:grid-cols-3" onClick={(event) => event.stopPropagation()}>
          {model.providers.length > 0 ? (
            model.providers.map((item) => {
              const itemRate = item.totalRequests > 0 && typeof item.successRate === "number" ? item.successRate : 100;
              return (
                <div
                  key={`${item.providerId}-${item.modelName}-${item.providerModel}`}
                  className="rounded-xl border border-border/60 bg-background/70 px-3 py-2"
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <div className="truncate text-xs font-semibold">{item.providerName}</div>
                      <div className="truncate text-[11px] text-muted-foreground">{item.providerModel || "-"}</div>
                    </div>
                    <span className={cn("rounded-full px-2 py-1 text-[11px] font-semibold", pickSuccessRateClass(itemRate))}>
                      {itemRate.toFixed(0)}%
                    </span>
                  </div>
                  <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
                    <span>请求 {item.totalRequests.toLocaleString()}</span>
                    <span>均值 {formatResponseTime(item.avgResponseTimeMs)}</span>
                    {item.autoDisabledUntil ? (
                      <span className="font-semibold text-amber-700">{formatAutoDisabledUntil(item.autoDisabledUntil)}</span>
                    ) : null}
                  </div>
                </div>
              );
            })
          ) : (
            <div className="rounded-xl border border-dashed border-border/60 bg-muted/20 px-3 py-4 text-center text-xs text-muted-foreground md:col-span-2 xl:col-span-3">
              暂无有请求的提供商
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
};

export default function HealthPage() {
  const [loading, setLoading] = useState(true);
  const [health, setHealth] = useState<SystemHealth | null>(null);
  const [monitorView, setMonitorView] = useState<MonitorView>("providers");
  const [providerConsoleMap, setProviderConsoleMap] = useState<Record<number, string>>({});
  const [consoleLatencyMap, setConsoleLatencyMap] = useState<Record<number, ConsoleLatencyState>>({});
  const [expandedProviders, setExpandedProviders] = useState<Record<number, boolean>>({});
  const [expandedModels, setExpandedModels] = useState<Record<string, boolean>>({});
  const consoleLatencyRef = useRef<Record<number, ConsoleLatencyState>>({});
  const inFlightControllersRef = useRef<AbortController[]>([]);

  const fetchHealth = useCallback(async (showErrorToast: boolean) => {
    try {
      const data = await getSystemHealthDetail();
      setHealth(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (showErrorToast) {
        toast.error(`获取健康状态失败: ${message}`);
      }
      console.error(err);
    }
  }, []);

  const load = useCallback(async (showLoading = false) => {
    if (showLoading) {
      setLoading(true);
    }
    await fetchHealth(showLoading);
    if (showLoading) {
      setLoading(false);
    }
  }, [fetchHealth]);

  useEffect(() => {
    void load(true);
  }, [load]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      void load(false);
    }, AUTO_REFRESH_INTERVAL_MS);

    return () => window.clearInterval(timer);
  }, [load]);

  const toggleProviderExpanded = useCallback((providerId: number) => {
    setExpandedProviders((prev) => ({ ...prev, [providerId]: !prev[providerId] }));
  }, []);

  const toggleModelExpanded = useCallback((modelName: string) => {
    setExpandedModels((prev) => ({ ...prev, [modelName]: !prev[modelName] }));
  }, []);

  useEffect(() => {
    consoleLatencyRef.current = consoleLatencyMap;
  }, [consoleLatencyMap]);

  const loadProvidersForConsole = useCallback(async () => {
    try {
      const list = await getProviders();
      const map: Record<number, string> = {};
      for (const p of list as Provider[]) {
        if (p && typeof p.ID === "number" && typeof p.Console === "string") {
          map[p.ID] = p.Console;
        }
      }
      setProviderConsoleMap(map);
    } catch (err) {
      console.error(err);
      setProviderConsoleMap({});
    }
  }, []);

  const cancelConsoleProbes = useCallback(() => {
    for (const c of inFlightControllersRef.current) {
      try {
        c.abort();
      } catch {
        // ignore
      }
    }
    inFlightControllersRef.current = [];
  }, []);

  const checkConsoleLatencies = useCallback(async (providerList: ProviderHealth[]) => {
    cancelConsoleProbes();

    const minIntervalMs = 30_000;
    const now = Date.now();

    const pickPingUrl = (providerId: number) => withNoCachePing(providerConsoleMap[providerId] || "");

    const shouldProbe = (providerId: number, prev: ConsoleLatencyState | undefined) => {
      if (!pickPingUrl(providerId)) return false;
      if (!prev) return true;
      if (prev.status === "loading" || prev.status === "na") return true;
      if (prev.status === "ok" || prev.status === "error") {
        return now - prev.checkedAt >= minIntervalMs;
      }
      return true;
    };

    // 初始化状态（只把需要探测的置为 loading，避免每次刷新都频繁打控制台）
    setConsoleLatencyMap((prev) => {
      const next: Record<number, ConsoleLatencyState> = { ...prev };
      for (const p of providerList) {
        const pingUrl = pickPingUrl(p.id);
        if (!pingUrl) {
          next[p.id] = { status: "na" };
          continue;
        }
        if (shouldProbe(p.id, prev[p.id])) {
          next[p.id] = { status: "loading" };
        }
      }
      return next;
    });

    const concurrency = 3;
    let idx = 0;
    const currentMap = consoleLatencyRef.current;
    const targets = providerList.filter((p) => shouldProbe(p.id, currentMap[p.id]));
    const tasks = targets.map((p) => async () => {
      const pingUrl = pickPingUrl(p.id);
      if (!pingUrl) return;

      const controller = new AbortController();
      inFlightControllersRef.current.push(controller);
      const timeout = setTimeout(() => controller.abort(), 5000);
      try {
        const ms = await probeConsoleLatencyMs(pingUrl, controller.signal);
        const checkedAt = Date.now();
        setConsoleLatencyMap((prev) => ({ ...prev, [p.id]: { status: "ok", ms, checkedAt } }));
      } catch (err) {
        const checkedAt = Date.now();
        const message = err instanceof Error ? err.message : String(err);
        setConsoleLatencyMap((prev) => ({ ...prev, [p.id]: { status: "error", message, checkedAt } }));
      } finally {
        clearTimeout(timeout);
      }
    });

    const workers = Array.from({ length: Math.min(concurrency, tasks.length) }, async () => {
      while (idx < tasks.length) {
        const current = idx++;
        await tasks[current]();
      }
    });
    await Promise.all(workers);
  }, [cancelConsoleProbes, providerConsoleMap]);

  // 加载提供商列表（用于拿到 console URL）
  useEffect(() => {
    void loadProvidersForConsole();
  }, [loadProvidersForConsole]);

  // 当健康数据与 console 映射都准备好后，进行一次控制台延迟探测
  useEffect(() => {
    if (!health) return;
    if (Object.keys(providerConsoleMap).length === 0) return;
    void checkConsoleLatencies(health.components.providers.details);
  }, [health, providerConsoleMap, checkConsoleLatencies]);

  const modelMonitorItems = useMemo(
    () => (health ? buildModelMonitorItems(health.components.providers.details) : []),
    [health]
  );

  if (loading || !health) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loading message="加载健康状态" />
      </div>
    );
  }

  const { components } = health;
  const { providers } = components;

  return (
    <div className="h-full min-h-0 flex flex-col gap-4 p-1">
      {/* 页面头部 */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-2xl font-bold tracking-tight flex items-center gap-2">
            <Activity className="size-6" />
            健康监控
          </h2>
        </div>
        <div className="rounded-2xl border border-border/60 bg-card/80 p-1 shadow-sm">
          <div className="grid grid-cols-2 gap-1">
            <Button
              type="button"
              size="sm"
              variant={monitorView === "providers" ? "default" : "ghost"}
              className="h-8 px-3 text-xs"
              onClick={() => setMonitorView("providers")}
            >
              提供商监控
            </Button>
            <Button
              type="button"
              size="sm"
              variant={monitorView === "models" ? "default" : "ghost"}
              className="h-8 px-3 text-xs"
              onClick={() => setMonitorView("models")}
            >
              模型监控
            </Button>
          </div>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto space-y-4">
        {monitorView === "providers" ? (
          <div className="space-y-3">
            {providers.details.length > 0 ? (
              <div className="space-y-3">
                {providers.details.map((provider) => (
                  <HealthProviderRow
                    key={provider.id}
                    provider={provider}
                    expanded={Boolean(expandedProviders[provider.id])}
                    onToggle={() => toggleProviderExpanded(provider.id)}
                  />
                ))}
              </div>
            ) : (
              <div className="rounded-2xl border border-border/60 bg-card/70 py-12 text-center text-muted-foreground">
                暂无提供商数据
              </div>
            )}
          </div>
        ) : (
          <div className="space-y-3">
            {modelMonitorItems.length > 0 ? (
              modelMonitorItems.map((model) => (
                <HealthModelRow
                  key={model.name}
                  model={model}
                  expanded={Boolean(expandedModels[model.name])}
                  onToggle={() => toggleModelExpanded(model.name)}
                />
              ))
            ) : (
              <div className="rounded-2xl border border-border/60 bg-card/70 py-12 text-center text-muted-foreground">
                暂无模型健康数据
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
