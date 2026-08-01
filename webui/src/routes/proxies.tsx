import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { LoaderCircle, MapPin, Network, Pencil, Plus, Trash2 } from "lucide-react";
import Loading from "@/components/loading";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

const countryFlag = (countryCode: string) => {
  const code = countryCode.trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(code)) return "";
  return [...code].map((char) => String.fromCodePoint(127397 + char.charCodeAt(0))).join("");
};

export default function ProxiesPage() {
  const [proxies, setProxies] = useState<Proxy[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<Proxy | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Proxy | null>(null);
  const [name, setName] = useState("");
  const [proxyURL, setProxyURL] = useState("");
  const [regionResults, setRegionResults] = useState<Record<number, ProxyRegionCheckResult>>({});
  const [regionCheckingID, setRegionCheckingID] = useState<number | null>(null);

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

  useEffect(() => {
    void fetchProxies();
  }, [fetchProxies]);

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
        setRegionResults((current) => {
          const next = { ...current };
          delete next[editing.ID];
          return next;
        });
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

  const checkRegion = async (proxy: Proxy) => {
    setRegionCheckingID(proxy.ID);
    try {
      const result = await checkProxyRegion(proxy.ID);
      setRegionResults((current) => ({ ...current, [proxy.ID]: result }));
      toast.success(`${proxy.Name} 地区检查完成`);
    } catch (error) {
      toast.error(`地区检查失败: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      setRegionCheckingID(null);
    }
  };

  if (loading) return <Loading message="加载代理列表..." />;

  return (
    <div className="flex h-full min-h-0 flex-col gap-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">代理列表</h1>
          <p className="mt-1 text-sm text-muted-foreground">统一维护提供商访问上游时使用的代理。</p>
        </div>
        <Button onClick={openCreate}>
          <Plus className="size-4" />
          新增代理
        </Button>
      </div>

      <div className="min-h-0 overflow-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名称</TableHead>
              <TableHead>代理地址</TableHead>
              <TableHead>出口地区</TableHead>
              <TableHead>使用情况</TableHead>
              <TableHead className="w-32 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {proxies.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="h-36 text-center text-muted-foreground">
                  <Network className="mx-auto mb-2 size-6" />
                  暂无代理
                </TableCell>
              </TableRow>
            ) : proxies.map((proxy) => {
              const region = regionResults[proxy.ID];
              const location = region
                ? [region.country, region.region, region.city].filter((value, index, values) => value && values.indexOf(value) === index).join(" · ")
                : "未检查";
              return (
                <TableRow key={proxy.ID}>
                  <TableCell className="font-medium">{proxy.Name}</TableCell>
                  <TableCell className="max-w-[520px] break-all font-mono text-xs">{proxy.ProxyURL}</TableCell>
                  <TableCell>
                    <div className="flex min-w-32 items-center gap-1.5" title={region?.ip || undefined}>
                      {region && <span>{countryFlag(region.country_code)}</span>}
                      <span className="max-w-56 truncate text-sm">{location}</span>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Badge variant={proxy.UsageCount ? "default" : "outline"}>
                      {proxy.UsageCount ? `${proxy.UsageCount} 个提供商` : "未使用"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="icon" title="检查出口地区" disabled={regionCheckingID === proxy.ID} onClick={() => void checkRegion(proxy)}>
                        {regionCheckingID === proxy.ID ? <LoaderCircle className="size-4 animate-spin" /> : <MapPin className="size-4" />}
                      </Button>
                      <Button variant="ghost" size="icon" title="编辑代理" onClick={() => openEdit(proxy)}>
                        <Pencil className="size-4" />
                      </Button>
                      <Button variant="ghost" size="icon" title="删除代理" onClick={() => setPendingDelete(proxy)}>
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
