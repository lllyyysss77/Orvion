import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import {
  Activity,
  Copy,
  Eye,
  EyeOff,
  LoaderCircle,
  MapPin,
  Network,
  Pencil,
  Plus,
  Search,
  Square,
  Trash2,
} from "lucide-react";
import Loading from "@/components/loading";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  checkProxyRegion,
  configAPI,
  createProxy,
  deleteProxy,
  getProxies,
  updateProxy,
  type Proxy,
  type ProxyRegionCheckResult,
} from "@/lib/api";

const isValidProxyURL = (value: string) => {
  try {
    const parsed = new URL(value.trim().replace(/^socket5:\/\//i, "socks5://"));
    return ["http:", "socks5:"].includes(parsed.protocol.toLowerCase()) && Boolean(parsed.host);
  } catch {
    return false;
  }
};

const maskProxyURL = (value: string) => {
  try {
    const parsed = new URL(value);
    if (!parsed.password) return value;
    const username = decodeURIComponent(parsed.username);
    return `${parsed.protocol}//${username}:***@${parsed.host}${parsed.pathname}${parsed.search}${parsed.hash}`;
  } catch {
    return value.replace(/(\/\/[^:/@]+:)[^@]+@/, "$1***@");
  }
};

const proxyProtocol = (value: string) => value.split(":", 1)[0]?.toLowerCase() || "";

const countryFlag = (countryCode: string) => {
  const code = countryCode.trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(code)) return "";
  return [...code].map((char) => String.fromCodePoint(127397 + char.charCodeAt(0))).join("");
};

const formatCheckedAt = (value: string | null | undefined, now: number) => {
  if (!value) return "从未检查";
  const checkedAt = new Date(value);
  const elapsed = Math.max(0, now - checkedAt.getTime());
  if (elapsed < 60_000) return "刚刚";
  if (elapsed < 60 * 60_000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
  const time = checkedAt.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false });
  const today = new Date(now);
  if (checkedAt.toDateString() === today.toDateString()) return `今天 ${time}`;
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (checkedAt.toDateString() === yesterday.toDateString()) return `昨天 ${time}`;
  return `${checkedAt.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })} ${time}`;
};

const isStale = (value: string | null | undefined, now: number) => !value || now - new Date(value).getTime() > 24 * 60 * 60_000;

const formatBytes = (bytes: number | undefined) => {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  return `${value >= 10 || unitIndex === 0 ? Math.round(value) : value.toFixed(1)} ${units[unitIndex]}`;
};

type AvailabilityFilter = "all" | "available" | "unavailable" | "unchecked";
type SortMode = "default" | "name" | "latency" | "checked" | "usage";

export default function ProxiesPage() {
  const [proxies, setProxies] = useState<Proxy[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<Proxy | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Proxy | null>(null);
  const [name, setName] = useState("");
  const [proxyURL, setProxyURL] = useState("");
  const [checkingIDs, setCheckingIDs] = useState<Set<number>>(new Set());
  const [revealedIDs, setRevealedIDs] = useState<Set<number>>(new Set());
  const [batchProgress, setBatchProgress] = useState({ running: false, completed: 0, total: 0 });
  const [autoCheckInterval, setAutoCheckInterval] = useState("0");
  const [autoCheckSaving, setAutoCheckSaving] = useState(false);
  const [search, setSearch] = useState("");
  const [protocol, setProtocol] = useState("all");
  const [region, setRegion] = useState("all");
  const [availability, setAvailability] = useState<AvailabilityFilter>("all");
  const [sortMode, setSortMode] = useState<SortMode>("default");
  const [now, setNow] = useState(Date.now());
  const batchControllerRef = useRef<AbortController | null>(null);
  const revealTimersRef = useRef<Map<number, number>>(new Map());

  const fetchProxies = useCallback(async () => {
    setLoading(true);
    try {
      setProxies(await getProxies());
    } catch (error) {
      toast.error(`获取代理列表失败: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchAutoCheckConfig = useCallback(async () => {
    try {
      const response = await configAPI.getConfig("proxy_health_check");
      if (!response.value) {
        setAutoCheckInterval("0");
        return;
      }
      const config = JSON.parse(response.value) as { enabled?: boolean; interval_minutes?: number };
      setAutoCheckInterval(config.enabled && [15, 30, 60].includes(config.interval_minutes || 0) ? String(config.interval_minutes) : "0");
    } catch (error) {
      toast.error(`获取自动检查配置失败: ${error instanceof Error ? error.message : String(error)}`);
    }
  }, []);

  useEffect(() => {
    void fetchProxies();
    void fetchAutoCheckConfig();
  }, [fetchAutoCheckConfig, fetchProxies]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000);
    const revealTimers = revealTimersRef.current;
    return () => {
      window.clearInterval(timer);
      batchControllerRef.current?.abort();
      revealTimers.forEach((timerID) => window.clearTimeout(timerID));
      revealTimers.clear();
    };
  }, []);

  const regionOptions = useMemo(() => Array.from(new Set(proxies
    .map((proxy) => proxy.ExitCountry || proxy.ExitCountryCode || proxy.ExitRegion || "")
    .filter(Boolean))).sort((a, b) => a.localeCompare(b, "zh-CN")), [proxies]);

  const visibleProxies = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    const result = proxies.filter((proxy) => {
      const regionText = [proxy.ExitCountry, proxy.ExitCountryCode, proxy.ExitRegion, proxy.ExitCity].filter(Boolean).join(" ").toLowerCase();
      if (keyword && !proxy.Name.toLowerCase().includes(keyword)) return false;
      if (protocol !== "all" && proxyProtocol(proxy.ProxyURL) !== protocol) return false;
      if (region !== "all" && !regionText.includes(region.toLowerCase())) return false;
      const checked = Boolean(proxy.RegionCheckedAt);
      const healthChecked = checked && (proxy.CheckTotal || 0) > 0;
      if (availability === "available" && (!healthChecked || proxy.HealthStatus !== 1)) return false;
      if (availability === "unavailable" && (!healthChecked || proxy.HealthStatus === 1)) return false;
      if (availability === "unchecked" && healthChecked) return false;
      return true;
    });
    result.sort((a, b) => {
      if (sortMode === "name") return a.Name.localeCompare(b.Name, "zh-CN");
      if (sortMode === "latency") return (a.LatencyMS || Number.MAX_SAFE_INTEGER) - (b.LatencyMS || Number.MAX_SAFE_INTEGER);
      if (sortMode === "checked") return new Date(b.RegionCheckedAt || 0).getTime() - new Date(a.RegionCheckedAt || 0).getTime();
      if (sortMode === "usage") return (b.UsageCount || 0) - (a.UsageCount || 0);
      return a.ID - b.ID;
    });
    return result;
  }, [availability, protocol, proxies, region, search, sortMode]);

  const openCreate = () => {
    setEditing(null);
    setName("");
    setProxyURL("");
    setDialogOpen(true);
  };

  const openEdit = (proxy: Proxy) => {
    setEditing(proxy);
    setName(proxy.Name);
    setProxyURL(proxy.ProxyURL);
    setDialogOpen(true);
  };

  const save = async () => {
    const trimmedName = name.trim();
    const trimmedURL = proxyURL.trim();
    if (!trimmedName) {
      toast.error("代理名称不能为空");
      return;
    }
    if (!isValidProxyURL(trimmedURL)) {
      toast.error("代理地址仅支持 http 或 socks5");
      return;
    }
    setSaving(true);
    try {
      if (editing) {
        await updateProxy(editing.ID, { name: trimmedName, proxy_url: trimmedURL });
        toast.success(`代理 ${trimmedName} 更新成功`);
      } else {
        await createProxy({ name: trimmedName, proxy_url: trimmedURL });
        toast.success(`代理 ${trimmedName} 创建成功`);
      }
      setDialogOpen(false);
      await fetchProxies();
    } catch (error) {
      toast.error(`${editing ? "更新" : "创建"}代理失败: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!pendingDelete) return;
    try {
      await deleteProxy(pendingDelete.ID);
      toast.success(`代理 ${pendingDelete.Name} 已删除`);
      setPendingDelete(null);
      await fetchProxies();
    } catch (error) {
      toast.error(`删除代理失败: ${error instanceof Error ? error.message : String(error)}`);
    }
  };

  const applyCheckResult = useCallback((proxyID: number, result: ProxyRegionCheckResult) => {
    setProxies((current) => current.map((item) => item.ID === proxyID ? {
      ...item,
      ExitIP: result.ip,
      ExitCountry: result.country,
      ExitCountryCode: result.country_code,
      ExitRegion: result.region,
      ExitCity: result.city,
      RegionCheckedAt: result.checked_at,
      RegionCheckError: result.error || "",
      HealthStatus: result.available ? 1 : 0,
      LatencyMS: result.latency_ms,
      SuccessRate: result.success_rate,
      CheckSuccesses: result.successes,
      CheckTotal: result.total,
    } : item));
    setNow(Date.now());
  }, []);

  const checkOne = useCallback(async (proxy: Proxy, signal?: AbortSignal, notify = true) => {
    setCheckingIDs((current) => new Set(current).add(proxy.ID));
    try {
      const result = await checkProxyRegion(proxy.ID, signal);
      applyCheckResult(proxy.ID, result);
      if (notify) {
        if (result.available) toast.success(`${proxy.Name} 检查完成，延迟 ${result.latency_ms} ms`);
        else toast.error(`${proxy.Name} 不可用`);
      }
      return result.available;
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return false;
      if (notify) toast.error(`代理 ${proxy.Name} 检查失败`);
      return false;
    } finally {
      setCheckingIDs((current) => {
        const next = new Set(current);
        next.delete(proxy.ID);
        return next;
      });
    }
  }, [applyCheckResult]);

  const checkAll = async () => {
    if (batchProgress.running || proxies.length === 0) return;
    const controller = new AbortController();
    batchControllerRef.current = controller;
    setBatchProgress({ running: true, completed: 0, total: proxies.length });
    let cursor = 0;
    let completed = 0;
    let availableCount = 0;
    const worker = async () => {
      while (!controller.signal.aborted) {
        const index = cursor++;
        if (index >= proxies.length) return;
        if (await checkOne(proxies[index], controller.signal, false)) availableCount++;
        if (!controller.signal.aborted) {
          completed++;
          setBatchProgress({ running: true, completed, total: proxies.length });
        }
      }
    };
    await Promise.all(Array.from({ length: Math.min(3, proxies.length) }, worker));
    const stopped = controller.signal.aborted;
    batchControllerRef.current = null;
    setBatchProgress({ running: false, completed, total: proxies.length });
    if (stopped) toast.info(`已停止检查，完成 ${completed} / ${proxies.length}`);
    else toast.success(`检查完成，${availableCount} / ${proxies.length} 个节点可用`);
  };

  const saveAutoCheckConfig = async () => {
    setAutoCheckSaving(true);
    try {
      const interval = Number(autoCheckInterval);
      await configAPI.updateConfig("proxy_health_check", {
        enabled: interval > 0,
        interval_minutes: interval > 0 ? interval : 30,
        concurrency: 3,
      });
      toast.success(interval > 0 ? `已开启每 ${interval} 分钟自动检查` : "已关闭自动健康检查");
    } catch (error) {
      toast.error(`保存自动检查配置失败: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      setAutoCheckSaving(false);
    }
  };

  const copyProxyURL = async (proxy: Proxy) => {
    try {
      await navigator.clipboard.writeText(proxy.ProxyURL);
      toast.success(`已复制 ${proxy.Name} 的代理地址`);
    } catch (error) {
      toast.error(`复制失败: ${error instanceof Error ? error.message : String(error)}`);
    }
  };

  if (loading) return <Loading message="加载代理列表..." className="h-full min-h-0" />;

  return (
    <div className="flex h-full min-h-0 flex-col gap-4">
      <section className="overflow-hidden rounded-3xl border border-border/60 bg-gradient-to-br from-card via-card to-primary/[0.06] p-5 shadow-[0_16px_40px_rgba(98,71,47,0.07)]">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex size-11 shrink-0 items-center justify-center rounded-2xl bg-primary/10 text-primary ring-1 ring-primary/15">
              <Network className="size-5" />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="text-2xl font-semibold tracking-tight">代理列表</h1>
              </div>
              <p className="mt-1 text-sm text-muted-foreground">统一维护提供商访问上游时使用的代理，实时掌握出口地区与健康状态。</p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            {batchProgress.running ? (
              <Button variant="outline" className="rounded-xl bg-background/70" onClick={() => batchControllerRef.current?.abort()}>
                <Square className="size-4" />
                停止检查 {batchProgress.completed}/{batchProgress.total}
              </Button>
            ) : (
              <Button variant="outline" className="rounded-xl bg-background/70" onClick={() => void checkAll()} disabled={proxies.length === 0}>
                <Activity className="size-4" />
                检查全部
              </Button>
            )}
            <Button className="rounded-xl shadow-sm" onClick={openCreate}>
              <Plus className="size-4" />
              新增代理
            </Button>
          </div>
        </div>

      </section>

      {batchProgress.running && (
        <div className="h-1 overflow-hidden rounded-full bg-muted" aria-label={`检查进度 ${batchProgress.completed}/${batchProgress.total}`}>
          <div className="h-full bg-primary transition-[width]" style={{ width: `${batchProgress.total ? batchProgress.completed / batchProgress.total * 100 : 0}%` }} />
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-4 rounded-2xl border border-border/60 bg-card/75 px-4 py-3.5 shadow-sm">
        <div className="flex items-start gap-3">
          <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600 ring-1 ring-emerald-100 dark:bg-emerald-400/10 dark:text-emerald-300 dark:ring-emerald-400/20"><Activity className="size-4" /></div>
          <div>
          <div className="flex items-center gap-2 text-sm font-medium">自动健康检查<Badge variant="outline" className="rounded-full px-2 py-0 text-[10px]">{autoCheckInterval === "0" ? "已关闭" : `每 ${autoCheckInterval} 分钟`}</Badge></div>
          <p className="mt-1 text-xs text-muted-foreground">后台最多 3 个并发，仅在可用状态变化时写入通知。</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Select value={autoCheckInterval} onValueChange={setAutoCheckInterval}>
            <SelectTrigger className="w-36 rounded-xl"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="0">关闭</SelectItem>
              <SelectItem value="15">每 15 分钟</SelectItem>
              <SelectItem value="30">每 30 分钟</SelectItem>
              <SelectItem value="60">每 60 分钟</SelectItem>
            </SelectContent>
          </Select>
          <Button variant="outline" className="rounded-xl" onClick={() => void saveAutoCheckConfig()} disabled={autoCheckSaving}>
            {autoCheckSaving ? "保存中..." : "保存"}
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2 rounded-2xl border border-border/60 bg-card/70 p-3 shadow-sm">
        <div className="mr-1 flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <Search className="size-4 text-primary" />
          <span>筛选节点</span>
        </div>
        <div className="relative min-w-56 flex-1 sm:max-w-80">
          <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索节点名称" className="h-9 rounded-xl border-border/60 bg-background/70 pl-9" />
        </div>
        <Select value={protocol} onValueChange={setProtocol}>
          <SelectTrigger className="w-32 rounded-xl bg-background/70"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部协议</SelectItem>
            <SelectItem value="http">HTTP</SelectItem>
            <SelectItem value="socks5">SOCKS5</SelectItem>
          </SelectContent>
        </Select>
        <Select value={region} onValueChange={setRegion}>
          <SelectTrigger className="w-36 rounded-xl bg-background/70"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部地区</SelectItem>
            {regionOptions.map((item) => <SelectItem key={item} value={item}>{item}</SelectItem>)}
          </SelectContent>
        </Select>
        <Select value={availability} onValueChange={(value) => setAvailability(value as AvailabilityFilter)}>
          <SelectTrigger className="w-36 rounded-xl bg-background/70"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">全部状态</SelectItem>
            <SelectItem value="available">可用</SelectItem>
            <SelectItem value="unavailable">不可用</SelectItem>
            <SelectItem value="unchecked">未检查</SelectItem>
          </SelectContent>
        </Select>
        <Select value={sortMode} onValueChange={(value) => setSortMode(value as SortMode)}>
          <SelectTrigger className="w-36 rounded-xl bg-background/70"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="default">默认排序</SelectItem>
            <SelectItem value="name">按名称</SelectItem>
            <SelectItem value="latency">按延迟</SelectItem>
            <SelectItem value="checked">按检查时间</SelectItem>
            <SelectItem value="usage">按使用数量</SelectItem>
          </SelectContent>
        </Select>
        <Badge variant="outline" className="ml-auto rounded-full bg-background/70 px-2.5 py-1 text-xs">
          显示 {visibleProxies.length} / {proxies.length}
        </Badge>
      </div>

      <div className="min-h-0 flex-1 overflow-auto rounded-2xl border border-border/70 bg-card/80 shadow-[0_12px_30px_rgba(98,71,47,0.06)]">
        <Table>
          <TableHeader className="sticky top-0 z-10 bg-muted/90 backdrop-blur-sm">
            <TableRow className="hover:bg-transparent">
              <TableHead>名称</TableHead>
              <TableHead>代理地址</TableHead>
              <TableHead>出口地区</TableHead>
              <TableHead>可用性</TableHead>
              <TableHead>使用情况</TableHead>
              <TableHead>今日流量</TableHead>
              <TableHead className="w-32 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {visibleProxies.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="h-36 text-center text-muted-foreground">
                  <Network className="mx-auto mb-2 size-6" />
                  {proxies.length === 0 ? "暂无代理" : "没有匹配的代理"}
                </TableCell>
              </TableRow>
            ) : visibleProxies.map((proxy) => {
              const checked = Boolean(proxy.RegionCheckedAt);
              const healthChecked = checked && (proxy.CheckTotal || 0) > 0;
              const stale = isStale(proxy.RegionCheckedAt, now);
              const location = checked
                ? [proxy.ExitCountry, proxy.ExitRegion, proxy.ExitCity].filter((value, index, values) => value && values.indexOf(value) === index).join(" · ") || "地区未知"
                : "未检查";
              const checking = checkingIDs.has(proxy.ID);
              const revealed = revealedIDs.has(proxy.ID);
              return (
                <TableRow key={proxy.ID} className="group border-border/50 transition-colors hover:bg-primary/[0.035]">
                  <TableCell className="py-4">
                    <div className="flex min-w-40 items-center gap-3">
                      <div className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/10 transition-transform group-hover:scale-105">
                        <Network className="size-4" />
                      </div>
                      <div className="min-w-0">
                        <div className="truncate font-semibold" title={proxy.Name}>{proxy.Name}</div>
                        <Badge variant="outline" className="mt-1 rounded-full px-1.5 py-0 text-[10px] uppercase tracking-wide text-muted-foreground">
                          {proxyProtocol(proxy.ProxyURL) === "socks5" ? "SOCKS5" : "HTTP"}
                        </Badge>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex min-w-72 max-w-[480px] items-center gap-1.5 rounded-xl border border-border/50 bg-muted/25 px-2 py-1.5">
                      <code className="min-w-0 flex-1 truncate text-xs text-foreground/75" title={revealed ? proxy.ProxyURL : maskProxyURL(proxy.ProxyURL)}>
                        {revealed ? proxy.ProxyURL : maskProxyURL(proxy.ProxyURL)}
                      </code>
                      {proxy.ProxyURL.includes("@") && (
                        <Button variant="ghost" size="icon" className="size-8 shrink-0" title={revealed ? "隐藏凭据" : "临时显示凭据"} onClick={() => {
                          const timerID = revealTimersRef.current.get(proxy.ID);
                          if (timerID) window.clearTimeout(timerID);
                          revealTimersRef.current.delete(proxy.ID);
                          if (revealed) {
                            setRevealedIDs((current) => {
                              const next = new Set(current);
                              next.delete(proxy.ID);
                              return next;
                            });
                            return;
                          }
                          setRevealedIDs((current) => new Set(current).add(proxy.ID));
                          revealTimersRef.current.set(proxy.ID, window.setTimeout(() => {
                            setRevealedIDs((current) => {
                              const next = new Set(current);
                              next.delete(proxy.ID);
                              return next;
                            });
                            revealTimersRef.current.delete(proxy.ID);
                          }, 10_000));
                        }}>
                          {revealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                        </Button>
                      )}
                      <Button variant="ghost" size="icon" className="size-8 shrink-0" title="复制完整代理地址" onClick={() => void copyProxyURL(proxy)}>
                        <Copy className="size-4" />
                      </Button>
                    </div>
                  </TableCell>
                  <TableCell className="py-4">
                    <div className="min-w-32" title={proxy.ExitIP || undefined}>
                      <div className="flex items-center gap-1.5">
                        <MapPin className="size-3.5 shrink-0 text-primary" />
                        {proxy.ExitCountryCode && <span>{countryFlag(proxy.ExitCountryCode)}</span>}
                        <span className="max-w-48 truncate text-sm">{location}</span>
                      </div>
                      {proxy.ExitIP && <div className="mt-0.5 font-mono text-xs text-muted-foreground">{proxy.ExitIP}</div>}
                    </div>
                  </TableCell>
                  <TableCell className="py-4">
                    <div className="min-w-32 space-y-1">
                      <div className="flex items-center gap-2">
                        <Badge className={!healthChecked ? "rounded-full" : proxy.HealthStatus === 1 ? "rounded-full border-emerald-200 bg-emerald-50 text-emerald-700 hover:bg-emerald-50 dark:border-emerald-400/20 dark:bg-emerald-400/10 dark:text-emerald-300" : "rounded-full"} variant={!healthChecked ? "outline" : proxy.HealthStatus === 1 ? "default" : "destructive"}>
                          {!healthChecked ? "未检查" : proxy.HealthStatus === 1 ? "可用" : "不可用"}
                        </Badge>
                        {healthChecked && proxy.HealthStatus === 1 && <span className="text-xs tabular-nums">{proxy.LatencyMS} ms</span>}
                      </div>
                      {healthChecked && (
                        <div className="text-xs text-muted-foreground">{proxy.CheckSuccesses}/{proxy.CheckTotal} 成功 · {Math.round(proxy.SuccessRate || 0)}%</div>
                      )}
                      <div className={`text-xs ${stale ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground"}`} title={proxy.RegionCheckedAt ? new Date(proxy.RegionCheckedAt).toLocaleString("zh-CN") : undefined}>
                        {formatCheckedAt(proxy.RegionCheckedAt, now)}{stale && checked ? " · 建议复查" : ""}
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="py-4">
                    <Badge className="rounded-full" variant={proxy.UsageCount ? "default" : "outline"}>
                      {proxy.UsageCount ? `${proxy.UsageCount} 个提供商` : "未使用"}
                    </Badge>
                  </TableCell>
                  <TableCell className="py-4 font-mono text-xs tabular-nums text-foreground/75">{formatBytes(proxy.TrafficBytes)}</TableCell>
                  <TableCell className="py-4">
                    <div className="flex justify-end gap-1 opacity-70 transition-opacity group-hover:opacity-100">
                      <Button variant="ghost" size="icon" className="size-8 rounded-lg hover:bg-primary/10 hover:text-primary" title="检查节点" disabled={checking} onClick={() => void checkOne(proxy)}>
                        {checking ? <LoaderCircle className="size-4 animate-spin" /> : <MapPin className="size-4" />}
                      </Button>
                      <Button variant="ghost" size="icon" className="size-8 rounded-lg hover:bg-primary/10 hover:text-primary" title="编辑代理" onClick={() => openEdit(proxy)}>
                        <Pencil className="size-4" />
                      </Button>
                      <Button variant="ghost" size="icon" className="size-8 rounded-lg hover:bg-destructive/10 hover:text-destructive" title="删除代理" onClick={() => setPendingDelete(proxy)}>
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing ? "编辑代理" : "新增代理"}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label htmlFor="proxy-name">名称</Label>
              <Input id="proxy-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：香港节点" />
            </div>
            <div className="space-y-2">
              <Label htmlFor="proxy-url">代理地址</Label>
              <Input id="proxy-url" value={proxyURL} onChange={(event) => setProxyURL(event.target.value)} placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080" />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
            <Button onClick={() => void save()} disabled={saving}>{saving ? "保存中..." : "保存"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={Boolean(pendingDelete)} onOpenChange={(value) => !value && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除代理</AlertDialogTitle>
            <AlertDialogDescription>
              确认删除“{pendingDelete?.Name}”？正在被提供商使用的代理无法删除。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => void remove()}>删除</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
