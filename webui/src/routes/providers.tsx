import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "@/components/ui/button";
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
} from "@/components/ui/alert-dialog";
import { Label } from "@/components/ui/label";
import Loading from "@/components/loading";
import ProviderConfigEditor, { type ProviderConfigEditorRef } from "@/components/provider-config-editor";
import {
  getProviders,
  createProvider,
  updateProvider,
  updateProviderStatus,
  deleteProvider,
  getProviderTemplates,
  getProviderModels
} from "@/lib/api";
import type { Provider, ProviderTemplate, ProviderModel } from "@/lib/api";
import { openExternalUrl } from "@/lib/utils";
import { toast } from "sonner";
import { Shield, ExternalLink, Pencil, Trash2, Boxes, Plus, Copy } from "lucide-react";

type ProviderCapability = "chat" | "openai" | "claude";

const providerCapabilityOptions: { value: ProviderCapability; label: string }[] = [
  { value: "chat", label: "chat" },
  { value: "openai", label: "openai" },
  { value: "claude", label: "claude" },
];

const defaultProviderCapabilities: ProviderCapability[] = providerCapabilityOptions.map((item) => item.value);

const normalizeProviderCapabilities = (value?: string[] | null): ProviderCapability[] => {
  const raw = Array.isArray(value) ? value : [];
  const next = raw
    .map((item) => String(item).trim().toLowerCase())
    .filter((item): item is ProviderCapability => item === "chat" || item === "openai" || item === "claude");
  return next.length > 0 ? Array.from(new Set(next)) : [...defaultProviderCapabilities];
};

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

type InterfaceConversionView = {
  enabled: boolean;
  target: "" | "chat" | "responses" | "messages";
};

const normalizeInterfaceConversionTarget = (value: unknown): InterfaceConversionView["target"] => {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "chat" || normalized === "responses" || normalized === "messages") {
    return normalized;
  }
  return "";
};

// 定义表单验证模式
const formSchema = z.object({
  name: z.string().min(1, { message: "提供商名称不能为空" }),
  models_fetch_mode: z.enum(["v1_models", "api_pricing"]),
  config: z.string().min(1, { message: "配置不能为空" }),
  console: z.string().optional(),
  proxy_url: z.string().trim().refine((value) => {
    if (!value) return true;
    try {
      const normalized = value.replace(/^socket5:\/\//i, "socks5://");
      const parsed = new URL(normalized);
      const protocol = parsed.protocol.toLowerCase();
      return protocol === "http:" || protocol === "https:" || protocol === "socks5:";
    } catch {
      return false;
    }
  }, { message: "代理地址仅支持 http/https/socks5" }),
  capabilities: z.array(z.enum(["chat", "openai", "claude"])).min(1, { message: "至少选择一个接口支持能力" }),
  interface_conversion_enabled: z.boolean(),
  interface_conversion_target: z.enum(["chat", "responses", "messages", ""]),
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
  const [providerStatusLoadingId, setProviderStatusLoadingId] = useState<number | null>(null);
  const [structuredConfigEnabled, setStructuredConfigEnabled] = useState(false);
  const configCacheRef = useRef<Record<string, string>>({});
  const providerConfigEditorRef = useRef<ProviderConfigEditorRef | null>(null);

  // 筛选条件
  const [nameFilter, setNameFilter] = useState<string>("");
  const [debouncedNameFilter, setDebouncedNameFilter] = useState<string>("");

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: "",
      models_fetch_mode: "v1_models",
      config: "",
      console: "",
      proxy_url: "",
      capabilities: [...defaultProviderCapabilities],
      interface_conversion_enabled: false,
      interface_conversion_target: "",
    },
  });
  const getFetchModeBadgeLabel = (mode?: string) => (
    mode === "api_pricing" ? "newapi" : "通用"
  );
  const formatProviderCardName = (name: string) => Array.from(name).slice(0, 12).join("");
  const selectedCapabilities = form.watch("capabilities");
  const conversionEnabled = form.watch("interface_conversion_enabled");
  const conversionTarget = form.watch("interface_conversion_target");
  const conversionTargetOptions = useMemo(() => {
    const values = Array.isArray(selectedCapabilities) ? selectedCapabilities : [];
    const options: Array<{ value: "chat" | "responses" | "messages"; label: string }> = [];
    if (values.includes("chat")) options.push({ value: "chat", label: "/v1/chat/completions" });
    if (values.includes("openai")) options.push({ value: "responses", label: "/v1/responses" });
    if (values.includes("claude")) options.push({ value: "messages", label: "/v1/messages" });
    return options;
  }, [selectedCapabilities]);

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

    let nextConfig = configCacheRef.current.default;
    if (!nextConfig && editingProvider) {
      nextConfig = editingProvider.Config;
    }
    if (!nextConfig) {
      const template = providerTemplates[0];
      const parsedTemplate = parseConfigJson(template?.template);
      nextConfig = parsedTemplate
        ? JSON.stringify(parsedTemplate, null, 2)
        : (template?.template || defaultConfig);
    }

    setStructuredConfigEnabled(true);
    configCacheRef.current.default = nextConfig;
    form.setValue("config", nextConfig);
  }, [
    open,
    providerTemplates,
    editingProvider,
    form,
  ]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedNameFilter(nameFilter.trim());
    }, 300);
    return () => window.clearTimeout(timer);
  }, [nameFilter]);

  const fetchProviders = useCallback(async (signal?: AbortSignal) => {
    try {
      setLoading(true);
      const name = debouncedNameFilter || undefined;

      const data = await getProviders({ name }, { signal });
      setProviders(data);
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") {
        return;
      }
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取提供商列表失败: ${message}`);
      console.error(err);
    } finally {
      if (!signal?.aborted) {
        setLoading(false);
      }
    }
  }, [debouncedNameFilter]);

  const fetchProviderTemplates = useCallback(async () => {
    try {
      const data = await getProviderTemplates();
      setProviderTemplates(data);
      if (!form.getValues("config") && data.length > 0) {
        const parsed = parseConfigJson(data[0]?.template);
        form.setValue("config", parsed ? JSON.stringify(parsed, null, 2) : data[0].template);
      }
    } catch (err) {
      console.error("获取提供商模板失败", err);
    }
  }, [form]);

  useEffect(() => {
    void fetchProviderTemplates();
  }, [fetchProviderTemplates]);

  useEffect(() => {
    const controller = new AbortController();
    void fetchProviders(controller.signal);
    return () => controller.abort();
  }, [fetchProviders]);

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

  const handleStructuredConfigChange = useCallback((nextJson: string) => {
    const current = form.getValues("config");
    if (current === nextJson) {
      return;
    }
    configCacheRef.current.default = nextJson;
    form.setValue("config", nextJson, { shouldDirty: true, shouldValidate: true });
  }, [form]);

  useEffect(() => {
    if (!conversionEnabled) {
      return;
    }
    const exists = conversionTargetOptions.some((item) => item.value === conversionTarget);
    if (!exists) {
      const fallback = conversionTargetOptions[0]?.value ?? "";
      form.setValue("interface_conversion_target", fallback, { shouldDirty: true });
    }
  }, [conversionEnabled, conversionTarget, conversionTargetOptions, form]);

  const handleCreate = async (values: z.infer<typeof formSchema>) => {
    try {
      await createProvider({
        name: values.name,
        config: values.config,
        console: values.console || "",
        proxy_url: values.proxy_url || "",
        models_fetch_mode: values.models_fetch_mode,
        capabilities: values.capabilities,
        interface_conversion_enabled: values.interface_conversion_enabled,
        interface_conversion_target: values.interface_conversion_target,
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 创建成功`);
      form.reset({
        name: "",
        models_fetch_mode: "v1_models",
        config: "",
        console: "",
        proxy_url: "",
        capabilities: [...defaultProviderCapabilities],
        interface_conversion_enabled: false,
        interface_conversion_target: "",
      });
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
      await updateProvider(editingProvider.ID, {
        name: values.name,
        config: values.config,
        console: values.console || "",
        proxy_url: values.proxy_url || "",
        models_fetch_mode: values.models_fetch_mode,
        capabilities: values.capabilities,
        interface_conversion_enabled: values.interface_conversion_enabled,
        interface_conversion_target: values.interface_conversion_target,
      });
      setOpen(false);
      toast.success(`提供商 ${values.name} 更新成功`);
      setEditingProvider(null);
      form.reset({
        name: "",
        models_fetch_mode: "v1_models",
        config: "",
        console: "",
        proxy_url: "",
        capabilities: [...defaultProviderCapabilities],
        interface_conversion_enabled: false,
        interface_conversion_target: "",
      });
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

  const handleToggleProviderStatus = async (provider: Provider) => {
    const nextStatus = !provider.ProviderEnabled;
    setProviderStatusLoadingId(provider.ID);
    setProviders((prev) =>
      prev.map((item) =>
        item.ID === provider.ID
          ? {
            ...item,
            ProviderEnabled: nextStatus,
            EnabledProviderModelCount: nextStatus ? item.ProviderModelCount : 0,
          }
          : item
      )
    );
    try {
      const updated = await updateProviderStatus(provider.ID, nextStatus);
      setProviders((prev) => prev.map((item) => item.ID === provider.ID ? updated : item));
      toast.success(`${provider.Name} 已${nextStatus ? "启用" : "关闭"}`);
    } catch (err) {
      setProviders((prev) => prev.map((item) => item.ID === provider.ID ? provider : item));
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`切换提供商状态失败: ${message}`);
      console.error(err);
    } finally {
      setProviderStatusLoadingId(null);
    }
  };

  const openEditDialog = (provider: Provider) => {
    configCacheRef.current = {};
    setEditingProvider(provider);
    form.reset({
      name: provider.Name,
      models_fetch_mode: provider.ModelsFetchMode === "api_pricing" ? "api_pricing" : "v1_models",
      config: provider.Config,
      console: provider.Console || "",
      proxy_url: provider.ProxyURL || "",
      capabilities: normalizeProviderCapabilities(provider.Capabilities),
      interface_conversion_enabled: provider.InterfaceConversionEnabled === 1 || provider.InterfaceConversionEnabled === true,
      interface_conversion_target: normalizeInterfaceConversionTarget(provider.InterfaceConversionTarget),
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
      models_fetch_mode: "v1_models",
      config: defaultConfig,
      console: "",
      proxy_url: "",
      capabilities: [...defaultProviderCapabilities],
      interface_conversion_enabled: false,
      interface_conversion_target: "",
    });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const hasFilter = nameFilter.trim() !== "";
  return (
    <div className="flex h-full min-h-0 flex-col gap-5">
      <div className="flex flex-col gap-4 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-3xl font-semibold tracking-tight">提供商管理</h2>
          </div>
          <div className="flex w-full sm:w-auto items-center justify-end gap-2">
            <Label className="sr-only">提供商名称</Label>
            <Input
              placeholder="输入名称"
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              className="h-8 w-[160px] text-xs px-2"
            />
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
      <div className="flex-1 min-h-0 overflow-hidden">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载提供商列表" />
          </div>
        ) : providers.length === 0 ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            {hasFilter ? '未找到匹配的提供商' : '暂无提供商数据'}
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="flex-1 min-h-0 overflow-y-auto py-1">
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {providers.map((provider) => (
                  <div
                    key={provider.ID}
                    className="group rounded-[26px] border border-border/70 bg-card/88 p-5 shadow-[0_18px_50px_rgba(98,71,47,0.08)] transition-all duration-200 hover:-translate-y-0.5 hover:shadow-[0_24px_60px_rgba(98,71,47,0.12)]"
                  >
                    {(() => {
                      const hasProxy = Boolean(provider.ProxyURL && provider.ProxyURL.trim() !== "");
                      const proxyStatusLabel = hasProxy ? "代理" : "直连";
                      const providerEnabled = Boolean(provider.ProviderEnabled);
                      const statusLoading = providerStatusLoadingId === provider.ID;
                      return (
                    <div className="mb-3 flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <h3 className="truncate text-sm font-semibold text-foreground" title={provider.Name}>
                          {formatProviderCardName(provider.Name)}
                        </h3>
                      </div>
                      <div className="flex items-center gap-1">
                        <button
                          type="button"
                          title={providerEnabled ? "关闭提供商" : "启用提供商"}
                          aria-label={providerEnabled ? "关闭提供商" : "启用提供商"}
                          disabled={statusLoading}
                          onClick={() => void handleToggleProviderStatus(provider)}
                          className={`relative flex size-7 shrink-0 items-center justify-center rounded-full border shadow-sm transition-all duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400/50 focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60 ${
                            providerEnabled
                              ? "border-emerald-200 bg-emerald-50 text-emerald-600 shadow-emerald-500/10 hover:border-emerald-300 hover:bg-emerald-100 hover:text-emerald-700"
                              : "border-border/80 bg-background/85 text-muted-foreground/45 hover:border-emerald-300 hover:bg-emerald-50 hover:text-emerald-600"
                          }`}
                        >
                          <Shield className="size-4 stroke-[2.3]" />
                        </button>
                        <div className={`rounded-full border px-2 py-0.5 text-[11px] font-medium ${hasProxy ? "border-sky-200 bg-sky-50 text-sky-700" : "border-slate-200 bg-slate-100 text-slate-700"}`}>
                          {proxyStatusLabel}
                        </div>
                        <div className="rounded-full border border-emerald-200 bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700">
                          {getFetchModeBadgeLabel(provider.ModelsFetchMode)}
                        </div>
                      </div>
                    </div>
                      );
                    })()}

                    <div className="flex items-center justify-end gap-2">
                      <Button
                        title="打开控制台"
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 rounded-full"
                        disabled={!provider.Console}
                        onClick={() => {
                          if (!openExternalUrl(provider.Console)) {
                            toast.error("控制台地址无效或浏览器阻止了弹窗（仅支持 http/https）");
                          }
                        }}
                      >
                        <ExternalLink className="h-4 w-4" />
                      </Button>
                      <Button
                        title="编辑"
                        variant="outline"
                        size="icon"
                        className="h-8 w-8 rounded-full"
                        onClick={() => openEditDialog(provider)}
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        title="查看模型"
                        variant="secondary"
                        size="icon"
                        className="h-8 w-8 rounded-full"
                        onClick={() => openModelsDialog(provider.ID)}
                      >
                        <Boxes className="h-4 w-4" />
                      </Button>
                      <Button
                        title="删除"
                        variant="destructive"
                        size="icon"
                        className="h-8 w-8 rounded-full"
                        onClick={() => openDeleteDialog(provider.ID)}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </div>
                ))}
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
              <div className="grid gap-3 md:grid-cols-[1.4fr_1fr]">
                <FormField
                  control={form.control}
                  name="name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>名称</FormLabel>
                      <FormControl>
                        <Input {...field} className="max-w-[360px]" />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="models_fetch_mode"
                  render={({ field }) => (
                    <FormItem className="min-w-[220px]">
                      <FormLabel>模型获取方式</FormLabel>
                      <Select value={field.value} onValueChange={field.onChange}>
                        <FormControl>
                          <SelectTrigger className="w-full min-w-[220px]">
                            <SelectValue placeholder="选择获取方式" />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectItem value="v1_models">通用</SelectItem>
                          <SelectItem value="api_pricing">NewAPI</SelectItem>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={form.control}
                name="config"
                render={({ field }) => (
                  <FormItem>
                    <div className="flex items-center justify-between gap-2">
                      <FormLabel>配置</FormLabel>
                      {structuredConfigEnabled && (
                        <Button
                          type="button"
                          variant="secondary"
                          size="sm"
                          onClick={() => providerConfigEditorRef.current?.addItem()}
                        >
                          <Plus className="size-4" />
                          添加字段
                        </Button>
                      )}
                    </div>
                    {structuredConfigEnabled ? (
                      <ProviderConfigEditor
                        ref={providerConfigEditorRef}
                        value={field.value}
                        onChange={handleStructuredConfigChange}
                      />
                    ) : (
                      <FormControl>
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
                name="proxy_url"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>代理地址（可选）</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="capabilities"
                render={({ field }) => {
                  const currentValues = Array.isArray(field.value) ? field.value : [];
                  return (
                    <FormItem>
                      <FormLabel>接口支持能力（可多选）</FormLabel>
                      <div className="grid gap-2 sm:grid-cols-3">
                        {providerCapabilityOptions.map((option) => {
                          const checked = currentValues.includes(option.value);
                          return (
                            <label
                              key={option.value}
                              className="flex cursor-pointer items-start gap-2 rounded-lg border border-border/70 px-3 py-2"
                            >
                              <Checkbox
                                checked={checked}
                                onCheckedChange={(next) => {
                                  const shouldCheck = next === true;
                                  const valueSet = new Set(currentValues);
                                  if (shouldCheck) {
                                    valueSet.add(option.value);
                                  } else {
                                    valueSet.delete(option.value);
                                  }
                                  field.onChange(Array.from(valueSet));
                                }}
                              />
                              <span className="min-w-0">
                                <span className="block text-sm font-medium leading-none">{option.label}</span>
                              </span>
                            </label>
                          );
                        })}
                      </div>
                      <FormMessage />
                    </FormItem>
                  );
                }}
              />

              <div className="space-y-2 rounded-lg border border-border/70 p-3">
                <div className="text-sm font-medium">接口转换（可选）</div>
                <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <FormField
                    control={form.control}
                    name="interface_conversion_enabled"
                    render={({ field }) => (
                      <label className="flex cursor-pointer items-center gap-2">
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={(next) => {
                            const enabled = next === true;
                            field.onChange(enabled);
                            if (enabled && !form.getValues("interface_conversion_target") && conversionTargetOptions.length > 0) {
                              form.setValue("interface_conversion_target", conversionTargetOptions[0].value, { shouldDirty: true });
                            }
                            if (!enabled) {
                              form.setValue("interface_conversion_target", "", { shouldDirty: true });
                            }
                          }}
                        />
                        <span className="text-sm">启用接口转换</span>
                      </label>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name="interface_conversion_target"
                    render={({ field }) => (
                      <Select
                        value={field.value}
                        onValueChange={field.onChange}
                        disabled={!conversionEnabled}
                      >
                        <SelectTrigger className="w-full sm:w-[280px]">
                          <SelectValue placeholder="选择目标接口能力" />
                        </SelectTrigger>
                        <SelectContent>
                          {conversionTargetOptions.map((option) => (
                            <SelectItem key={option.value} value={option.value}>
                              {option.label}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    )}
                  />
                </div>
              </div>

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

      <AlertDialog open={deleteId !== null} onOpenChange={(nextOpen) => { if (!nextOpen) setDeleteId(null); }}>
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
                              <Copy className="h-4 w-4" />
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
