import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from "@/components/ui/alert-dialog";
import Loading from "@/components/loading";
import { toast } from "sonner";
import {
  deleteCodexSubscription,
  getCodexOAuthStatus,
  getCodexSubscriptionModels,
  getCodexSubscriptionTeamQuota,
  getCodexSubscriptions,
  startCodexOAuth,
  type CodexModel,
  type CodexQuotaWindow,
  type CodexSubscription,
  type CodexTeamQuota
} from "@/lib/api";

const formatTime = (value?: string) => {
  if (!value) return "-";
  const trimmed = value.toString().trim();
  if (!trimmed) return "-";
  const numeric = /^[0-9]+$/.test(trimmed);
  if (numeric) {
    const num = Number(trimmed);
    const ms = trimmed.length >= 13 ? num : num * 1000;
    const date = new Date(ms);
    if (!Number.isNaN(date.getTime())) {
      return date.toLocaleString();
    }
  }
  const date = new Date(trimmed);
  if (Number.isNaN(date.getTime())) return trimmed;
  return date.toLocaleString();
};

const formatDisplayName = (sub: CodexSubscription) =>
  sub.email || sub.account_id || sub.id;

const formatRemainingPercent = (window: CodexQuotaWindow) => {
  const raw = window.remaining_percent;
  if (typeof raw !== "number" || Number.isNaN(raw)) return "--";
  const clamped = Math.max(0, Math.min(100, raw));
  return `${Math.round(clamped)}%`;
};

const quotaBarClassName = (window: CodexQuotaWindow) => {
  const raw = window.remaining_percent;
  if (typeof raw !== "number" || Number.isNaN(raw)) {
    return "bg-amber-500";
  }
  const clamped = Math.max(0, Math.min(100, raw));
  if (clamped >= 80) return "bg-green-500";
  if (clamped >= 50) return "bg-amber-500";
  return "bg-red-500";
};

const quotaBarWidth = (window: CodexQuotaWindow) => {
  const raw = window.remaining_percent;
  if (typeof raw !== "number" || Number.isNaN(raw)) return "0%";
  const clamped = Math.max(0, Math.min(100, raw));
  return `${Math.round(clamped)}%`;
};

export default function CodexOfficialPage() {
  const [subscriptions, setSubscriptions] = useState<CodexSubscription[]>([]);
  const [loading, setLoading] = useState(true);
  const [oauthState, setOauthState] = useState<string | null>(null);
  const [modelDialogOpen, setModelDialogOpen] = useState(false);
  const [modelLoading, setModelLoading] = useState(false);
  const [activeSubscription, setActiveSubscription] = useState<CodexSubscription | null>(null);
  const [models, setModels] = useState<CodexModel[]>([]);
  const [quotaDialogOpen, setQuotaDialogOpen] = useState(false);
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [quotaSubscription, setQuotaSubscription] = useState<CodexSubscription | null>(null);
  const [quota, setQuota] = useState<CodexTeamQuota | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<CodexSubscription | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const pollRef = useRef<number | null>(null);

  const sortedModels = useMemo(
    () => [...models].sort((a, b) => a.id.localeCompare(b.id)),
    [models]
  );

  const fetchSubscriptions = async () => {
    setLoading(true);
    try {
      const list = await getCodexSubscriptions();
      setSubscriptions(list);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "获取 Codex 订阅失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchSubscriptions();
  }, []);

  useEffect(() => {
    return () => {
      if (pollRef.current) {
        window.clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, []);

  useEffect(() => {
    const handler = (event: MessageEvent) => {
      if (event?.data?.type !== "codex-oauth-callback") return;
      if (!oauthState) return;
      void refreshOAuthStatus(oauthState, true);
    };
    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
  }, [oauthState]);

  const refreshOAuthStatus = async (state: string, showToast: boolean) => {
    try {
      const result = await getCodexOAuthStatus(state);
      if (result.status === "ok") {
        if (pollRef.current) {
          window.clearInterval(pollRef.current);
          pollRef.current = null;
        }
        if (showToast) {
          toast.success("Codex 订阅已添加");
        }
        await fetchSubscriptions();
      }
      if (result.status === "error" && showToast) {
        toast.error(result.message || "Codex 授权失败");
      }
    } catch (error) {
      console.error(error);
      if (showToast) {
        toast.error(error instanceof Error ? error.message : "查询授权状态失败");
      }
    }
  };

  const startPolling = (state: string) => {
    if (pollRef.current) {
      window.clearInterval(pollRef.current);
    }
    pollRef.current = window.setInterval(() => {
      void refreshOAuthStatus(state, false);
    }, 1200);
  };

  const handleStartOAuth = async () => {
    try {
      const result = await startCodexOAuth();
      setOauthState(result.state);
      window.open(result.auth_url, "_blank", "width=520,height=720");
      startPolling(result.state);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "发起 Codex 授权失败");
    }
  };

  const handleOpenModels = async (sub: CodexSubscription) => {
    setActiveSubscription(sub);
    setModelDialogOpen(true);
    setModelLoading(true);
    try {
      const list = await getCodexSubscriptionModels(sub.id);
      setModels(list);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "获取可用模型失败");
      setModels([]);
    } finally {
      setModelLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      await deleteCodexSubscription(deleteTarget.id);
      toast.success("已删除订阅");
      setDeleteTarget(null);
      await fetchSubscriptions();
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "删除订阅失败");
    } finally {
      setDeleteLoading(false);
    }
  };

  const handleOpenQuota = async (sub: CodexSubscription) => {
    setQuotaSubscription(sub);
    setQuotaDialogOpen(true);
    setQuotaLoading(true);
    try {
      const result = await getCodexSubscriptionTeamQuota(sub.id);
      setQuota(result);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "查询 team 额度失败");
      setQuota(null);
    } finally {
      setQuotaLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Codex 官方</h1>
          <p className="text-sm text-muted-foreground mt-1">
            管理 Codex(ChatGPT) 订阅、授权与可用模型。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={fetchSubscriptions} disabled={loading}>
            刷新
          </Button>
          <Button onClick={handleStartOAuth}>添加订阅</Button>
        </div>
      </div>

      <div>
        <Card>
          <CardHeader className="flex-row items-center justify-between gap-3">
            <CardTitle>已有订阅</CardTitle>
            <Badge variant="secondary">{subscriptions.length} 条</Badge>
          </CardHeader>
          <CardContent>
            {loading ? (
              <div className="py-10">
                <Loading message="加载中..." />
              </div>
            ) : subscriptions.length === 0 ? (
              <div className="rounded-xl border border-dashed border-border/60 p-6 text-sm text-muted-foreground">
                暂无订阅，请点击右上角“添加订阅”完成 Codex 授权。
              </div>
            ) : (
              <div className="grid gap-4 grid-cols-1 md:grid-cols-2 xl:grid-cols-3">
                {subscriptions.map((sub) => (
                  <div
                    key={sub.id}
                    className="rounded-2xl border border-border/70 bg-card/70 p-5 space-y-4 shadow-sm"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <div className="text-base font-semibold leading-tight truncate" title={formatDisplayName(sub)}>
                          {formatDisplayName(sub)}
                        </div>
                        <div className="mt-1 text-xs text-muted-foreground font-mono truncate" title={sub.account_id || sub.id}>
                          {sub.account_id || sub.id}
                        </div>
                      </div>
                      {sub.plan_type ? (
                        <Badge variant="outline">{sub.plan_type}</Badge>
                      ) : (
                        <Badge variant="secondary">unknown</Badge>
                      )}
                    </div>

                    <div className="rounded-xl border border-border/60 bg-background/60 p-3.5 space-y-2 text-sm">
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-muted-foreground">重置时间</span>
                        <span className="text-right font-medium">{formatTime(sub.subscription_active_until)}</span>
                      </div>
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-muted-foreground">最近刷新</span>
                        <span className="text-right font-medium">{formatTime(sub.last_refresh)}</span>
                      </div>
                      <div className="flex items-center justify-between gap-3">
                        <span className="text-muted-foreground">过期时间</span>
                        <span className="text-right font-medium">{formatTime(sub.expired)}</span>
                      </div>
                    </div>

                    <div className="grid grid-cols-2 gap-2 pt-1">
                      <Button
                        size="default"
                        variant="outline"
                        className="w-full"
                        onClick={() => handleOpenQuota(sub)}
                      >
                        Team 额度
                      </Button>
                      <Button
                        size="default"
                        variant="outline"
                        className="w-full"
                        onClick={() => handleOpenModels(sub)}
                      >
                        可用模型
                      </Button>
                      <Button
                        size="default"
                        variant="destructive"
                        className="col-span-2 w-full"
                        onClick={() => setDeleteTarget(sub)}
                      >
                        删除订阅
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Dialog open={modelDialogOpen} onOpenChange={setModelDialogOpen}>
        <DialogContent className="max-w-2xl max-h-[70vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>可用模型</DialogTitle>
            <DialogDescription>
              {activeSubscription ? formatDisplayName(activeSubscription) : "订阅"}
            </DialogDescription>
          </DialogHeader>
          {modelLoading ? (
            <div className="py-8">
              <Loading message="加载模型..." />
            </div>
          ) : sortedModels.length === 0 ? (
            <div className="text-sm text-muted-foreground">暂无可用模型。</div>
          ) : (
            <div className="space-y-2">
              {sortedModels.map((model) => (
                <div
                  key={model.id}
                  className="flex items-center justify-between rounded-lg border border-border/60 bg-muted/40 px-3 py-2 text-sm"
                >
                  <span className="font-mono text-xs">{model.id}</span>
                  <span className="text-[11px] text-muted-foreground">
                    {model.owned_by || "-"}
                  </span>
                </div>
              ))}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={quotaDialogOpen} onOpenChange={setQuotaDialogOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>Team 额度</DialogTitle>
            <DialogDescription>
              {quotaSubscription ? formatDisplayName(quotaSubscription) : "订阅"}
            </DialogDescription>
          </DialogHeader>
          {quotaLoading ? (
            <div className="py-8">
              <Loading message="查询额度..." />
            </div>
          ) : !quota ? (
            <div className="text-sm text-muted-foreground">暂无额度数据。</div>
          ) : (
            <div className="space-y-2 rounded-lg border border-border/60 bg-muted/30 p-3 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">套餐类型</span>
                <span>{quota.plan_type || "-"}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">HTTP 状态</span>
                <span>{quota.http_status}</span>
              </div>
              {Array.isArray(quota.windows) && quota.windows.length > 0 ? (
                <div className="space-y-2 pt-1">
                  {quota.windows.map((window) => (
                    <div key={window.id} className="space-y-1 rounded-md border border-border/50 bg-background/40 p-2">
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-xs font-medium">{window.label || window.id}</span>
                        <div className="flex items-center gap-2 text-xs text-muted-foreground">
                          <span className="font-semibold text-foreground">{formatRemainingPercent(window)}</span>
                          <span>{formatTime(window.reset_at || window.reset_label)}</span>
                        </div>
                      </div>
                      <div className="h-2 w-full rounded-full bg-muted overflow-hidden">
                        <div
                          className={`h-2 ${quotaBarClassName(window)}`}
                          style={{ width: quotaBarWidth(window) }}
                        />
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <>
                  <div className="flex items-center justify-between">
                    <span className="text-muted-foreground">请求额度</span>
                    <span>{quota.request_remaining ?? "-"} / {quota.request_limit ?? "-"}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-muted-foreground">Token 额度</span>
                    <span>{quota.token_remaining ?? "-"} / {quota.token_limit ?? "-"}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-muted-foreground">重置时间</span>
                    <span>{formatTime(quota.reset_at || quota.reset_time)}</span>
                  </div>
                </>
              )}
              {quota.message && (
                <div className="text-xs text-amber-600 break-all">提示：{quota.message}</div>
              )}
              <div className="text-[11px] text-muted-foreground break-all">
                来源：{quota.source || "-"}
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除订阅？</AlertDialogTitle>
            <AlertDialogDescription>
              删除后将移除本地凭据文件，需重新授权才能使用。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteLoading}>取消</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete} disabled={deleteLoading}>
              {deleteLoading ? "删除中..." : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
