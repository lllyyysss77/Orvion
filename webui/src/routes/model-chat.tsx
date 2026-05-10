import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import Loading from "@/components/loading";
import { FaPaperPlane } from "react-icons/fa";
import {
  ChevronDown,
  ChevronRight,
  Copy,
  ImageIcon,
  Pencil,
  Plus,
  RefreshCw,
  Settings,
  Sparkles,
  Trash2,
} from "lucide-react";
import {
  getModels,
  getModelProviders,
  getProviders,
  type Model,
  type ModelWithProvider,
  type Provider,
} from "@/lib/api";
import { getStoredAuthToken } from "@/lib/auth";
import { toast } from "sonner";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

type ChatMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  status?: "streaming" | "done" | "error";
  imagePreviewUrl?: string;
};

type StreamMessageContentPart = {
  text?: string;
  content?: string;
};

type StreamChoice = {
  delta?: {
    content?: string | StreamMessageContentPart[];
  };
  message?: {
    content?: string;
  };
};

type StreamPayload = {
  type?: string;
  choices?: StreamChoice[];
  output_text?: string;
  output?: Array<{
    content?: Array<{
      text?: string;
    }>;
  }>;
  delta?: string | {
    text?: string;
    content?: string;
  };
  candidates?: Array<{
    content?: {
      parts?: Array<{
        text?: string;
      }>;
    };
  }>;
  content?: string;
  data?: {
    content?: string;
  };
  text?: string;
};

type AssistantResponsePayload = {
  content: string;
  imagePreviewUrl?: string;
};

const STREAMABLE_ENDPOINTS = ["chat/completions", "messages", "responses"];
const BASE_ENDPOINTS = ["chat/completions", "images/generations", "images/edits"];
const IMAGE_URL_PATTERN = /^https?:\/\/\S+\.(png|jpe?g|webp|gif|bmp|svg)(\?.*)?$/i;

const ThinkingBubble = () => (
  <div className="inline-flex items-center gap-2 rounded-full border border-fuchsia-200/70 bg-white/90 px-3 py-1.5 text-xs text-fuchsia-700 shadow-sm">
    <span className="font-medium">思考中</span>
    <span className="flex items-center gap-1">
      {[0, 1, 2].map((index) => (
        <span
          key={index}
          className="size-1.5 rounded-full bg-fuchsia-500/80 animate-bounce"
          style={{ animationDelay: `${index * 120}ms`, animationDuration: "0.9s" }}
        />
      ))}
    </span>
  </div>
);

export default function ModelChatTestPage() {
  const [models, setModels] = useState<Model[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [modelProviders, setModelProviders] = useState<ModelWithProvider[]>([]);
  const [selectedModelId, setSelectedModelId] = useState<string>("");
  const [modelSearch, setModelSearch] = useState("");
  const [selectedModelProviderId, setSelectedModelProviderId] = useState<string>("");
  const [endpoint, setEndpoint] = useState("chat/completions");
  const [prompt, setPrompt] = useState("");
  const [size, setSize] = useState("");
  const [imageFile, setImageFile] = useState<File | null>(null);
  const [maskFile, setMaskFile] = useState<File | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [modelDropdownOpen, setModelDropdownOpen] = useState(false);
  const [localImageEnabled, setLocalImageEnabled] = useState(false);
  const [modelCollapsed, setModelCollapsed] = useState(false);
  const [imageCollapsed, setImageCollapsed] = useState(false);
  const imageInputRef = useRef<HTMLInputElement | null>(null);
  const maskInputRef = useRef<HTMLInputElement | null>(null);
  const chatViewportRef = useRef<HTMLDivElement | null>(null);
  const previewUrlsRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (!localImageEnabled) {
      setImageFile(null);
      setMaskFile(null);
    }
  }, [localImageEnabled]);

  useEffect(() => {
    const viewport = chatViewportRef.current;
    if (!viewport) {
      return;
    }
    viewport.scrollTop = viewport.scrollHeight;
  }, [messages]);

  useEffect(() => {
    const previewUrls = previewUrlsRef.current;
    return () => {
      previewUrls.forEach((url) => URL.revokeObjectURL(url));
      previewUrls.clear();
    };
  }, []);

  useEffect(() => {
    const fetchBase = async () => {
      try {
        const [modelRes, providerRes] = await Promise.all([
          getModels({ page: 1, page_size: 100 }),
          getProviders(),
        ]);
        setModels(modelRes.data ?? []);
        setProviders(providerRes ?? []);
      } catch (err) {
        toast.error(err instanceof Error ? err.message : "获取模型信息失败");
      } finally {
        setLoading(false);
      }
    };
    fetchBase();
  }, []);

  useEffect(() => {
    const modelId = Number(selectedModelId);
    if (!modelId) {
      setModelProviders([]);
      setSelectedModelProviderId("");
      return;
    }
    const fetchProviders = async () => {
      try {
        const list = await getModelProviders(modelId);
        setModelProviders(list ?? []);
        setSelectedModelProviderId("");
      } catch (err) {
        toast.error(err instanceof Error ? err.message : "获取模型关联提供商失败");
      }
    };
    fetchProviders();
  }, [selectedModelId]);

  const providerMap = useMemo(() => {
    const map = new Map<number, Provider>();
    providers.forEach((provider) => map.set(provider.ID, provider));
    return map;
  }, [providers]);

  const selectedModel = models.find((item) => String(item.ID) === selectedModelId);
  const selectedModelProviderLabel = useMemo(() => {
    const item = modelProviders.find((entry) => String(entry.ID) === selectedModelProviderId);
    if (!item) {
      return "";
    }
    const provider = providerMap.get(item.ProviderID);
    return provider ? provider.Name : "未知提供商";
  }, [modelProviders, providerMap, selectedModelProviderId]);
  const filteredModels = useMemo(() => {
    const keyword = modelSearch.trim().toLowerCase();
    if (!keyword) {
      return models;
    }
    return models.filter((model) => model.Name.toLowerCase().includes(keyword));
  }, [modelSearch, models]);

  useEffect(() => {
    if (!selectedModelId) {
      return;
    }
    const model = models.find((item) => String(item.ID) === selectedModelId);
    if (model && model.Name !== modelSearch) {
      setModelSearch(model.Name);
    }
  }, [selectedModelId, models, modelSearch]);

  const normalizedCapabilities = useMemo<string[]>(() => {
    const raw = selectedModel?.Capabilities ?? [];
    if (!raw || raw.length === 0) {
      return ["chat"];
    }
    return raw.map((item) => item.toLowerCase().trim());
  }, [selectedModel]);

  const allowedEndpoints = useMemo(() => {
    const endpointSet = new Set<string>(BASE_ENDPOINTS);
    if (normalizedCapabilities.includes("chat") && normalizedCapabilities.includes("vision")) {
      endpointSet.add("messages");
      endpointSet.add("responses");
    }
    if (normalizedCapabilities.includes("video")) {
      endpointSet.add("videos");
    }
    if (normalizedCapabilities.includes("embedding")) {
      endpointSet.add("embeddings");
    }
    return Array.from(endpointSet);
  }, [normalizedCapabilities]);

  useEffect(() => {
    if (!selectedModelId) {
      return;
    }
    if (allowedEndpoints.length === 0) {
      return;
    }
    if (!allowedEndpoints.includes(endpoint)) {
      setEndpoint(allowedEndpoints[0]);
    }
  }, [allowedEndpoints, endpoint, selectedModelId]);

  const chooseModel = (id: string) => {
    setSelectedModelId(id);
    const model = models.find((item) => String(item.ID) === id);
    setModelSearch(model?.Name ?? "");
    setModelDropdownOpen(false);
  };

  const toRenderableMarkdown = (content: string): string => {
    const normalized = content.replace(/\)\s*!\[/g, ")\n![");
    return normalized
      .split("\n")
      .map((line) => {
        const trimmed = line.trim();
        if (IMAGE_URL_PATTERN.test(trimmed)) {
          return `![生成图片](${trimmed})`;
        }
        return line;
      })
      .join("\n");
  };

  const updateAssistantMessage = (id: string, updater: (message: ChatMessage) => ChatMessage) => {
    setMessages((prev) =>
      prev.map((message) => {
        if (message.id !== id) {
          return message;
        }
        return updater(message);
      })
    );
  };

  const appendAssistantContent = (id: string, chunk: string) => {
    if (!chunk) {
      return;
    }
    updateAssistantMessage(id, (message) => ({
      ...message,
      content: `${message.content}${chunk}`,
      status: "streaming",
    }));
  };

  const extractSseText = (rawData: string): string => {
    const data = rawData.trim();
    if (!data || data === "[DONE]") {
      return "";
    }
    try {
      const parsed = JSON.parse(data) as StreamPayload;
      const choice = parsed.choices?.[0];
      if (typeof choice?.delta?.content === "string") return choice.delta.content;
      if (Array.isArray(choice?.delta?.content)) {
        return choice.delta.content
          .map((item) => item?.text || item?.content || "")
          .join("");
      }
      if (typeof choice?.message?.content === "string") return choice.message.content;
      if (typeof parsed.output_text === "string") return parsed.output_text;
      if (typeof parsed.delta === "string") return parsed.delta;
      if (typeof parsed.delta === "object" && parsed.delta !== null) {
        if (typeof parsed.delta.text === "string") return parsed.delta.text;
        if (typeof parsed.delta.content === "string") return parsed.delta.content;
      }
      if (Array.isArray(parsed.output)) {
        return parsed.output
          .flatMap((item) => item?.content ?? [])
          .map((item) => item?.text ?? "")
          .join("");
      }
      if (Array.isArray(parsed.candidates)) {
        return parsed.candidates
          .flatMap((item) => item?.content?.parts ?? [])
          .map((part) => part?.text ?? "")
          .join("");
      }
      if (typeof parsed.content === "string") return parsed.content;
      if (typeof parsed.data?.content === "string") return parsed.data.content;
      if (typeof parsed.text === "string") return parsed.text;
    } catch {
      return data;
    }
    return "";
  };

  const extractAssistantPayload = (payload: unknown): AssistantResponsePayload => {
    if (typeof payload === "string") {
      return { content: payload };
    }
    if (!payload || typeof payload !== "object") {
      return { content: "" };
    }
    const root = payload as Record<string, unknown>;
    const nested =
      root.data && typeof root.data === "object"
        ? (root.data as Record<string, unknown>)
        : root;

    let content = "";
    if (typeof nested.content === "string") {
      content = nested.content;
    } else if (typeof root.content === "string") {
      content = root.content;
    }

    let imagePreviewUrl: string | undefined;
    if (typeof nested.image_url === "string") {
      imagePreviewUrl = nested.image_url;
    } else if (typeof nested.imageUrl === "string") {
      imagePreviewUrl = nested.imageUrl;
    } else if (typeof root.image_url === "string") {
      imagePreviewUrl = root.image_url;
    } else if (typeof root.imageUrl === "string") {
      imagePreviewUrl = root.imageUrl;
    }

    return { content, imagePreviewUrl };
  };

  const streamFromSse = async (
    response: Response,
    assistantId: string
  ): Promise<void> => {
    const reader = response.body?.getReader();
    if (!reader) {
      return;
    }
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      const blocks = buffer.split("\n\n");
      buffer = blocks.pop() ?? "";
      for (const block of blocks) {
        const lines = block.split("\n");
        let eventType = "message";
        let data = "";
        for (const line of lines) {
          if (line.startsWith("event:")) {
            eventType = line.slice(6).trim();
          } else if (line.startsWith("data:")) {
            data += `${line.slice(5).trim()}\n`;
          }
        }
        const normalized = data.trim();
        if (eventType === "error" && normalized) {
          throw new Error(normalized);
        }
        if (!normalized || normalized === "[DONE]") {
          continue;
        }
        const text = extractSseText(normalized);
        appendAssistantContent(assistantId, text);
      }
    }
  };

  const typewriterRender = async (text: string, assistantId: string): Promise<void> => {
    if (!text) {
      return;
    }
    const step = text.length > 1200 ? 8 : 4;
    for (let index = 0; index < text.length; index += step) {
      appendAssistantContent(assistantId, text.slice(index, index + step));
      await new Promise((resolve) => setTimeout(resolve, 12));
    }
  };

  const collectSseText = (raw: string): string => {
    return raw
      .split("\n")
      .filter((line) => line.startsWith("data:"))
      .map((line) => extractSseText(line.slice(5).trim()))
      .join("");
  };

  const doTestRequest = async (promptText: string, selectedProviderId: number): Promise<Response> => {
    const token = getStoredAuthToken();
    const headers: Record<string, string> = {};
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }
    const shouldUseFileRequest = localImageEnabled && Boolean(imageFile);
    if (shouldUseFileRequest) {
      const formData = new FormData();
      formData.append("prompt", promptText);
      formData.append("endpoint", endpoint);
      formData.append("stream", STREAMABLE_ENDPOINTS.includes(endpoint) ? "true" : "false");
      if (size.trim()) {
        formData.append("size", size.trim());
      }
      formData.append("image", imageFile as File);
      if (maskFile && endpoint === "images/edits") {
        formData.append("mask", maskFile);
      }
      return fetch(`/api/test/chat/${selectedProviderId}`, {
        method: "POST",
        headers,
        body: formData,
      });
    }
    if (endpoint === "images/edits") {
      throw new Error("请上传图片文件");
    }
    headers["Content-Type"] = "application/json";
    const payload: Record<string, unknown> = {
      prompt: promptText,
      endpoint,
      stream: STREAMABLE_ENDPOINTS.includes(endpoint),
    };
    if (size.trim()) {
      payload.size = size.trim();
    }
    return fetch(`/api/test/chat/${selectedProviderId}`, {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
    });
  };

  const handleSubmit = async () => {
    if (!selectedModelProviderId) {
      toast.error("请选择模型关联提供商");
      return;
    }
    const promptText = prompt.trim();
    if (!promptText) {
      toast.error("请输入测试内容");
      return;
    }
    const userImagePreviewUrl = localImageEnabled && imageFile ? URL.createObjectURL(imageFile) : undefined;
    if (userImagePreviewUrl) {
      previewUrlsRef.current.add(userImagePreviewUrl);
    }
    const userMessageId = `u-${Date.now()}`;
    const assistantMessageId = `a-${Date.now()}`;
    setMessages((prev) => [
      ...prev,
      { id: userMessageId, role: "user", content: promptText, status: "done", imagePreviewUrl: userImagePreviewUrl },
      { id: assistantMessageId, role: "assistant", content: "", status: "streaming" },
    ]);
    setPrompt("");
    setSubmitting(true);
    try {
      const response = await doTestRequest(promptText, Number(selectedModelProviderId));
      if (!response.ok) {
        const message = (await response.text()) || "测试请求失败";
        throw new Error(message);
      }
      const contentType = response.headers.get("content-type") ?? "";
      if (contentType.includes("text/event-stream") && STREAMABLE_ENDPOINTS.includes(endpoint)) {
        await streamFromSse(response, assistantMessageId);
      } else if (contentType.includes("text/event-stream")) {
        const raw = await response.text();
        const merged = collectSseText(raw);
        updateAssistantMessage(assistantMessageId, (message) => ({
          ...message,
          content: merged || message.content,
        }));
      } else {
        const json = await response.json();
        const payload = extractAssistantPayload(json);
        updateAssistantMessage(assistantMessageId, (message) => ({
          ...message,
          imagePreviewUrl: payload.imagePreviewUrl || message.imagePreviewUrl,
        }));
        if (payload.content.trim()) {
          await typewriterRender(payload.content, assistantMessageId);
        } else if (payload.imagePreviewUrl) {
          updateAssistantMessage(assistantMessageId, (message) => ({
            ...message,
            content: message.content || "图片已生成，请查看预览",
          }));
        }
      }
      updateAssistantMessage(assistantMessageId, (message) => ({
        ...message,
        status: "done",
      }));
    } catch (err) {
      const message = err instanceof Error ? err.message : "测试请求失败";
      updateAssistantMessage(assistantMessageId, (current) => ({
        ...current,
        content: current.content || `请求失败：${message}`,
        status: "error",
      }));
      toast.error(message);
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopyMessage = async (content: string) => {
    if (!content.trim()) {
      return;
    }
    try {
      await navigator.clipboard.writeText(content);
      toast.success("已复制消息内容");
    } catch {
      toast.error("复制失败");
    }
  };

  const handleEditMessage = (content: string) => {
    setPrompt(content);
  };

  const handleDeleteMessage = (id: string) => {
    setMessages((prev) => {
      const target = prev.find((message) => message.id === id);
      if (target?.imagePreviewUrl && target.imagePreviewUrl.startsWith("blob:")) {
        URL.revokeObjectURL(target.imagePreviewUrl);
        previewUrlsRef.current.delete(target.imagePreviewUrl);
      }
      return prev.filter((message) => message.id !== id);
    });
  };

  const getLastUserMessage = (): string => {
    for (let index = messages.length - 1; index >= 0; index -= 1) {
      if (messages[index].role === "user") {
        return messages[index].content;
      }
    }
    return "";
  };

  if (loading) {
    return (
      <div className="flex h-full min-h-0 items-center justify-center">
        <Loading message="加载模型信息" />
      </div>
    );
  }

  const endpointIsMedia = endpoint.startsWith("images/") || endpoint === "videos";
  const endpointBadge = endpointIsMedia ? "媒体测试" : "对话测试";
  const promptPlaceholder = endpoint === "embeddings"
    ? "请输入要生成向量的文本"
    : endpointIsMedia
    ? "描述你想生成/编辑的内容"
    : "请输入一段对话内容";

  return (
    <div className="h-full overflow-hidden">
      <div className="grid h-full min-h-0 grid-cols-1 gap-4 lg:grid-cols-[310px_1fr]">
        <aside className="h-full min-h-0 overflow-y-auto rounded-[30px] border border-border/65 bg-card/82 p-5 shadow-[0_18px_50px_rgba(98,71,47,0.07)]">
          <div className="flex items-center gap-3 px-1">
            <Settings className="h-5 w-5 text-foreground/80" />
            <h2 className="text-2xl font-semibold leading-none tracking-tight text-foreground">模型配置</h2>
          </div>

          <div className="mt-6 space-y-5">
            <div className="space-y-2">
              <button
                type="button"
                onClick={() => setModelCollapsed((prev) => !prev)}
                className="flex w-full items-center justify-between rounded-lg px-1 py-1.5 text-left"
              >
                <div className="flex items-center gap-2 text-base font-semibold text-foreground">
                  <Sparkles className="h-5 w-5 text-foreground/75" />
                  <span>模型</span>
                </div>
                {modelCollapsed ? <ChevronRight className="h-4 w-4 text-foreground/70" /> : <ChevronDown className="h-4 w-4 text-foreground/70" />}
              </button>
              {!modelCollapsed && (
                <div className="space-y-2">
                  <div className="relative">
                    <Input
                      value={modelSearch}
                      onFocus={() => setModelDropdownOpen(true)}
                      onBlur={() => setTimeout(() => setModelDropdownOpen(false), 120)}
                      onChange={(event) => {
                        const value = event.target.value;
                        setModelSearch(value);
                        setModelDropdownOpen(true);
                        if (selectedModel && value !== selectedModel.Name) {
                          setSelectedModelId("");
                        }
                      }}
                      placeholder="搜索并选择模型"
                      className="h-9 bg-white"
                    />
                    {modelDropdownOpen && (
                      <div className="absolute z-20 mt-1 max-h-56 w-full overflow-y-auto rounded-md border border-border bg-popover shadow-md">
                        {filteredModels.length === 0 ? (
                          <div className="px-3 py-2 text-sm text-muted-foreground">无匹配模型</div>
                        ) : (
                          filteredModels.map((model) => (
                            <button
                              key={model.ID}
                              type="button"
                              className="flex w-full items-center justify-between px-3 py-2 text-left text-sm transition-colors hover:bg-muted"
                              onMouseDown={() => chooseModel(String(model.ID))}
                            >
                              <span>{model.Name}</span>
                              {String(model.ID) === selectedModelId && (
                                <span className="text-xs text-primary">已选</span>
                              )}
                            </button>
                          ))
                        )}
                      </div>
                    )}
                  </div>
                  <div className="grid grid-cols-2 gap-2">
                    <div className="min-w-0">
                      <Select
                        value={selectedModelProviderId}
                        onValueChange={setSelectedModelProviderId}
                        disabled={!selectedModelId}
                      >
                        <SelectTrigger className="h-9 w-full bg-white">
                          <SelectValue placeholder="选择关联提供商" />
                        </SelectTrigger>
                        <SelectContent>
                          {modelProviders.map((item) => {
                            const provider = providerMap.get(item.ProviderID);
                            const label = provider
                              ? provider.Name
                              : "未知提供商";
                            return (
                              <SelectItem key={item.ID} value={String(item.ID)}>
                                {label}
                              </SelectItem>
                            );
                          })}
                        </SelectContent>
                      </Select>
                    </div>
                    <div className="min-w-0">
                      <Select
                        value={endpoint}
                        onValueChange={setEndpoint}
                        disabled={!selectedModelId || allowedEndpoints.length === 0}
                      >
                        <SelectTrigger className="h-9 w-full bg-white">
                          <SelectValue placeholder="选择接口" />
                        </SelectTrigger>
                        <SelectContent>
                          {allowedEndpoints.map((item) => (
                            <SelectItem key={item} value={item}>
                              {item}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  </div>
                  {(endpoint === "images/generations" || endpoint === "images/edits") && (
                    <Input
                      value={size}
                      onChange={(event) => setSize(event.target.value)}
                      placeholder="图片尺寸（例如 1024x1024）"
                      className="h-9 bg-white"
                    />
                  )}
                </div>
              )}
            </div>

            <div className="space-y-2">
              <button
                type="button"
                onClick={() => setImageCollapsed((prev) => !prev)}
                className="flex w-full items-center justify-between rounded-lg px-1 py-1.5 text-left"
              >
                <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                  <ImageIcon className="h-5 w-5 text-foreground/75" />
                  <span>图片上传</span>
                </div>
                <div className="flex items-center gap-2">
                  <Switch
                    checked={localImageEnabled}
                    onCheckedChange={setLocalImageEnabled}
                    onClick={(event) => event.stopPropagation()}
                  />
                  <button
                    type="button"
                    onClick={(event) => {
                      event.stopPropagation();
                      if (!localImageEnabled) {
                        return;
                      }
                      setImageCollapsed(false);
                      imageInputRef.current?.click();
                    }}
                    disabled={!localImageEnabled}
                    className="inline-flex h-7 w-7 items-center justify-center rounded-full border border-border/60 bg-background text-foreground/70 transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    <Plus className="h-4 w-4" />
                  </button>
                  {imageCollapsed ? <ChevronRight className="h-4 w-4 text-foreground/70" /> : <ChevronDown className="h-4 w-4 text-foreground/70" />}
                </div>
              </button>
              {!imageCollapsed && (
                <div className="space-y-2">
                  <div className="space-y-2 rounded-xl border border-border/60 bg-white/70 p-2.5">
                    <p className="text-xs text-muted-foreground">仅支持本地图片上传。请先开启开关，再点击右侧 + 号选择图片。</p>
                    <input
                      ref={imageInputRef}
                      type="file"
                      accept="image/*"
                      className="hidden"
                      onChange={(event) => setImageFile(event.target.files?.[0] ?? null)}
                    />
                    <div className="rounded-lg border border-border/60 bg-background/80 px-3 py-2 text-xs text-muted-foreground">
                      主图片：{imageFile ? imageFile.name : "未选择"}
                    </div>
                    {endpoint === "images/edits" && (
                      <>
                        <input
                          ref={maskInputRef}
                          type="file"
                          accept="image/*"
                          className="hidden"
                          onChange={(event) => setMaskFile(event.target.files?.[0] ?? null)}
                        />
                        <div className="flex items-center gap-2">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-8 rounded-full px-3"
                            disabled={!localImageEnabled}
                            onClick={() => maskInputRef.current?.click()}
                          >
                            上传遮罩
                          </Button>
                          <span className="min-w-0 truncate text-xs text-muted-foreground">
                            {maskFile ? maskFile.name : "未选择遮罩"}
                          </span>
                        </div>
                      </>
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        </aside>

        <section className="flex h-full min-h-0 flex-col rounded-[32px] border border-border/65 bg-background/74 shadow-[0_20px_56px_rgba(98,71,47,0.08)] backdrop-blur-xl">
          <div className="flex items-start justify-between gap-3 px-6 pb-3 pt-6">
            <div>
              <h1 className="text-2xl font-semibold tracking-tight text-foreground">AI 对话</h1>
              <p className="text-sm text-muted-foreground">
                {selectedModel?.Name || "未选择模型"}
                {selectedModelProviderLabel ? ` · ${selectedModelProviderLabel}` : ""}
              </p>
            </div>
            <div className="rounded-full border border-border/60 bg-white/80 px-3 py-1 text-xs text-muted-foreground">
              调试模式 · {endpointBadge}
            </div>
          </div>

          <div ref={chatViewportRef} className="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 pb-4">
            {messages.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-border/60 bg-white/55 px-4 py-3 text-sm text-muted-foreground">
                在下方输入测试内容后发送，右侧会按聊天气泡展示请求与响应结果。
              </div>
            ) : (
              messages.map((message) => (
                <div
                  key={message.id}
                  className={message.role === "user" ? "ml-auto max-w-[88%]" : "mr-auto max-w-[88%]"}
                >
                  {message.imagePreviewUrl ? (
                    <div className={message.role === "user" ? "mb-2 flex justify-end pr-8" : "mb-2 flex justify-start pl-8"}>
                      <img
                        src={message.imagePreviewUrl}
                        alt={message.role === "user" ? "上传图片预览" : "生成图片预览"}
                        className="max-h-72 w-auto max-w-[320px] rounded-xl border border-border/60 object-contain shadow-sm"
                        loading="lazy"
                      />
                    </div>
                  ) : null}
                  <div className={message.role === "user" ? "flex items-start justify-end gap-2" : "flex items-start gap-2"}>
                    {message.role === "assistant" && (
                      <div className="mt-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-fuchsia-400 to-cyan-400 text-[11px] font-semibold text-white">
                        AI
                      </div>
                    )}
                    <div
                      className={
                        message.role === "user"
                          ? "rounded-2xl rounded-tr-md bg-blue-600 px-5 py-3 text-sm leading-6 text-white shadow-sm"
                          : "rounded-2xl rounded-tl-md bg-muted px-5 py-3 text-sm leading-7 text-foreground"
                      }
                    >
                      {message.role === "assistant" ? (
                        message.status === "streaming" && !message.content.trim() ? (
                          <ThinkingBubble />
                        ) : (
                          <ReactMarkdown
                            remarkPlugins={[remarkGfm]}
                            components={{
                              h1: ({ children }) => <h1 className="mb-3 text-lg font-semibold">{children}</h1>,
                              h2: ({ children }) => <h2 className="mb-2 text-base font-semibold">{children}</h2>,
                              h3: ({ children }) => <h3 className="mb-2 text-sm font-semibold">{children}</h3>,
                              p: ({ children }) => <p className="mb-3 text-sm leading-6 last:mb-0">{children}</p>,
                              ul: ({ children }) => <ul className="mb-3 list-disc space-y-1 pl-5">{children}</ul>,
                              ol: ({ children }) => <ol className="mb-3 list-decimal space-y-1 pl-5">{children}</ol>,
                              code: ({ className, children }) =>
                                !className ? (
                                  <code className="rounded bg-muted-foreground/15 px-1 py-0.5 font-mono text-xs">{children}</code>
                                ) : (
                                  <pre className="overflow-x-auto rounded-lg bg-muted/80 p-3 text-xs">
                                    <code className="font-mono">{children}</code>
                                  </pre>
                                ),
                              img: ({ src, alt }) => (
                                <img
                                  src={src}
                                  alt={alt || "markdown-image"}
                                  className="my-2 max-h-80 w-auto max-w-full rounded-lg border border-border/60 object-contain"
                                  loading="lazy"
                                />
                              ),
                              blockquote: ({ children }) => (
                                <blockquote className="mb-3 border-l-2 border-primary/40 pl-3 text-muted-foreground">
                                  {children}
                                </blockquote>
                              ),
                            }}
                          >
                            {toRenderableMarkdown(message.content || "(无内容)")}
                          </ReactMarkdown>
                        )
                      ) : (
                        <p className="whitespace-pre-wrap break-words">{message.content}</p>
                      )}
                    </div>
                    {message.role === "user" && (
                      <div className="mt-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-lime-500 text-[11px] font-semibold text-white">
                        L
                      </div>
                    )}
                  </div>
                  <div className={message.role === "user" ? "mt-2 flex justify-end gap-2 pr-8" : "mt-2 flex gap-2 pl-8"}>
                    {message.role === "assistant" && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 rounded-full text-muted-foreground"
                        onClick={() => setPrompt(getLastUserMessage())}
                      >
                        <RefreshCw className="h-4 w-4" />
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 rounded-full text-muted-foreground"
                      onClick={() => handleCopyMessage(message.content)}
                    >
                      <Copy className="h-4 w-4" />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 rounded-full text-muted-foreground"
                      onClick={() => handleEditMessage(message.content)}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 rounded-full text-muted-foreground"
                      onClick={() => handleDeleteMessage(message.id)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </div>
              ))
            )}
          </div>

          <div className="px-6 pb-6 pt-3">
            <div className="rounded-3xl border border-border/60 bg-white/80 p-3 shadow-sm backdrop-blur-sm">
              <Textarea
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                    event.preventDefault();
                    if (!submitting) {
                      void handleSubmit();
                    }
                  }
                }}
                rows={2}
                placeholder={promptPlaceholder}
                className="min-h-[44px] resize-none border-none bg-transparent px-1 py-1 shadow-none focus-visible:ring-0"
              />
              <div className="mt-3 flex items-center justify-end gap-3">
                <Button onClick={handleSubmit} disabled={submitting} className="gap-2 rounded-full px-5">
                  <FaPaperPlane />
                  {submitting ? "发送中..." : "发送测试"}
                </Button>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
