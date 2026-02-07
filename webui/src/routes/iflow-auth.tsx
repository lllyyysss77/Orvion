import { useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import Loading from "@/components/loading";
import { toast } from "sonner";
import {
  addIFlowSubscriptionByCookie,
  deleteIFlowSubscription,
  getIFlowOAuthStatus,
  getIFlowSubscriptionModels,
  getIFlowSubscriptions,
  startIFlowOAuth,
  type IFlowModel,
  type IFlowSubscription,
} from "@/lib/api";

const formatTime = (value?: string) => {
  if (!value) return "-";
  const trimmed = value.trim();
  if (!trimmed) return "-";
  const maybeDate = new Date(trimmed);
  if (!Number.isNaN(maybeDate.getTime())) return maybeDate.toLocaleString();
  return trimmed;
};

const displayName = (sub: IFlowSubscription) => sub.email || sub.id;

export default function IFlowAuthPage() {
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [oauthStarting, setOauthStarting] = useState(false);
  const [oauthState, setOauthState] = useState<string | null>(null);
  const [subscriptions, setSubscriptions] = useState<IFlowSubscription[]>([]);
  const [modelDialogOpen, setModelDialogOpen] = useState(false);
  const [modelLoading, setModelLoading] = useState(false);
  const [activeSubscription, setActiveSubscription] = useState<IFlowSubscription | null>(null);
  const [models, setModels] = useState<IFlowModel[]>([]);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [cookieInput, setCookieInput] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<IFlowSubscription | null>(null);
  const [deleting, setDeleting] = useState(false);
  const pollRef = useRef<number | null>(null);

  const sortedModels = useMemo(
    () => [...models].sort((a, b) => a.id.localeCompare(b.id)),
    [models]
  );

  const fetchSubscriptions = async () => {
    setLoading(true);
    try {
      const list = await getIFlowSubscriptions();
      setSubscriptions(list);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "获取 iFlow 订阅失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchSubscriptions();
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
      if (event?.data?.type !== "iflow-oauth-callback") return;
      if (!oauthState) return;
      void refreshOAuthStatus(oauthState, true);
    };
    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
  }, [oauthState]);

  const refreshOAuthStatus = async (state: string, showToast: boolean) => {
    try {
      const result = await getIFlowOAuthStatus(state);
      if (result.status === "ok") {
        if (pollRef.current) {
          window.clearInterval(pollRef.current);
          pollRef.current = null;
        }
        setOauthState(null);
        if (showToast) {
          toast.success("iFlow OAuth 授权成功");
        }
        await fetchSubscriptions();
      } else if (result.status === "error") {
        if (pollRef.current) {
          window.clearInterval(pollRef.current);
          pollRef.current = null;
        }
        setOauthState(null);
        if (showToast) {
          toast.error(result.message || "iFlow OAuth 授权失败");
        }
      }
    } catch (error) {
      console.error(error);
      if (showToast) {
        toast.error(error instanceof Error ? error.message : "查询 OAuth 状态失败");
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
    setOauthStarting(true);
    try {
      const result = await startIFlowOAuth();
      setOauthState(result.state);
      window.open(result.auth_url, "_blank", "width=520,height=720");
      startPolling(result.state);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "发起 iFlow OAuth 失败");
    } finally {
      setOauthStarting(false);
    }
  };

  const handleAddByCookie = async () => {
    const cookie = cookieInput.trim();
    if (!cookie) {
      toast.error("请输入 iFlow Cookie");
      return;
    }

    setSubmitting(true);
    try {
      await addIFlowSubscriptionByCookie(cookie);
      toast.success("iFlow 订阅添加成功");
      setDialogOpen(false);
      setCookieInput("");
      await fetchSubscriptions();
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "添加 iFlow 订阅失败");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteIFlowSubscription(deleteTarget.id);
      toast.success("已删除 iFlow 订阅");
      setDeleteTarget(null);
      await fetchSubscriptions();
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "删除 iFlow 订阅失败");
    } finally {
      setDeleting(false);
    }
  };

  const handleOpenModels = async (sub: IFlowSubscription) => {
    setActiveSubscription(sub);
    setModelDialogOpen(true);
    setModelLoading(true);
    try {
      const list = await getIFlowSubscriptionModels(sub.id);
      setModels(list);
    } catch (error) {
      console.error(error);
      toast.error(error instanceof Error ? error.message : "获取 iFlow 可用模型失败");
      setModels([]);
    } finally {
      setModelLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">iFlow 认证</h1>
          <p className="text-sm text-muted-foreground mt-1">
            支持 OAuth 登录或 Cookie 登录，统一管理 iFlow 订阅凭据（auth_files）。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={fetchSubscriptions} disabled={loading}>
            刷新
          </Button>
          <Button onClick={handleStartOAuth} disabled={oauthStarting}>
            OAuth 登录
          </Button>
          <Button onClick={() => setDialogOpen(true)}>Cookie 登录</Button>
        </div>
      </div>

      {oauthState && (
        <div className="rounded-xl border border-border/60 bg-background/60 px-4 py-3 text-sm text-muted-foreground">
          OAuth 授权进行中，状态码：<span className="font-mono">{oauthState}</span>
        </div>
      )}

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
              暂无 iFlow 订阅，请使用右上角 OAuth 登录或 Cookie 登录添加凭据。
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
                      <div
                        className="text-base font-semibold leading-tight truncate"
                        title={displayName(sub)}
                      >
                        {displayName(sub)}
                      </div>
                      <div
                        className="mt-1 text-xs text-muted-foreground font-mono truncate"
                        title={sub.id}
                      >
                        {sub.id}
                      </div>
                    </div>
                    <Badge variant="outline">{sub.type || "iflow"}</Badge>
                  </div>

                  <div className="rounded-xl border border-border/60 bg-background/60 p-3.5 space-y-2 text-sm">
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
                      onClick={() => handleOpenModels(sub)}
                    >
                      可用模型
                    </Button>
                    <Button
                      size="default"
                      variant="destructive"
                      className="w-full"
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

      <Dialog open={modelDialogOpen} onOpenChange={setModelDialogOpen}>
        <DialogContent className="max-w-2xl max-h-[70vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>可用模型</DialogTitle>
            <DialogDescription>
              {activeSubscription ? displayName(activeSubscription) : "订阅"}
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
                    {model.owned_by || model.type || "-"}
                  </span>
                </div>
              ))}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>iFlow Cookie 登录</DialogTitle>
            <DialogDescription>
              粘贴浏览器 Cookie（至少包含 `BXAuth=...`），系统会自动换取并保存 API Key。
            </DialogDescription>
          </DialogHeader>
          <Textarea
            className="min-h-36"
            placeholder="例如：BXAuth=xxxx; 其它字段..."
            value={cookieInput}
            onChange={(event) => setCookieInput(event.target.value)}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={handleAddByCookie} disabled={submitting}>
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除</AlertDialogTitle>
            <AlertDialogDescription>
              删除后该 iFlow 订阅将无法继续用于 `auth_files` 轮询请求。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete} disabled={deleting}>
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
