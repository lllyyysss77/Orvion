import { useState, useEffect, useRef } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Tooltip,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Checkbox } from "@/components/ui/checkbox";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import Loading from "@/components/loading";
import { Label } from "@/components/ui/label";
import ProviderConfigEditor from "@/components/provider-config-editor";
import iconSvg from "@/assets/icon.svg";
import {
  getProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  getProviderTemplates,
  getProviderModels,
  getProvidersStats
} from "@/lib/api";
import type { Provider, ProviderTemplate, ProviderModel, ProviderStatsItem } from "@/lib/api";
import { toast } from "sonner";
import { ExternalLink, Pencil, Trash2, Boxes } from "lucide-react";

const parseConfigJson = (raw?: string | null): Record<string, unknown> | null => {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return null;
    return parsed as Record<string, unknown>;
  } catch {
    return null;
  }
};

const AUTH_PROVIDER_TYPE_SET = new Set(["iflow", "codex-auths"]);

const isAuthProviderTemplate = (template?: ProviderTemplate, fallbackType?: string): boolean => {
  if (template?.auth_mode === true) return true;
  if ((template?.category || "").toLowerCase() === "auth") return true;
  if (fallbackType) return AUTH_PROVIDER_TYPE_SET.has(fallbackType);
  return false;
};

const ensureAuthTemplateConfig = (template?: ProviderTemplate, providerType?: string): string => {
  if (template?.template) {
    const parsed = parseConfigJson(template.template);
    if (parsed) return JSON.stringify(parsed, null, 2);
  }
  const fallbackType = providerType || "auth";
  return JSON.stringify({ auth_mode: fallbackType }, null, 2);
};

const getConsoleFavicon = (consoleUrl?: string): string => {
  if (!consoleUrl) return "";
  try {
    const url = new URL(consoleUrl);
    return `${url.origin}/favicon.ico`;
  } catch {
    return "";
  }
};

const ProviderFavicon = ({
  consoleUrl,
  fallback,
}: {
  consoleUrl?: string;
  fallback: string;
}) => {
  const [failed, setFailed] = useState(false);
  const src = getConsoleFavicon(consoleUrl);
  const label = (fallback || "?").slice(0, 1).toUpperCase();

  if (!src || failed) {
    return (
      <span className="size-4 rounded-full bg-muted/70 text-[9px] text-muted-foreground inline-flex items-center justify-center">
        {label}
      </span>
    );
  }

  return (
    <img
      src={src}
      alt={label}
      className="size-4 rounded-full border border-border/60"
      onError={() => setFailed(true)}
    />
  );
};
// 定义表单验证模式
const formSchema = z.object({
  name: z.string().min(1, { message: "提供商名称不能为空" }),
  type: z.string().min(1, { message: "提供商类型不能为空" }),
  config: z.string().min(1, { message: "配置不能为空" }),
  console: z.string().optional(),
  rpmLimit: z.number().min(0, { message: "RPM 限制必须大于等于 0" }).optional(),
});

export default function ProvidersPage() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerTemplates, setProviderTemplates] = useState<ProviderTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [modelsOpen, setModelsOpen] = useState(false);
  const [modelsOpenId, setModelsOpenId] = useState<number | null>(null);
  const [providerModels, setProviderModels] = useState<ProviderModel[]>([]);
  const [filteredProviderModels, setFilteredProviderModels] = useState<ProviderModel[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [structuredConfigEnabled, setStructuredConfigEnabled] = useState(false);
  const configCacheRef = useRef<Record<string, string>>({});
  const statsRequestRef = useRef(0);
  const [providerStats, setProviderStats] = useState<Record<number, {
    rpmCount: number | null;
    rpmLoaded: boolean;
  }>>({});

  // 筛选条件
  const [nameFilter, setNameFilter] = useState<string>("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [availableTypes, setAvailableTypes] = useState<string[]>([]);
  const providerTypeDisplayMap = new Map(
    providerTemplates.map((item) => [item.type, item.display_name || item.type])
  );

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: { name: "", type: "", config: "", console: "", rpmLimit: 0 },
  });
  const selectedProviderType = form.watch("type");
  const selectedTemplate = providerTemplates.find((item) => item.type === selectedProviderType);
  const selectedIsAuthType = isAuthProviderTemplate(selectedTemplate, selectedProviderType);

  useEffect(() => {
    fetchProviders();
    fetchProviderTemplates();
  }, []);

  // 监听筛选条件变化
  useEffect(() => {
    fetchProviders();
  }, [nameFilter, typeFilter]);

  useEffect(() => {
    if (!open) {
      setStructuredConfigEnabled(false);
      configCacheRef.current = {};
      return;
    }

    const defaultConfig = JSON.stringify(
      { base_url: "", api_key: "" },
      null,
      2
    );

    const type = selectedProviderType || editingProvider?.Type || "";
    const cached = type ? configCacheRef.current[type] : undefined;

    let nextConfig = cached;

    if (!nextConfig && editingProvider && editingProvider.Type === type) {
      nextConfig = editingProvider.Config;
    }

    if (!nextConfig && type) {
      const template = providerTemplates.find((item) => item.type === type);
      if (template) {
        if (isAuthProviderTemplate(template, type)) {
          nextConfig = ensureAuthTemplateConfig(template, type);
        } else {
          const parsedTemplate = parseConfigJson(template.template);
          nextConfig = parsedTemplate ? JSON.stringify(parsedTemplate, null, 2) : template.template;
        }
      }
    }

    const selectedTemplateMeta = providerTemplates.find((item) => item.type === type);
    const isAuthType = isAuthProviderTemplate(selectedTemplateMeta, type);
    if (!nextConfig) {
      nextConfig = isAuthType ? ensureAuthTemplateConfig(selectedTemplateMeta, type) : defaultConfig;
    }

    setStructuredConfigEnabled(!isAuthType);
    if (type) {
      configCacheRef.current[type] = nextConfig;
    }
    form.setValue("config", nextConfig);
    if (isAuthType) {
      form.setValue("console", "");
      form.setValue("rpmLimit", 0);
    }
  }, [
    open,
    selectedProviderType,
    providerTemplates,
    editingProvider,
    form,
  ]);

  const fetchProviders = async () => {
    try {
      setLoading(true);
      // 处理筛选条件，"all"表示不过滤，空字符串表示不过滤
      const name = nameFilter.trim() || undefined;
      const type = typeFilter === "all" ? undefined : typeFilter;

      const data = await getProviders({ name, type });
      setProviders(data);
      void fetchProviderStats(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取提供商列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const fetchProviderStats = async (items: Provider[]) => {
    if (items.length === 0) {
      setProviderStats({});
      return;
    }
    const requestId = ++statsRequestRef.current;
    setProviderStats((prev) => {
      const next: Record<number, {
        rpmCount: number | null;
        rpmLoaded: boolean;
      }> = {};
      for (const provider of items) {
        const existing = prev[provider.ID];
        next[provider.ID] = {
          rpmCount: existing?.rpmCount ?? null,
          rpmLoaded: false,
        };
      }
      return next;
    });

    let statsList: ProviderStatsItem[] = [];
    try {
      statsList = await getProvidersStats(items.map((provider) => provider.ID));
    } catch (err) {
      if (requestId !== statsRequestRef.current) return;
      toast.error("获取提供商状态失败");
      return;
    }

    if (requestId !== statsRequestRef.current) {
      return;
    }

    const statsMap = new Map(statsList.map((item) => [item.provider_id, item]));
    const next: Record<number, { rpmCount: number | null; rpmLoaded: boolean }> = {};
    for (const provider of items) {
      const item = statsMap.get(provider.ID);
      next[provider.ID] = {
        rpmCount: item ? item.rpm_count ?? null : null,
        rpmLoaded: item ? item.rpm_loaded ?? true : false,
      };
    }
    setProviderStats(next);
  };

  const fetchProviderTemplates = async () => {
    try {
      const data = await getProviderTemplates();
      setProviderTemplates(data);
      const types = data.map((template) => template.type);
      setAvailableTypes(types);

      if (!form.getValues("type") && types.length > 0) {
        const firstType = types[0];
        form.setValue("type", firstType);
        const firstTemplate = data.find((item) => item.type === firstType);
        if (firstTemplate) {
          const parsed = parseConfigJson(firstTemplate.template);
          form.setValue("config", parsed ? JSON.stringify(parsed, null, 2) : firstTemplate.template);
        }
      }
    } catch (err) {
      console.error("获取提供商模板失败", err);
    }
  };

  const fetchProviderModels = async (providerId: number) => {
    try {
      setModelsLoading(true);
      const data = await getProviderModels(providerId);
      setProviderModels(data);
      setFilteredProviderModels(data);
    } catch (err) {
      console.error("获取提供商模型失败", err);
      setProviderModels([]);
      setFilteredProviderModels([]);
    } finally {
      setModelsLoading(false);
    }
  };

  const openModelsDialog = async (providerId: number) => {
    setModelsOpen(true);
    setModelsOpenId(providerId);
    await fetchProviderModels(providerId);
  };

  const copyModelName = async (modelName: string) => {
    await navigator.clipboard.writeText(modelName);
    toast.success(`已复制模型名称: ${modelName}`);
  };

  const handleStructuredConfigChange = (nextJson: string) => {
    if (selectedProviderType) {
      configCacheRef.current[selectedProviderType] = nextJson;
    }
    form.setValue("config", nextJson, { shouldDirty: true, shouldValidate: true });
  };

  const handleCreate = async (values: z.infer<typeof formSchema>) => {
    try {
      const templateMeta = providerTemplates.find((item) => item.type === values.type);
      const isAuthType = isAuthProviderTemplate(templateMeta, values.type);
      await createProvider({
        name: values.name,
        type: values.type,
        config: isAuthType ? ensureAuthTemplateConfig(templateMeta, values.type) : values.config,
        console: isAuthType ? "" : (values.console || ""),
        rpm_limit: isAuthType ? 0 : (values.rpmLimit || 0),
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 创建成功`);
      form.reset({ name: "", type: "", config: "", console: "", rpmLimit: 0 });
      fetchProviders();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`创建提供商失败: ${message}`);
      console.error(err);
    }
  };

  const handleUpdate = async (values: z.infer<typeof formSchema>) => {
    if (!editingProvider) return;
    try {
      const templateMeta = providerTemplates.find((item) => item.type === values.type);
      const isAuthType = isAuthProviderTemplate(templateMeta, values.type);
      await updateProvider(editingProvider.ID, {
        name: values.name,
        type: values.type,
        config: isAuthType ? ensureAuthTemplateConfig(templateMeta, values.type) : values.config,
        console: isAuthType ? "" : (values.console || ""),
        rpm_limit: isAuthType ? 0 : (values.rpmLimit || 0),
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 更新成功`);
      setEditingProvider(null);
      form.reset({ name: "", type: "", config: "", console: "", rpmLimit: 0 });
      fetchProviders();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`更新提供商失败: ${message}`);
      console.error(err);
    }
  };

  const handleDelete = async () => {
    if (!deleteId) return;
    try {
      const targetProvider = providers.find((provider) => provider.ID === deleteId);
      await deleteProvider(deleteId);
      setDeleteId(null);
      fetchProviders();
      toast.success(`提供商 ${targetProvider?.Name ?? deleteId} 删除成功`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除提供商失败: ${message}`);
      console.error(err);
    }
  };

  const openEditDialog = (provider: Provider) => {
    configCacheRef.current = {};
    setEditingProvider(provider);
    form.reset({
      name: provider.Name,
      type: provider.Type,
      config: provider.Config,
      console: provider.Console || "",
      rpmLimit: provider.RpmLimit || 0,
    });
    setOpen(true);
  };

  const openCreateDialog = () => {
    configCacheRef.current = {};
    if (providerTemplates.length === 0) {
      toast.error("暂无可用的提供商模板");
      return;
    }
    setEditingProvider(null);
    const firstTemplate = providerTemplates[0];
    const defaultType = firstTemplate?.type ?? "";
    const defaultConfig = firstTemplate
      ? (() => {
        const parsed = parseConfigJson(firstTemplate.template);
        if (parsed) return JSON.stringify(parsed, null, 2);
        // 没有模板时使用默认字段的 JSON
        return JSON.stringify({ base_url: "", api_key: "" }, null, 2);
      })()
      : "";
    form.reset({
      name: "",
      type: defaultType,
      config: defaultConfig,
      console: "",
    });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const hasFilter = nameFilter.trim() !== "" || typeFilter !== "all";
  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">提供商管理</h2>
          </div>
          <div className="flex w-full sm:w-auto items-center justify-end gap-2">
            <Label className="sr-only">提供商名称</Label>
            <Input
              placeholder="输入名称"
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              className="h-8 w-[160px] text-xs px-2"
            />
            <Label className="sr-only">类型</Label>
            <Select value={typeFilter} onValueChange={setTypeFilter}>
              <SelectTrigger className="h-8 w-[120px] text-xs px-2">
                <SelectValue placeholder="全部" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部</SelectItem>
                {availableTypes.map((type) => (
                  <SelectItem key={type} value={type}>
                    {providerTypeDisplayMap.get(type) || type}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              onClick={openCreateDialog}
              className="h-8 w-full text-xs sm:w-auto"
              disabled={providerTemplates.length === 0}
            >
              添加提供商
            </Button>
          </div>
        </div>
      </div>
      <div className="flex-1 min-h-0 border rounded-md bg-background shadow-sm">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载提供商列表" />
          </div>
        ) : providers.length === 0 ? (
          <div className="flex h-full items-center justify-center text-muted-foreground text-sm text-center px-6">
            {hasFilter ? '未找到匹配的提供商' : '暂无提供商数据'}
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="flex-1 min-h-0 overflow-y-auto p-3">
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4 2xl:grid-cols-5">
                {providers.map((provider) => {
                  const stats = providerStats[provider.ID];
                  const rpmLoaded = stats?.rpmLoaded ?? false;
                  const rpmCurrent = stats?.rpmCount ?? 0;
                  const rpmLimit = provider.RpmLimit || 0;
                  const rpmRatio = rpmLoaded
                    ? (rpmLimit > 0 ? Math.min(100, (rpmCurrent / rpmLimit) * 100) : 100)
                    : 0;
                  const rpmLabel = rpmLoaded ? (rpmLimit > 0 ? `${rpmCurrent}/${rpmLimit}` : `${rpmCurrent}/∞`) : "--/--";
                  return (
                    <Card key={provider.ID} className="py-4 shadow-sm">
                    <CardHeader className="pb-2 sm:px-4 px-3">
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <CardTitle className="text-base truncate" title={provider.Name}>
                            {provider.Name}
                          </CardTitle>
                          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
                            <span className="inline-flex items-center gap-1">
                              <ProviderFavicon consoleUrl={provider.Console} fallback={provider.Type || "?"} />
                              <span>类型: {providerTypeDisplayMap.get(provider.Type) || provider.Type || "未知"}</span>
                            </span>
                          </div>
                        </div>
                        <div className="flex items-center gap-1.5">
                          {provider.Console ? (
                            <Button
                              title={provider.Console}
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              onClick={() => window.open(provider.Console, '_blank')}
                            >
                              <ExternalLink className="h-3.5 w-3.5" />
                            </Button>
                          ) : (
                            <Button variant="ghost" size="icon" className="h-7 w-7" disabled>
                              <ExternalLink className="h-3.5 w-3.5 opacity-50" />
                            </Button>
                          )}
                        </div>
                      </div>
                    </CardHeader>
                    <CardContent className="sm:px-4 px-3 space-y-2 text-xs">
                      <div className="flex items-center gap-2">
                        <span className="text-[11px] text-muted-foreground w-12">RPM</span>
                        <div
                          className="relative h-2 flex-1 rounded-full bg-muted/70 overflow-hidden"
                          title={rpmLoaded ? (rpmLimit > 0 ? `当前 ${rpmCurrent} / 限制 ${rpmLimit}` : "无限制") : "加载中"}
                        >
                          <div
                            className={`h-full rounded-full ${rpmLimit > 0 ? "bg-amber-500/70" : "bg-emerald-500/50"}`}
                            style={{
                              width: `${rpmLimit > 0 ? Math.round(rpmRatio) : 100}%`
                            }}
                          />
                        </div>
                        <span className="text-[11px] text-muted-foreground tabular-nums w-16 text-right">
                          {rpmLabel}
                        </span>
                      </div>
                    </CardContent>
                    <CardFooter className="sm:px-4 px-3 pt-2 gap-2">
                      <Button variant="outline" size="icon" className="h-8 w-8" onClick={() => openEditDialog(provider)}>
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button variant="secondary" size="icon" className="h-8 w-8" onClick={() => openModelsDialog(provider.ID)}>
                        <Boxes className="h-4 w-4" />
                      </Button>
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button variant="destructive" size="icon" className="h-8 w-8" onClick={() => openDeleteDialog(provider.ID)}>
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>确定要删除这个提供商吗？</AlertDialogTitle>
                            <AlertDialogDescription>
                              此操作无法撤销。这将永久删除该提供商。
                            </AlertDialogDescription>
                          </AlertDialogHeader>
                          <AlertDialogFooter>
                            <AlertDialogCancel onClick={() => setDeleteId(null)}>取消</AlertDialogCancel>
                            <AlertDialogAction onClick={handleDelete}>确认删除</AlertDialogAction>
                          </AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    </CardFooter>
                    </Card>
                  );
                })}
              </div>
            </div>
          </div>
        )}
      </div>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>
              {editingProvider ? "编辑提供商" : "添加提供商"}
            </DialogTitle>
            {editingProvider && (
              <DialogDescription>修改提供商信息</DialogDescription>
            )}
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(editingProvider ? handleUpdate : handleCreate)} className="space-y-4 min-w-0">
              <FormField
                control={form.control}
                name="name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>名称</FormLabel>
                    <FormControl>
                      <Input {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="type"
                render={({ field }) => {
                  const currentValue = field.value ?? "";
                  const hasCurrentValue = providerTemplates.some(
                    (template) => template.type === currentValue
                  );
                  const templateOptions =
                    !hasCurrentValue && currentValue
                      ? [
                        ...providerTemplates,
                        {
                          type: currentValue,
                          template: "",
                        } as ProviderTemplate,
                      ]
                      : providerTemplates;
                  const apiKeyTemplates = templateOptions.filter(
                    (item) => !isAuthProviderTemplate(item, item.type)
                  );
                  const authTemplates = templateOptions.filter(
                    (item) => isAuthProviderTemplate(item, item.type)
                  );

                  return (
                    <FormItem>
                      <FormLabel>类型</FormLabel>
                      <FormControl>
                        {providerTemplates.length === 0 ? (
                          <p className="text-sm text-muted-foreground">
                            暂无可用类型，请先配置模板。
                          </p>
                        ) : (
                          <RadioGroup
                            value={currentValue}
                            onValueChange={(value) => field.onChange(value)}
                            className="space-y-3"
                          >
                            {apiKeyTemplates.length > 0 && (
                              <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground">APIKey 类型</p>
                                <div className="flex flex-wrap gap-2">
                                  {apiKeyTemplates.map((template) => {
                                    const radioId = `provider-type-${template.type}`;
                                    const selected = currentValue === template.type;
                                    const displayName = template.display_name || template.type;
                                    return (
                                      <label
                                        key={template.type}
                                        htmlFor={radioId}
                                        className={`flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm ${selected
                                          ? "border-primary bg-primary/10"
                                          : "border-border"
                                          }`}
                                      >
                                        <RadioGroupItem
                                          id={radioId}
                                          value={template.type}
                                          className="sr-only"
                                        />
                                        <Checkbox
                                          checked={selected}
                                          aria-hidden="true"
                                          tabIndex={-1}
                                          className="pointer-events-none"
                                        />
                                        <span className="select-none">{displayName}</span>
                                      </label>
                                    );
                                  })}
                                </div>
                              </div>
                            )}
                            {authTemplates.length > 0 && (
                              <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground">Auth 类型</p>
                                <div className="flex flex-wrap gap-2">
                                  {authTemplates.map((template) => {
                                    const radioId = `provider-type-${template.type}`;
                                    const selected = currentValue === template.type;
                                    const displayName = template.display_name || template.type;
                                    return (
                                      <label
                                        key={template.type}
                                        htmlFor={radioId}
                                        className={`flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm ${selected
                                          ? "border-primary bg-primary/10"
                                          : "border-border"
                                          }`}
                                      >
                                        <RadioGroupItem
                                          id={radioId}
                                          value={template.type}
                                          className="sr-only"
                                        />
                                        <Checkbox
                                          checked={selected}
                                          aria-hidden="true"
                                          tabIndex={-1}
                                          className="pointer-events-none"
                                        />
                                        <span className="select-none">{displayName}</span>
                                      </label>
                                    );
                                  })}
                                </div>
                              </div>
                            )}
                          </RadioGroup>
                        )}
                      </FormControl>
                      {!hasCurrentValue && currentValue && (
                        <p className="text-xs text-muted-foreground">
                          当前提供商类型{" "}
                          <span className="font-mono">{currentValue}</span>{" "}
                          不在模板列表中，可继续使用或选择其他类型。
                        </p>
                      )}
                      <FormMessage />
                    </FormItem>
                  );
                }}
              />

              {!selectedIsAuthType && (
                <>
                  <FormField
                    control={form.control}
                    name="config"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>配置</FormLabel>
                        {structuredConfigEnabled ? (
                          <ProviderConfigEditor value={field.value} onChange={handleStructuredConfigChange} providerType={selectedProviderType} />
                        ) : (
                          <FormControl>
                            {/* 避免 api_key 等超长字段撑破弹窗宽度 */}
                            <Textarea {...field} className="resize-none w-full max-w-full min-w-0 whitespace-pre-wrap break-all overflow-x-auto [field-sizing:fixed]" />
                          </FormControl>
                        )}
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name="console"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>控制台地址</FormLabel>
                        <FormControl>
                          <Input {...field} placeholder="https://example.com/console" />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name="rpmLimit"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>RPM 限制</FormLabel>
                        <FormControl>
                          <Input
                            type="number"
                            min={0}
                            placeholder="0 表示无限制"
                            value={field.value ?? 0}
                            onChange={(e) => field.onChange(Number(e.target.value) || 0)}
                          />
                        </FormControl>
                        <p className="text-xs text-muted-foreground">
                          每分钟最大请求数，0 表示无限制。达到限制后会自动切换到其他供应商。
                        </p>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </>
              )}
              {selectedIsAuthType && (
                <div className="rounded-md border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
                  Auth 类型将自动使用订阅凭据，不需要手动填写 APIKey、URL、Console 和 RPM。
                </div>
              )}

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                  取消
                </Button>
                <Button type="submit">
                  {editingProvider ? "更新" : "创建"}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>

      {/* 模型列表对话框 */}
      <Dialog open={modelsOpen} onOpenChange={setModelsOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{providers.find(v => v.ID === modelsOpenId)?.Name}模型列表</DialogTitle>
            <DialogDescription>
              当前提供商的所有可用模型
            </DialogDescription>
          </DialogHeader>

          {/* 搜索框 */}
          {!modelsLoading && providerModels.length > 0 && (
            <div className="mb-4">
              <Input
                placeholder="搜索模型 ID"
                onChange={(e) => {
                  const searchTerm = e.target.value.toLowerCase();
                  if (searchTerm === '') {
                    setFilteredProviderModels(providerModels);
                  } else {
                    const filteredModels = providerModels.filter(model =>
                      model.id.toLowerCase().includes(searchTerm)
                    );
                    setFilteredProviderModels(filteredModels);
                  }
                }}
                className="w-full"
              />
            </div>
          )}

          {modelsLoading ? (
            <Loading message="加载模型列表" />
          ) : (
            <div className="max-h-96 overflow-y-auto">
              {filteredProviderModels.length === 0 ? (
                <div className="text-center text-gray-500 py-8">
                  {providerModels.length === 0 ? '暂无模型数据' : '未找到匹配的模型'}
                </div>
              ) : (
                <div className="space-y-2">
                  {filteredProviderModels.map((model, index) => (
                    <div
                      key={index}
                      className="flex items-center justify-between p-2 border rounded-lg"
                    >
                      <div className="flex-1">
                        <div className="font-medium">{model.id}</div>
                      </div>
                      <TooltipProvider>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => copyModelName(model.id)}
                              className="min-w-12 gap-2 px-2"
                            >
                              <img
                                src={iconSvg}
                                alt="复制"
                                className="h-4 w-4 opacity-80"
                              />
                            </Button>
                          </TooltipTrigger>
                        </Tooltip>
                      </TooltipProvider>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          <DialogFooter>
            <Button onClick={() => setModelsOpen(false)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
