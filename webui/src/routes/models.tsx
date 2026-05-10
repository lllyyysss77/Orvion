import { useState, useEffect, useCallback, useMemo, useRef, useLayoutEffect } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import {
  getModels,
  createModel,
  updateModel,
  updateModelStatus,
  deleteModel,
} from "@/lib/api";
import type { Model } from "@/lib/api";
import { Switch } from "@/components/ui/switch";
import { ModelProvidersPanel } from "@/routes/model-providers";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { toast } from "sonner";
import {
  Boxes,
  ChevronLeft,
  ChevronRight,
  Search,
  Plus,
  Pencil,
  Trash2,
  Coins,
  Eye,
  Video,
  Layers,
  ArrowUpDown,
  MessageSquare,
  SlidersHorizontal,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { resolveModelIcon } from "@/lib/model-icon";

const capabilityValues = ["chat", "vision", "video", "embedding", "rerank"] as const;
type ModelCapability = (typeof capabilityValues)[number];
type ModelColumnKey = "model" | "input" | "output" | "capabilities" | "status" | "actions";

const MODEL_FILTER_STORAGE_KEY = "orvion_models_filters_v1";
const MODEL_COLUMNS_STORAGE_KEY = "orvion_models_columns_v1";
const MODEL_WIDTH_STORAGE_KEY = "orvion_models_width_v1";

type ModelFilterState = {
  searchInput: string;
  capabilityFilter: string;
  familyFilter: string;
};

type ModelColumnVisibility = Record<ModelColumnKey, boolean>;
type ModelWidthState = {
  model: "compact" | "default" | "wide";
  price: "compact" | "default";
};

const defaultModelFilters: ModelFilterState = {
  searchInput: "",
  capabilityFilter: "all",
  familyFilter: "all",
};

const defaultModelColumns: ModelColumnVisibility = {
  model: true,
  input: true,
  output: true,
  capabilities: true,
  status: true,
  actions: true,
};

const defaultModelWidths: ModelWidthState = {
  model: "default",
  price: "default",
};

const parseStoredJSON = <T,>(raw: string | null): T | null => {
  if (!raw) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
};

const readStoredModelFilters = (): ModelFilterState => {
  if (typeof window === "undefined") return defaultModelFilters;
  const parsed = parseStoredJSON<Partial<ModelFilterState>>(window.localStorage.getItem(MODEL_FILTER_STORAGE_KEY));
  if (!parsed) return defaultModelFilters;
  return {
    searchInput: typeof parsed.searchInput === "string" ? parsed.searchInput : "",
    capabilityFilter: typeof parsed.capabilityFilter === "string" ? parsed.capabilityFilter : "all",
    familyFilter: typeof parsed.familyFilter === "string" ? parsed.familyFilter : "all",
  };
};

const readStoredModelColumns = (): ModelColumnVisibility => {
  if (typeof window === "undefined") return defaultModelColumns;
  const parsed = parseStoredJSON<Partial<ModelColumnVisibility>>(window.localStorage.getItem(MODEL_COLUMNS_STORAGE_KEY));
  if (!parsed) return defaultModelColumns;
  const next = { ...defaultModelColumns };
  (Object.keys(next) as ModelColumnKey[]).forEach((key) => {
    if (typeof parsed[key] === "boolean") {
      next[key] = parsed[key] as boolean;
    }
  });
  if (!(Object.values(next).some(Boolean))) {
    return defaultModelColumns;
  }
  return next;
};

const readStoredModelWidths = (): ModelWidthState => {
  if (typeof window === "undefined") return defaultModelWidths;
  const parsed = parseStoredJSON<Partial<ModelWidthState>>(window.localStorage.getItem(MODEL_WIDTH_STORAGE_KEY));
  if (!parsed) return defaultModelWidths;
  return {
    model: parsed.model === "compact" || parsed.model === "wide" ? parsed.model : "default",
    price: parsed.price === "compact" ? "compact" : "default",
  };
};

const capabilityOptions: {
  value: ModelCapability;
  label: string;
  icon: typeof MessageSquare;
  activeClass: string;
  iconClass: string;
}[] = [
  {
    value: "chat",
    label: "对话",
    icon: MessageSquare,
    activeClass: "bg-blue-50 text-blue-700 border-blue-200",
    iconClass: "text-blue-500",
  },
  {
    value: "vision",
    label: "视觉",
    icon: Eye,
    activeClass: "bg-emerald-50 text-emerald-700 border-emerald-200",
    iconClass: "text-emerald-500",
  },
  {
    value: "video",
    label: "视频",
    icon: Video,
    activeClass: "bg-purple-50 text-purple-700 border-purple-200",
    iconClass: "text-purple-500",
  },
  {
    value: "embedding",
    label: "嵌入",
    icon: Layers,
    activeClass: "bg-orange-50 text-orange-700 border-orange-200",
    iconClass: "text-orange-500",
  },
  {
    value: "rerank",
    label: "重排",
    icon: ArrowUpDown,
    activeClass: "bg-slate-100 text-slate-700 border-slate-300",
    iconClass: "text-slate-500",
  },
];

const ModelIcon = ({ name, size = "md" }: { name: string; size?: "md" | "sm" }) => {
  const config = resolveModelIcon(name);
  const fallback = (name || "M").slice(0, 2).toUpperCase();
  const compact = size === "sm";

  const wrapperClass = compact
    ? "inline-flex size-5 shrink-0 items-center justify-center leading-none"
    : "inline-flex size-10 shrink-0 items-center justify-center rounded-2xl bg-muted/60 leading-none";
  const fallbackClass = compact
    ? "inline-flex size-5 shrink-0 items-center justify-center text-muted-foreground font-semibold text-[10px] leading-none"
    : "inline-flex size-10 shrink-0 items-center justify-center rounded-2xl bg-primary/10 text-primary font-semibold text-sm leading-none";

  if (!config) {
    return (
      <div className={fallbackClass}>
        {fallback}
      </div>
    );
  }

  return (
    <div className={wrapperClass}>
      <img src={config.src} alt={config.alt} className={cn("block object-contain", compact ? "size-[18px]" : "size-5")} />
    </div>
  );
};

type ModelFamily = {
  key: string;
  label: string;
  iconName: string;
  count: number;
};

const resolveModelFamily = (modelName: string) => {
  const iconConfig = resolveModelIcon(modelName);
  if (iconConfig) {
    const family = iconConfig.key.trim().toLowerCase();
    return {
      key: family,
      label: iconConfig.alt || iconConfig.key,
      iconName: iconConfig.key,
    };
  }

  const normalized = (modelName ?? "")
    .trim()
    .toLowerCase()
    .split(/[^a-z0-9]+/g)
    .filter(Boolean)[0];
  const family = normalized && normalized.length > 0 ? normalized : "other";

  return {
    key: family,
    label: family.charAt(0).toUpperCase() + family.slice(1),
    iconName: family,
  };
};

const formatPrice = (value?: number | null) => {
  if (value == null || !Number.isFinite(value)) {
    return "--.--";
  }
  return value.toFixed(2);
};

const isModelEnabled = (model: Model) => (model.Status == null ? true : Number(model.Status) === 1);

const isValidNonNegativePrice = (value: string) => {
  const trimmed = value.trim();
  if (trimmed === "") return false;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) && parsed >= 0;
};

const sortModelsByName = (items: Model[]) => (
  [...items].sort((left, right) =>
    left.Name.localeCompare(right.Name, "zh-Hans-CN", {
      numeric: true,
      sensitivity: "base",
    })
  )
);

// 定义表单验证模式
const formSchema = z.object({
  name: z.string().min(1, { message: "模型名称不能为空" }),
  remark: z.string(),
  max_retry: z.number().min(0, { message: "重试次数限制不能为负数" }),
  time_out: z.number().min(0, { message: "超时时间不能为负数" }),
  io_log: z.boolean(),
  strategy: z.enum(["lottery", "rotor"]),
  breaker: z.boolean(),
  capabilities: z.array(z.enum(capabilityValues)).min(1, { message: "至少选择一个模型类型" }),
  input_price: z.string().refine(isValidNonNegativePrice, { message: "输入价格不能为负数" }),
  output_price: z.string().refine(isValidNonNegativePrice, { message: "输出价格不能为负数" }),
  cache_read_price: z.string().refine(isValidNonNegativePrice, { message: "缓存读价格不能为负数" }),
  cache_write_price: z.string().refine(isValidNonNegativePrice, { message: "缓存写价格不能为负数" }),
  status: z.boolean(),
});

export default function ModelsPage() {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editingModel, setEditingModel] = useState<Model | null>(null);
  const [deleteId, setDeleteId] = useState<number | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(12);
  const [searchInput, setSearchInput] = useState(() => readStoredModelFilters().searchInput);
  const [searchTerm, setSearchTerm] = useState(() => readStoredModelFilters().searchInput.trim());
  const [capabilityFilter, setCapabilityFilter] = useState<string>(() => readStoredModelFilters().capabilityFilter);
  const [familyFilter, setFamilyFilter] = useState<string>(() => readStoredModelFilters().familyFilter);
  const [columnVisibility, setColumnVisibility] = useState<ModelColumnVisibility>(() => readStoredModelColumns());
  const [widthSettings, setWidthSettings] = useState<ModelWidthState>(() => readStoredModelWidths());
  const [providerPanelOpen, setProviderPanelOpen] = useState(false);
  const [providerPanelModel, setProviderPanelModel] = useState<Model | null>(null);
  const [statusUpdatingIds, setStatusUpdatingIds] = useState<number[]>([]);
  const listRef = useRef<HTMLDivElement | null>(null);
  const familyTabsRef = useRef<HTMLDivElement | null>(null);
  const familyButtonRefs = useRef<Record<string, HTMLButtonElement | null>>({});
  const [familyIndicator, setFamilyIndicator] = useState<{ left: number; width: number; visible: boolean }>({
    left: 0,
    width: 0,
    visible: false,
  });

  // 初始化表单
  const form = useForm<z.infer<typeof formSchema>>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: "",
      remark: "",
      max_retry: 10,
      time_out: 60,
      io_log: false,
      strategy: "lottery",
      breaker: false,
      capabilities: ["chat"],
      input_price: "0",
      output_price: "0",
      cache_read_price: "0",
      cache_write_price: "0",
      status: true,
    },
  });

  useEffect(() => {
    const timer = setTimeout(() => {
      setSearchTerm(searchInput.trim());
      setPage(1);
    }, 400);
    return () => clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const payload: ModelFilterState = {
      searchInput,
      capabilityFilter,
      familyFilter,
    };
    window.localStorage.setItem(MODEL_FILTER_STORAGE_KEY, JSON.stringify(payload));
  }, [searchInput, capabilityFilter, familyFilter]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(MODEL_COLUMNS_STORAGE_KEY, JSON.stringify(columnVisibility));
  }, [columnVisibility]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(MODEL_WIDTH_STORAGE_KEY, JSON.stringify(widthSettings));
  }, [widthSettings]);

  const fetchModels = useCallback(async () => {
    try {
      setLoading(true);
      const queryParams = {
        page: 1,
        page_size: 100,
        search: searchTerm || undefined,
        capability: capabilityFilter !== "all" ? capabilityFilter : undefined,
      };
      const firstPage = await getModels(queryParams);
      const totalPages = Math.max(firstPage.pages || 1, 1);
      const allModels = [...firstPage.data];

      if (totalPages > 1) {
        const remainRequests: Promise<{ data: Model[] }>[] = [];
        for (let currentPage = 2; currentPage <= totalPages; currentPage += 1) {
          remainRequests.push(getModels({ ...queryParams, page: currentPage }));
        }
        const remainPages = await Promise.all(remainRequests);
        for (const pageData of remainPages) {
          allModels.push(...pageData.data);
        }
      }

      setModels(sortModelsByName(allModels));
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取模型列表失败: ${message}`);
      console.error(err);
    } finally {
      setLoading(false);
    }
  }, [capabilityFilter, searchTerm]);

  useEffect(() => {
    void fetchModels();
  }, [fetchModels]);

  const modelsWithFamily = useMemo(() => (
    models.map((model) => ({
      model,
      family: resolveModelFamily(model.Name),
    }))
  ), [models]);

  const familyOptions = useMemo<ModelFamily[]>(() => {
    const familyMap = new Map<string, ModelFamily>();

    for (const item of modelsWithFamily) {
      const current = familyMap.get(item.family.key);
      if (current) {
        current.count += 1;
        continue;
      }

      familyMap.set(item.family.key, {
        key: item.family.key,
        label: item.family.label,
        iconName: item.family.iconName,
        count: 1,
      });
    }

    return Array.from(familyMap.values()).sort((left, right) => {
      if (left.count !== right.count) {
        return right.count - left.count;
      }
      return left.label.localeCompare(right.label, "zh-Hans-CN", {
        sensitivity: "base",
      });
    });
  }, [modelsWithFamily]);

  useEffect(() => {
    if (familyFilter === "all") return;
    const exists = familyOptions.some((item) => item.key === familyFilter);
    if (!exists) {
      setFamilyFilter("all");
    }
  }, [familyFilter, familyOptions]);

  useLayoutEffect(() => {
    const updateFamilyIndicator = () => {
      const container = familyTabsRef.current;
      if (!container) return;

      const activeKey = familyFilter === "all" ? "all" : familyFilter;
      const activeButton = familyButtonRefs.current[activeKey];
      if (!activeButton) {
        setFamilyIndicator((prev) => ({ ...prev, visible: false }));
        return;
      }

      const containerRect = container.getBoundingClientRect();
      const buttonRect = activeButton.getBoundingClientRect();
      const nextLeft = buttonRect.left - containerRect.left;
      const nextWidth = buttonRect.width;

      setFamilyIndicator({
        left: nextLeft,
        width: nextWidth,
        visible: nextWidth > 0,
      });
    };

    updateFamilyIndicator();
    window.addEventListener("resize", updateFamilyIndicator);
    return () => window.removeEventListener("resize", updateFamilyIndicator);
  }, [familyFilter, familyOptions]);

  const filteredModels = useMemo(() => {
    if (familyFilter === "all") {
      return modelsWithFamily.map((item) => item.model);
    }
    return modelsWithFamily
      .filter((item) => item.family.key === familyFilter)
      .map((item) => item.model);
  }, [familyFilter, modelsWithFamily]);

  const pages = useMemo(() => Math.ceil(filteredModels.length / pageSize), [filteredModels.length, pageSize]);

  useEffect(() => {
    const maxPage = Math.max(pages, 1);
    if (page > maxPage) {
      setPage(maxPage);
    }
  }, [page, pages]);

  const visibleModels = useMemo(() => {
    const start = (page - 1) * pageSize;
    return filteredModels.slice(start, start + pageSize);
  }, [filteredModels, page, pageSize]);

  const rowVirtualizer = useVirtualizer({
    count: visibleModels.length,
    getScrollElement: () => listRef.current,
    estimateSize: () => 108,
    overscan: 8,
  });

  const desktopGridTemplate = useMemo(() => {
    const modelWidth = widthSettings.model === "compact"
      ? "minmax(0,2.1fr)"
      : widthSettings.model === "wide"
      ? "minmax(0,2.8fr)"
      : "minmax(0,2.45fr)";
    const priceWidth = widthSettings.price === "compact" ? "6.5rem" : "7.5rem";
    const map: Record<ModelColumnKey, string> = {
      model: modelWidth,
      input: priceWidth,
      output: priceWidth,
      capabilities: "minmax(11rem,1fr)",
      status: "9rem",
      actions: "19rem",
    };
    const enabledColumns = (Object.keys(columnVisibility) as ModelColumnKey[]).filter((key) => columnVisibility[key]);
    return enabledColumns.map((key) => map[key]).join(" ");
  }, [columnVisibility, widthSettings]);

  const handleCreate = async (values: z.infer<typeof formSchema>) => {
    try {
      await createModel({
        name: values.name,
        remark: values.remark,
        max_retry: values.max_retry,
        time_out: values.time_out,
        io_log: values.io_log,
        strategy: values.strategy,
        breaker: values.breaker,
        capabilities: values.capabilities,
        input_price: Number(values.input_price),
        output_price: Number(values.output_price),
        cache_read_price: Number(values.cache_read_price),
        cache_write_price: Number(values.cache_write_price),
      });
      setOpen(false);
      toast.success(`模型: ${values.name} 创建成功`);
      form.reset({
        name: "",
        remark: "",
        max_retry: 10,
        time_out: 60,
        io_log: false,
        strategy: "lottery",
        breaker: false,
        capabilities: ["chat"],
        input_price: "0",
        output_price: "0",
        cache_read_price: "0",
        cache_write_price: "0",
      });
      await fetchModels();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`创建模型失败: ${message}`);
    }
  };

  const handleUpdate = async (values: z.infer<typeof formSchema>) => {
    if (!editingModel) return;
    try {
      await updateModel(editingModel.ID, {
        name: values.name,
        remark: values.remark,
        max_retry: values.max_retry,
        time_out: values.time_out,
        io_log: values.io_log,
        strategy: values.strategy,
        breaker: values.breaker,
        capabilities: values.capabilities,
        input_price: Number(values.input_price),
        output_price: Number(values.output_price),
        cache_read_price: Number(values.cache_read_price),
        cache_write_price: Number(values.cache_write_price),
      });
      const previousEnabled = editingModel.Status == null ? true : Number(editingModel.Status) === 1;
      if (previousEnabled !== values.status) {
        await updateModelStatus(editingModel.ID, values.status);
      }
      setOpen(false);
      toast.success(`模型: ${values.name} 更新成功`);
      setEditingModel(null);
      form.reset({
        name: "",
        remark: "",
        max_retry: 10,
        time_out: 60,
        io_log: false,
        strategy: "lottery",
        breaker: false,
        capabilities: ["chat"],
        input_price: "0",
        output_price: "0",
        cache_read_price: "0",
        cache_write_price: "0",
        status: true,
      });
      await fetchModels();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`更新模型失败: ${message}`);
      console.error(err);
    }
  };

  const handleDelete = async () => {
    if (!deleteId) return;
    try {
      const targetModel = models.find((model) => model.ID === deleteId);
      await deleteModel(deleteId);
      setDeleteId(null);
      await fetchModels();
      toast.success(`模型: ${targetModel?.Name ?? deleteId} 删除成功`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`删除模型失败: ${message}`);
      console.error(err);
    }
  };

  const openEditDialog = (model: Model) => {
    setEditingModel(model);
    const statusEnabled = isModelEnabled(model);
    const rawCapabilities = Array.isArray(model.Capabilities) ? model.Capabilities : [];
    const normalizedCapabilities = rawCapabilities
      .map((item) => item.trim().toLowerCase())
      .filter((item): item is ModelCapability => capabilityValues.includes(item as ModelCapability));
    form.reset({
      name: model.Name,
      remark: model.Remark,
      max_retry: model.MaxRetry,
      time_out: model.TimeOut,
      io_log: Boolean(model.IOLog),
      strategy: model.Strategy === "rotor" ? "rotor" : "lottery",
      breaker: Boolean(model.Breaker),
      capabilities: normalizedCapabilities.length > 0 ? normalizedCapabilities : ["chat"],
      input_price: String(model.InputPrice ?? 0),
      output_price: String(model.OutputPrice ?? 0),
      cache_read_price: String(model.CacheReadPrice ?? 0),
      cache_write_price: String(model.CacheWritePrice ?? 0),
      status: statusEnabled,
    });
    setOpen(true);
  };

  const openProviderPanel = (model: Model) => {
    setProviderPanelModel(model);
    setProviderPanelOpen(true);
  };

  const openCreateDialog = () => {
    setEditingModel(null);
    form.reset({
      name: "",
      remark: "",
      max_retry: 10,
      time_out: 60,
      io_log: false,
      strategy: "lottery",
      breaker: false,
      capabilities: ["chat"],
      input_price: "0",
      output_price: "0",
      cache_read_price: "0",
      cache_write_price: "0",
      status: true,
    });
    setOpen(true);
  };

  const openDeleteDialog = (id: number) => {
    setDeleteId(id);
  };

  const handlePageChange = (nextPage: number) => {
    const maxPage = Math.max(pages, 1);
    if (nextPage < 1 || nextPage > maxPage) return;
    setPage(nextPage);
  };

  const handleToggleStatus = async (model: Model, nextStatus: boolean) => {
    if (statusUpdatingIds.includes(model.ID)) return;

    setStatusUpdatingIds((prev) => [...prev, model.ID]);
    try {
      await updateModelStatus(model.ID, nextStatus);
      setModels((prev) => prev.map((item) => (
        item.ID === model.ID ? { ...item, Status: nextStatus ? 1 : 0 } : item
      )));
      toast.success(`模型 ${model.Name} 已${nextStatus ? "启用" : "停用"}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`更新模型状态失败: ${message}`);
    } finally {
      setStatusUpdatingIds((prev) => prev.filter((id) => id !== model.ID));
    }
  };

  const handleToggleColumn = (column: ModelColumnKey, checked: boolean) => {
    setColumnVisibility((prev) => {
      const next = { ...prev, [column]: checked };
      if (!Object.values(next).some(Boolean)) {
        return prev;
      }
      return next;
    });
  };

  const handleCopyModelName = async (modelName: string) => {
    try {
      await navigator.clipboard.writeText(modelName);
      toast.success(`已复制模型名：${modelName}`);
    } catch (error) {
      console.error("copy model name failed", error);
      toast.error("复制模型名失败");
    }
  };

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-2xl font-bold tracking-tight">模型管理</h2>
          </div>
          <div className="flex items-center gap-2">
            <div className="relative">
              <Search className="size-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="搜索模型"
                value={searchInput}
                onChange={(event) => setSearchInput(event.target.value)}
                className="h-8 w-44 rounded-full pl-9 text-xs bg-muted/60 border-transparent focus-visible:ring-1 focus-visible:ring-primary/40"
              />
            </div>
            <Select
              value={capabilityFilter}
              onValueChange={(value) => {
                setCapabilityFilter(value);
                setPage(1);
              }}
            >
              <SelectTrigger className="h-8 w-36 rounded-full bg-muted/60 text-xs">
                <SelectValue placeholder="模型类型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部类型</SelectItem>
                {capabilityOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8 rounded-full bg-muted/60 text-foreground hover:bg-muted/80"
              onClick={openCreateDialog}
              aria-label="添加模型"
              title="添加模型"
            >
              <Plus className="size-4" />
            </Button>
            <Popover>
              <PopoverTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 rounded-full bg-muted/60 text-foreground hover:bg-muted/80"
                  aria-label="列表显示设置"
                  title="列表显示设置"
                >
                  <SlidersHorizontal className="size-4" />
                </Button>
              </PopoverTrigger>
              <PopoverContent align="end" className="w-72 p-3 space-y-3">
                <div className="text-xs font-semibold text-foreground">列显示</div>
                <div className="grid grid-cols-2 gap-2">
                  {([
                    ["model", "模型"],
                    ["input", "输入价格"],
                    ["output", "输出价格"],
                    ["capabilities", "能力"],
                    ["status", "状态"],
                    ["actions", "操作"],
                  ] as [ModelColumnKey, string][]).map(([key, label]) => (
                    <label key={key} className="inline-flex items-center gap-2 text-xs text-foreground">
                      <Checkbox
                        checked={columnVisibility[key]}
                        onCheckedChange={(checked) => handleToggleColumn(key, checked === true)}
                      />
                      <span>{label}</span>
                    </label>
                  ))}
                </div>
                <div className="space-y-2 border-t border-border/60 pt-2">
                  <div className="text-xs font-semibold text-foreground">列宽</div>
                  <div className="space-y-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-xs text-muted-foreground">模型列</span>
                      <Select
                        value={widthSettings.model}
                        onValueChange={(value) => setWidthSettings((prev) => ({ ...prev, model: value as ModelWidthState["model"] }))}
                      >
                        <SelectTrigger className="h-8 w-28 text-xs">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="compact">紧凑</SelectItem>
                          <SelectItem value="default">标准</SelectItem>
                          <SelectItem value="wide">加宽</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-xs text-muted-foreground">价格列</span>
                      <Select
                        value={widthSettings.price}
                        onValueChange={(value) => setWidthSettings((prev) => ({ ...prev, price: value as ModelWidthState["price"] }))}
                      >
                        <SelectTrigger className="h-8 w-28 text-xs">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="compact">紧凑</SelectItem>
                          <SelectItem value="default">标准</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                  </div>
                </div>
              </PopoverContent>
            </Popover>
            <div className="flex items-center gap-1 rounded-full bg-muted/60 px-2 py-1">
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7 rounded-full"
                onClick={() => handlePageChange(page - 1)}
                disabled={page === 1}
                aria-label="上一页"
              >
                <ChevronLeft className="size-4" />
              </Button>
              <span className="text-xs font-medium tabular-nums text-muted-foreground">
                {page}/{Math.max(pages, 1)}
              </span>
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7 rounded-full"
                onClick={() => handlePageChange(page + 1)}
                disabled={page === pages || pages === 0}
                aria-label="下一页"
              >
                <ChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        </div>
        <div className="overflow-x-auto pb-1">
          <div ref={familyTabsRef} className="relative inline-flex min-w-max items-center gap-1 rounded-full border border-border/60 bg-muted/45 p-1">
          <span
            aria-hidden
            className={cn(
              "pointer-events-none absolute bottom-1 top-1 rounded-full bg-background shadow-sm transition-[transform,width,opacity] duration-300 ease-out",
              familyIndicator.visible ? "opacity-100" : "opacity-0",
            )}
            style={{
              width: familyIndicator.width,
              transform: `translateX(${familyIndicator.left}px)`,
            }}
          />
          <button
            type="button"
            ref={(node) => { familyButtonRefs.current.all = node; }}
            onClick={() => {
              setFamilyFilter("all");
              setPage(1);
            }}
            aria-pressed={familyFilter === "all"}
            className={cn(
              "relative z-10 inline-flex h-9 shrink-0 items-center gap-2 rounded-full px-3 text-sm font-semibold transition-colors duration-200",
              familyFilter === "all"
                ? "text-foreground"
                : "text-muted-foreground hover:text-foreground"
            )}
          >
            <span className="inline-flex size-5 items-center justify-center text-muted-foreground">
              <Boxes className="size-4" />
            </span>
            <span>全部</span>
          </button>
          {familyOptions.map((family) => (
            <button
              key={family.key}
              type="button"
              ref={(node) => { familyButtonRefs.current[family.key] = node; }}
              onClick={() => {
                setFamilyFilter(family.key);
                setPage(1);
              }}
              aria-pressed={familyFilter === family.key}
              className={cn(
                "relative z-10 inline-flex h-9 shrink-0 items-center gap-2 rounded-full px-3 text-sm font-semibold transition-colors duration-200",
                familyFilter === family.key
                  ? "text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              )}
            >
              <span className="inline-flex size-5 shrink-0 items-center justify-center">
                <ModelIcon name={family.iconName} size="sm" />
              </span>
              <span>{family.label}</span>
            </button>
          ))}
          </div>
        </div>
      </div>
      <div className="flex-1 min-h-0 overflow-hidden">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message="加载模型列表" />
          </div>
        ) : filteredModels.length === 0 ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
            {models.length === 0 ? "暂无模型数据" : "当前筛选条件下暂无模型"}
          </div>
        ) : (
          <div className="h-full flex flex-col">
            <div className="flex h-full min-h-0 flex-col overflow-hidden rounded-[28px] border border-border/70 bg-card/88 shadow-[0_18px_50px_rgba(98,71,47,0.08)]">
              <div
                className="hidden xl:grid xl:[grid-template-columns:var(--model-cols)] items-center gap-4 border-b border-border/60 px-5 py-3 text-xs font-medium text-muted-foreground"
                style={{ ["--model-cols" as any]: desktopGridTemplate }}
              >
                {columnVisibility.model ? <div>模型</div> : null}
                {columnVisibility.input ? <div className="text-center">输入</div> : null}
                {columnVisibility.output ? <div className="text-center">输出</div> : null}
                {columnVisibility.capabilities ? <div className="text-center">能力</div> : null}
                {columnVisibility.status ? <div className="text-center">状态</div> : null}
                {columnVisibility.actions ? <div className="text-right">操作</div> : null}
              </div>
              <div ref={listRef} className="flex-1 min-h-0 overflow-y-auto">
                <div className="relative w-full" style={{ height: rowVirtualizer.getTotalSize() }}>
                  {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                    const model = visibleModels[virtualRow.index];
                    if (!model) return null;
                  const enabled = isModelEnabled(model);
                  const statusUpdating = statusUpdatingIds.includes(model.ID);
                  return (
                    <div
                      key={model.ID}
                      data-index={virtualRow.index}
                      ref={rowVirtualizer.measureElement}
                      className="group absolute left-0 top-0 grid w-full gap-4 overflow-hidden border-b border-border/50 px-4 py-3 transform-gpu transition-[background-color,transform,box-shadow,border-color] duration-200 hover:translate-x-1 hover:border-border/30 hover:bg-gradient-to-r hover:from-primary/5 hover:via-background hover:to-background hover:shadow-[0_12px_28px_rgba(98,71,47,0.10)] last:border-b-0 xl:[grid-template-columns:var(--model-cols)] xl:items-center xl:px-5"
                      style={{
                        transform: `translateY(${virtualRow.start}px)`,
                        ["--model-cols" as any]: desktopGridTemplate,
                      }}
                    >
                      <div className="pointer-events-none absolute inset-y-2 left-0 w-1 rounded-r-full bg-primary/0 transition-all duration-200 group-hover:bg-primary/70 group-hover:shadow-[0_0_18px_rgba(180,104,56,0.28)]" />
                      {columnVisibility.model ? (
                      <div className="min-w-0">
                        <div className="mb-1 text-[11px] font-medium text-muted-foreground xl:hidden">模型</div>
                        <div className="grid min-w-0 grid-cols-[2.5rem_minmax(0,1fr)] items-center gap-3">
                          <ModelIcon name={model.Name} />
                          <div className="min-w-0 leading-tight">
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <button
                                  type="button"
                                  className="w-full truncate text-left cursor-pointer text-base font-semibold leading-6 transition-colors group-hover:text-primary"
                                  title={model.Name}
                                  onClick={() => void handleCopyModelName(model.Name)}
                                >
                                  {model.Name}
                                </button>
                              </TooltipTrigger>
                              <TooltipContent side="top" align="start">
                                {model.Name}
                              </TooltipContent>
                            </Tooltip>
                            {model.Remark ? (
                              <div className="mt-1 text-[11px] text-muted-foreground truncate" title={model.Remark}>
                                {model.Remark}
                              </div>
                            ) : null}
                          </div>
                        </div>
                      </div>
                      ) : null}

                      {columnVisibility.input ? (
                      <div className="min-w-0 xl:justify-self-center">
                        <div className="mb-1 text-[11px] font-medium text-muted-foreground xl:hidden">输入</div>
                        <div className="flex items-center gap-1 text-sm font-medium text-foreground/80 xl:justify-center">
                          <Coins className="size-3.5 text-emerald-500" />
                          <span className="tabular-nums">
                            {formatPrice(model.InputPrice)}$
                          </span>
                        </div>
                      </div>
                      ) : null}

                      {columnVisibility.output ? (
                      <div className="min-w-0 xl:justify-self-center">
                        <div className="mb-1 text-[11px] font-medium text-muted-foreground xl:hidden">输出</div>
                        <div className="flex items-center gap-1 text-sm font-medium text-foreground/80 xl:justify-center">
                          <Coins className="size-3.5 text-emerald-500" />
                          <span className="tabular-nums">
                            {formatPrice(model.OutputPrice)}$
                          </span>
                        </div>
                      </div>
                      ) : null}

                      {columnVisibility.capabilities ? (
                      <div className="min-w-0 xl:justify-self-center xl:w-full">
                        <div className="mb-1 text-[11px] font-medium text-muted-foreground xl:hidden">能力</div>
                        <div className="flex flex-wrap items-center gap-1.5 xl:justify-center">
                          {(model.Capabilities && model.Capabilities.length > 0 ? model.Capabilities : ["chat"]).map((capability) => {
                            const option = capabilityOptions.find((item) => item.value === capability);
                            if (!option) {
                              return (
                                <span key={capability} className="rounded-full border border-border/60 bg-muted/40 px-2 py-0.5 text-[10px] text-muted-foreground">
                                  {capability}
                                </span>
                              );
                            }
                            const Icon = option.icon;
                            return (
                              <span
                                key={capability}
                                className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium", option.activeClass)}
                              >
                                <Icon className={cn("size-3", option.iconClass)} />
                                <span>{option.label}</span>
                              </span>
                            );
                          })}
                        </div>
                      </div>
                      ) : null}

                      {columnVisibility.status ? (
                      <div className="min-w-0 xl:justify-self-center xl:w-full">
                        <div className="mb-1 text-[11px] font-medium text-muted-foreground xl:hidden">状态</div>
                        <div className="flex items-center gap-3 xl:justify-center">
                          <span
                            className={cn(
                              "inline-flex min-w-[52px] items-center justify-center rounded-full border px-2 py-0.5 text-[11px] font-medium",
                              enabled
                                ? "border-emerald-200 bg-emerald-50 text-emerald-700"
                                : "border-slate-200 bg-slate-100 text-slate-600"
                            )}
                          >
                            {enabled ? "启用" : "关闭"}
                          </span>
                          <Switch
                            checked={enabled}
                            disabled={statusUpdating}
                            onCheckedChange={(checked) => void handleToggleStatus(model, checked)}
                            aria-label={`${model.Name} 状态切换`}
                          />
                        </div>
                      </div>
                      ) : null}

                      {columnVisibility.actions ? (
                      <div className="flex flex-wrap items-center justify-start gap-2 xl:justify-self-end xl:justify-end">
                        <Button
                          variant="outline"
                          className="h-9 rounded-full px-4 leading-none"
                          onClick={(event) => {
                            event.stopPropagation();
                            openProviderPanel(model);
                          }}
                        >
                          <span className="inline-flex size-4 shrink-0 items-center justify-center">
                            <Boxes className="h-4 w-4" />
                          </span>
                          <span className="leading-none">提供商</span>
                        </Button>
                        <Button
                          variant="outline"
                          className="h-9 rounded-full px-4 leading-none"
                          onClick={(event) => {
                            event.stopPropagation();
                            openEditDialog(model);
                          }}
                        >
                          <span className="inline-flex size-4 shrink-0 items-center justify-center">
                            <Pencil className="h-4 w-4" />
                          </span>
                          <span className="leading-none">编辑</span>
                        </Button>
                        <AlertDialog>
                          <AlertDialogTrigger asChild>
                            <Button
                              variant="destructive"
                              className="h-9 rounded-full px-4 leading-none"
                              onClick={(event) => {
                                event.stopPropagation();
                                openDeleteDialog(model.ID);
                              }}
                            >
                              <span className="inline-flex size-4 shrink-0 items-center justify-center">
                                <Trash2 className="h-4 w-4" />
                              </span>
                              <span className="leading-none">删除</span>
                            </Button>
                          </AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>确定要删除这个模型吗？</AlertDialogTitle>
                              <AlertDialogDescription>此操作无法撤销。这将永久删除该模型。</AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel onClick={() => setDeleteId(null)}>取消</AlertDialogCancel>
                              <AlertDialogAction onClick={handleDelete}>确认删除</AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                      </div>
                      ) : null}
                    </div>
                  );
                })}
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingModel ? "编辑模型" : "添加模型"}
            </DialogTitle>
            <DialogDescription>
              {editingModel
                ? "修改模型信息"
                : "添加一个新的模型"}
            </DialogDescription>
          </DialogHeader>

          <Form {...form}>
            <form onSubmit={form.handleSubmit(editingModel ? handleUpdate : handleCreate)} className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
                <FormField
                  control={form.control}
                  name="name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>名称</FormLabel>
                      <FormControl>
                        <Input {...field} className="h-9" />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                {editingModel ? (
                  <FormField
                    control={form.control}
                    name="status"
                    render={({ field }) => (
                      <FormItem className="flex h-9 items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3">
                        <FormLabel className="text-xs text-muted-foreground">启用</FormLabel>
                        <FormControl>
                          <Switch
                            checked={field.value === true}
                            onCheckedChange={(checked) => field.onChange(checked === true)}
                            aria-label="切换模型启用状态"
                          />
                        </FormControl>
                      </FormItem>
                    )}
                  />
                ) : null}
              </div>

              <FormField
                control={form.control}
                name="remark"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>备注</FormLabel>
                    <FormControl>
                      <Textarea {...field} rows={2} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="capabilities"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className="flex items-center gap-2">
                      模型类型
                      <span className="text-xs text-muted-foreground">支持多选</span>
                    </FormLabel>
                    <FormControl>
                      <div className="flex flex-wrap gap-2">
                        {capabilityOptions.map((option) => {
                          const selected = field.value?.includes(option.value);
                          const Icon = option.icon;
                          return (
                            <button
                              key={option.value}
                              type="button"
                              onClick={() => {
                                const current = new Set<ModelCapability>(field.value ?? []);
                                if (current.has(option.value)) {
                                  current.delete(option.value);
                                } else {
                                  current.add(option.value);
                                }
                                field.onChange(Array.from(current));
                              }}
                              className={cn(
                                "inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-xs font-medium transition",
                                selected
                                  ? option.activeClass
                                  : "border-border/60 bg-muted/40 text-muted-foreground hover:bg-muted/60"
                              )}
                            >
                              <Icon className={cn("size-3.5", selected ? option.iconClass : "text-muted-foreground")} />
                              <span>{option.label}</span>
                            </button>
                          );
                        })}
                      </div>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className="grid gap-3 sm:grid-cols-2">
                <FormField
                  control={form.control}
                  name="input_price"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>输入价格($/M)</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          className="h-9"
                          step="0.0001"
                          inputMode="decimal"
                          {...field}
                          onChange={(e) => field.onChange(e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="output_price"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>输出价格($/M)</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          className="h-9"
                          step="0.0001"
                          inputMode="decimal"
                          {...field}
                          onChange={(e) => field.onChange(e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <FormField
                  control={form.control}
                  name="cache_read_price"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>缓存读价格($/M)</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          className="h-9"
                          step="0.0001"
                          inputMode="decimal"
                          {...field}
                          onChange={(e) => field.onChange(e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="cache_write_price"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>缓存写价格($/M)</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          className="h-9"
                          step="0.0001"
                          inputMode="decimal"
                          {...field}
                          onChange={(e) => field.onChange(e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <div className="grid grid-cols-2 gap-3">
                <FormField
                  control={form.control}
                  name="max_retry"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>重试次数</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          className="h-9"
                          {...field}
                          onChange={e => field.onChange(+e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="time_out"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>超时(秒)</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          className="h-9"
                          {...field}
                          onChange={e => field.onChange(+e.target.value)}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <div className="grid gap-3 sm:grid-cols-2">
                <FormField
                  control={form.control}
                  name="io_log"
                  render={({ field }) => (
                    <FormItem className="flex items-center justify-between rounded-lg border border-border/60 bg-muted/50 px-3 py-2">
                      <FormLabel className="text-xs text-muted-foreground">IO 记录</FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value === true}
                          onCheckedChange={(checked) => field.onChange(checked === true)}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="breaker"
                  render={({ field }) => (
                    <FormItem className="flex items-center justify-between rounded-lg border border-border/60 bg-muted/50 px-3 py-2">
                      <FormLabel className="text-xs text-muted-foreground">熔断</FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value === true}
                          onCheckedChange={(checked) => field.onChange(checked === true)}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />
              </div>

              <FormField
                control={form.control}
                name="strategy"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>负载均衡策略</FormLabel>
                    <Select value={field.value} onValueChange={field.onChange}>
                      <FormControl>
                        <SelectTrigger className="w-full">
                          <SelectValue placeholder="选择策略" />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value="lottery">抽签（权重随机）</SelectItem>
                        <SelectItem value="rotor">轮转（权重轮询）</SelectItem>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                  取消
                </Button>
                <Button type="submit">
                  {editingModel ? "更新" : "创建"}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={providerPanelOpen}
        onOpenChange={(nextOpen) => {
          setProviderPanelOpen(nextOpen);
          if (!nextOpen) {
            setProviderPanelModel(null);
          }
        }}
      >
        <DialogContent className="max-w-5xl max-h-[88vh] p-4 overflow-hidden">
          {providerPanelModel ? (
            <ModelProvidersPanel embedded fixedModel={providerPanelModel} />
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  );
}
