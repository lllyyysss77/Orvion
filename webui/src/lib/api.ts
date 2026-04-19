// API client for interacting with the backend
import { getStoredAuthToken, getStoredAuthTokenMode } from "./auth";

const API_BASE = '/api';

export interface Provider {
  ID: number;
  Name: string;
  Config: string;
  Console: string;
  ModelsFetchMode?: "v1_models" | "api_pricing" | string;
}

export interface Model {
  ID: number;
  Name: string;
  Remark: string;
  MaxRetry: number;
  TimeOut: number;
  // 后端当前返回为 0/1（对应 models.io_log）
  IOLog: number;
  Strategy: string;
  // 后端当前返回为 0/1（对应 models.breaker）
  Breaker?: number | null;
  // 后端当前返回为 0/1（对应 models.status）
  Status?: number | null;
  Capabilities?: string[] | null;
  InputPrice?: number | null;
  OutputPrice?: number | null;
}

export interface AuthKeySummary {
  name: string;
  keyMasked: string;
  expiresAt: string | null;
  expireInDays: number | null;
  totalCost: number;
  totalRequests: number;
  successRequests: number;
  failureRequests: number;
  totalTimeMs: number;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  inputCost: number;
  outputCost: number;
  allowAll: boolean;
  models: string[];
}

export interface ModelWithProvider {
  ID: number;
  ModelID: number;
  ProviderModel: string;
  ProviderID: number;
  WithHeader: boolean;
  CustomerHeaders: Record<string, string> | null;
  Status: boolean | null;
  Weight: number;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
  pages: number;
}

export interface AuthKey {
  ID: number;
  CreatedAt: string;
  UpdatedAt: string;
  DeletedAt?: string | null;
  Name: string;
  Key: string;
  Status: boolean;
  AllowAll: boolean;
  Models: string[] | null;
  ExpiresAt: string | null;
  RpmLimit: number;
  UsageCount: number;
  TotalCost: number;
  LastUsedAt: string | null;
}

const toBoolean = (value: unknown): boolean => value === true || value === 1 || value === "1";

const parseRecordStringString = (value: unknown): Record<string, string> => {
  if (!value) return {};
  if (typeof value === "object") return value as Record<string, string>;
  if (typeof value !== "string") return {};
  const trimmed = value.trim();
  if (!trimmed) return {};
  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (parsed && typeof parsed === "object") return parsed as Record<string, string>;
    return {};
  } catch {
    return {};
  }
};

const parseStringArray = (value: unknown): string[] => {
  if (!value) return [];
  if (Array.isArray(value)) return value.filter((v) => typeof v === "string");
  if (typeof value !== "string") return [];
  const trimmed = value.trim();
  if (!trimmed) return [];
  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (Array.isArray(parsed)) return parsed.filter((v) => typeof v === "string");
    return [];
  } catch {
    return [];
  }
};

const asRecord = (value: unknown): Record<string, unknown> => (
  value && typeof value === "object" ? value as Record<string, unknown> : {}
);

const normalizeAuthKey = (raw: unknown): AuthKey => {
  const record = asRecord(raw);
  const allowAll = toBoolean(record.AllowAll);
  return {
    ...record,
    Status: toBoolean(record.Status),
    AllowAll: allowAll,
    Models: allowAll ? [] : parseStringArray(record.Models),
    RpmLimit: typeof record.RpmLimit === "number" ? record.RpmLimit : 0,
  } as AuthKey;
};

const normalizeModelWithProvider = (raw: unknown): ModelWithProvider => {
  const record = asRecord(raw);
  return {
    ...record,
    WithHeader: toBoolean(record.WithHeader),
    Status: record.Status == null ? null : toBoolean(record.Status),
    CustomerHeaders: parseRecordStringString(record.CustomerHeaders),
  } as ModelWithProvider;
};

export interface SystemConfig {
  enable_smart_routing: boolean;
  success_rate_weight: number;
  response_time_weight: number;
  decay_threshold_hours: number;
  min_weight: number;
}

export interface SystemStatus {
  total_providers: number;
  total_models: number;
  active_requests: number;
  uptime: string;
  version: string;
}

// Generic API request function
async function apiRequest<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const url = `${API_BASE}${endpoint}`;

  const token = getStoredAuthToken();

  const response = await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      ...options.headers,
    },
    ...options,
  });

  // Handle 401 Unauthorized response
  if (response.status === 401) {
    // Redirect to login page
    window.location.href = '/login';
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    throw new Error(`API request failed: ${response.status} ${response.statusText}`);
  }

  const data = await response.json();
  if (data.code !== 200) {
    throw new Error(`${data.message}`);
  }
  return data.data as T;
}

async function apiRequestFormData<T>(endpoint: string, formData: FormData): Promise<T> {
  const url = `${API_BASE}${endpoint}`;
  const token = getStoredAuthToken();

  const response = await fetch(url, {
    method: "POST",
    headers: {
      ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
    },
    body: formData,
  });

  if (response.status === 401) {
    window.location.href = '/login';
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    throw new Error(`API request failed: ${response.status} ${response.statusText}`);
  }

  const data = await response.json();
  if (data.code !== 200) {
    throw new Error(`${data.message}`);
  }
  return data.data as T;
}

async function authKeyRequest<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const token = getStoredAuthToken();

  const response = await fetch(endpoint, {
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      ...options.headers,
    },
    ...options,
  });

  if (response.status === 401) {
    window.location.href = '/login';
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    throw new Error(`API request failed: ${response.status} ${response.statusText}`);
  }

  const data = await response.json();
  if (data.code !== 200) {
    throw new Error(`${data.message}`);
  }
  return data.data as T;
}

export async function getVersion(): Promise<string> {
  return apiRequest<string>('/version');
}

export async function getAuthKeySummary(): Promise<AuthKeySummary> {
  return authKeyRequest<AuthKeySummary>('/auth-key/summary');
}

// Provider API functions
export async function getProviders(filters: {
  name?: string;
} = {}, options: { signal?: AbortSignal } = {}): Promise<Provider[]> {
  const params = new URLSearchParams();

  if (filters.name) params.append("name", filters.name);

  const queryString = params.toString();
  const endpoint = queryString ? `/providers?${queryString}` : '/providers';

  return apiRequest<Provider[]>(endpoint, { signal: options.signal });
}

export async function createProvider(provider: {
  name: string;
  config: string;
  console: string;
  models_fetch_mode?: "v1_models" | "api_pricing";
}): Promise<Provider> {
  return apiRequest<Provider>('/providers', {
    method: 'POST',
    body: JSON.stringify(provider),
  });
}

export async function updateProvider(id: number, provider: {
  name?: string;
  config?: string;
  console?: string;
  models_fetch_mode?: "v1_models" | "api_pricing";
}): Promise<Provider> {
  return apiRequest<Provider>(`/providers/${id}`, {
    method: 'PUT',
    body: JSON.stringify(provider),
  });
}

export async function deleteProvider(id: number): Promise<void> {
  await apiRequest<void>(`/providers/${id}`, {
    method: 'DELETE',
  });
}

// Model API functions
export type ModelQuery = {
  page?: number;
  page_size?: number;
  search?: string;
  strategy?: string;
  io_log?: 'true' | 'false';
  capability?: string;
};

export async function getModels(params: ModelQuery = {}): Promise<PaginatedResponse<Model>> {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.append('page', params.page.toString());
  if (params.page_size) searchParams.append('page_size', params.page_size.toString());
  if (params.search) searchParams.append('search', params.search);
  if (params.strategy) searchParams.append('strategy', params.strategy);
  if (params.io_log) searchParams.append('io_log', params.io_log);
  if (params.capability) searchParams.append('capability', params.capability);
  const query = searchParams.toString();
  return apiRequest<PaginatedResponse<Model>>(query ? `/models?${query}` : '/models');
}

export async function getModelOptions(): Promise<Model[]> {
  return apiRequest<Model[]>('/models/select');
}

export async function createModel(model: {
  name: string;
  remark: string;
  max_retry: number;
  time_out: number;
  io_log: boolean;
  strategy: string;
  breaker: boolean;
  capabilities: string[];
  input_price?: number | null;
  output_price?: number | null;
}): Promise<Model> {
  return apiRequest<Model>('/models', {
    method: 'POST',
    body: JSON.stringify(model),
  });
}

export async function updateModel(id: number, model: {
  name?: string;
  remark?: string;
  max_retry?: number;
  time_out?: number;
  io_log?: boolean;
  strategy?: string;
  breaker?: boolean;
  capabilities?: string[];
  input_price?: number | null;
  output_price?: number | null;
}): Promise<Model> {
  return apiRequest<Model>(`/models/${id}`, {
    method: 'PUT',
    body: JSON.stringify(model),
  });
}

export async function updateModelStatus(id: number, status: boolean): Promise<Model> {
  return apiRequest<Model>(`/models/${id}/status`, {
    method: 'PATCH',
    body: JSON.stringify({ status }),
  });
}

export async function deleteModel(id: number): Promise<void> {
  await apiRequest<void>(`/models/${id}`, {
    method: 'DELETE',
  });
}

// Auth key API
export type AuthKeyPayload = {
  name: string;
  key?: string;
  status: boolean;
  allow_all: boolean;
  models: string[];
  expires_at?: string | null;
  rpm_limit?: number;
};

export async function getAuthKeys(params: {
  id?: number;
  page?: number;
  page_size?: number;
  status?: "active" | "inactive";
  allow_all?: "true" | "false";
  search?: string;
} = {}): Promise<PaginatedResponse<AuthKey>> {
  const searchParams = new URLSearchParams();

  if (params.id != null) searchParams.append("id", params.id.toString());
  if (params.page) searchParams.append("page", params.page.toString());
  if (params.page_size) searchParams.append("page_size", params.page_size.toString());
  if (params.status) searchParams.append("status", params.status);
  if (params.allow_all) searchParams.append("allow_all", params.allow_all);
  if (params.search) searchParams.append("search", params.search);

  const queryString = searchParams.toString();
  const res = await apiRequest<PaginatedResponse<Record<string, unknown>>>(queryString ? `/auth-keys?${queryString}` : "/auth-keys");
  return {
    ...res,
    data: (res.data ?? []).map(normalizeAuthKey),
  };
}

export async function getAuthKeyById(id: number): Promise<AuthKey | null> {
  if (!Number.isFinite(id) || id <= 0) return null;
  const res = await getAuthKeys({ id, page: 1, page_size: 1 });
  const exact = (res.data ?? []).find((item) => item.ID === id);
  return exact ?? null;
}

export interface AuthKeyItem {
  id: number;
  name: string;
}

export async function getAuthKeysList(): Promise<AuthKeyItem[]> {
  return apiRequest<AuthKeyItem[]>("/auth-keys/list");
}

export async function createAuthKey(payload: AuthKeyPayload): Promise<AuthKey> {
  const res = await apiRequest<Record<string, unknown>>("/auth-keys", {
    method: "POST",
    body: JSON.stringify(payload),
  });
  return normalizeAuthKey(res);
}

export async function updateAuthKey(id: number, payload: AuthKeyPayload): Promise<AuthKey> {
  const res = await apiRequest<Record<string, unknown>>(`/auth-keys/${id}`, {
    method: "PUT",
    body: JSON.stringify(payload),
  });
  return normalizeAuthKey(res);
}

export async function deleteAuthKey(id: number): Promise<void> {
  await apiRequest<void>(`/auth-keys/${id}`, {
    method: "DELETE",
  });
}

export async function toggleAuthKeyStatus(id: number): Promise<AuthKey> {
  const res = await apiRequest<Record<string, unknown>>(`/auth-keys/${id}/status`, {
    method: "PATCH",
  });
  return normalizeAuthKey(res);
}

// Model-Provider API functions
export async function getModelProviders(modelId: number): Promise<ModelWithProvider[]> {
  const res = await apiRequest<Record<string, unknown>[]>(`/model-providers?model_id=${modelId}`);
  return (res ?? []).map(normalizeModelWithProvider);
}

export async function testModelChat(
  modelProviderId: number,
  prompt: string,
  endpoint: string,
  extra?: {
    imageUrl?: string;
    maskUrl?: string;
    size?: string;
  }
): Promise<{ content: string }> {
  const payload: Record<string, unknown> = { prompt, endpoint };
  if (extra?.imageUrl) payload.image_url = extra.imageUrl;
  if (extra?.maskUrl) payload.mask_url = extra.maskUrl;
  if (extra?.size) payload.size = extra.size;
  return apiRequest<{ content: string }>(`/test/chat/${modelProviderId}`, {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function testModelChatWithFiles(
  modelProviderId: number,
  formData: FormData
): Promise<{ content: string }> {
  return apiRequestFormData<{ content: string }>(`/test/chat/${modelProviderId}`, formData);
}

export async function getModelProviderStatus(providerId: number, modelName: string, providerModel: string): Promise<boolean[]> {
  const params = new URLSearchParams({
    provider_id: providerId.toString(),
    model_name: modelName,
    provider_model: providerModel
  });
  return apiRequest<boolean[]>(`/model-providers/status?${params.toString()}`);
}

export async function createModelProvider(association: {
  model_id: number;
  provider_name: string;
  provider_id: number;
  with_header: boolean;
  customer_headers: Record<string, string>;
  weight: number;
}): Promise<ModelWithProvider> {
  const res = await apiRequest<Record<string, unknown>>('/model-providers', {
    method: 'POST',
    body: JSON.stringify(association),
  });
  return normalizeModelWithProvider(res);
}

export async function updateModelProvider(id: number, association: {
  model_id?: number;
  provider_name?: string;
  provider_id?: number;
  with_header?: boolean;
  customer_headers?: Record<string, string>;
  weight?: number;
}): Promise<ModelWithProvider> {
  const res = await apiRequest<Record<string, unknown>>(`/model-providers/${id}`, {
    method: 'PUT',
    body: JSON.stringify(association),
  });
  return normalizeModelWithProvider(res);
}

export async function updateModelProviderStatus(id: number, status: boolean): Promise<ModelWithProvider> {
  const res = await apiRequest<Record<string, unknown>>(`/model-providers/${id}/status`, {
    method: 'PATCH',
    body: JSON.stringify({ status }),
  });
  return normalizeModelWithProvider(res);
}

export async function deleteModelProvider(id: number): Promise<void> {
  await apiRequest<void>(`/model-providers/${id}`, {
    method: 'DELETE',
  });
}

// System API functions
export async function getSystemStatus(): Promise<SystemStatus> {
  return apiRequest<SystemStatus>('/status');
}

// Metrics API functions
export interface MetricsData {
  reqs: number;
  tokens: number;
}

export interface MetricsSummary {
  totalReqs: number;
  successRate: number;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  todayTokens: number;
  totalAmount: number;
  todayAmount: number;
  todayReqs: number;
  todaySuccessRate: number;
  todaySuccessReqs: number;
  todayFailureReqs: number;
  totalSuccessReqs: number;
  totalFailureReqs: number;
}

export interface ModelCount {
  model: string;
  calls: number;
}

export interface ProjectCount {
  project: string;
  calls: number;
}

export async function getMetrics(days: number): Promise<MetricsData> {
  return apiRequest<MetricsData>(`/metrics/use/${days}`);
}

export async function getMetricsSummary(): Promise<MetricsSummary> {
  return apiRequest<MetricsSummary>('/metrics/summary');
}

export async function getModelCounts(): Promise<ModelCount[]> {
  return apiRequest<ModelCount[]>('/metrics/counts');
}

export async function getProjectCounts(): Promise<ProjectCount[]> {
  return apiRequest<ProjectCount[]>('/metrics/projects');
}

// Test API functions
export type ProviderConnectivityTestResult = {
  error?: string;
  message?: string;
} | null;

export async function testModelProvider(id: number): Promise<ProviderConnectivityTestResult> {
  const result = await apiRequest<unknown>(`/test/${id}`);
  const record = asRecord(result);
  if (typeof record.error === "string" || typeof record.message === "string") {
    return {
      error: typeof record.error === "string" ? record.error : undefined,
      message: typeof record.message === "string" ? record.message : undefined,
    };
  }
  return null;
}

// Provider Templates API functions
export interface ProviderTemplate {
  display_name?: string;
  category?: "apikey" | "auth" | string;
  auth_mode?: boolean;
  hide_config?: boolean;
  template: string;
}

export async function getProviderTemplates(): Promise<ProviderTemplate[]> {
  return apiRequest<ProviderTemplate[]>('/providers/template');
}

// Provider Models API functions
export interface ProviderModel {
  id: string;
  object: string;
  created: number;
  owned_by: string;
}

const PROVIDER_MODELS_CACHE_TTL_MS = 6 * 60 * 60 * 1000;
const providerModelsCache = new Map<number, { expiresAt: number; data: ProviderModel[] }>();

const cloneProviderModels = (models: ProviderModel[]): ProviderModel[] => (
  models.map((item) => ({ ...item }))
);

export function clearProviderModelsCache(providerId?: number): void {
  if (typeof providerId === "number") {
    providerModelsCache.delete(providerId);
    return;
  }
  providerModelsCache.clear();
}

export async function getProviderModels(
  providerId: number,
  options?: { forceRefresh?: boolean }
): Promise<ProviderModel[]> {
  if (!options?.forceRefresh) {
    const cached = providerModelsCache.get(providerId);
    if (cached) {
      if (cached.expiresAt > Date.now()) {
        return cloneProviderModels(cached.data);
      }
      providerModelsCache.delete(providerId);
    }
  }

  const data = await apiRequest<ProviderModel[]>(`/providers/models/${providerId}`);
  providerModelsCache.set(providerId, {
    expiresAt: Date.now() + PROVIDER_MODELS_CACHE_TTL_MS,
    data: cloneProviderModels(data),
  });
  return data;
}

// Config API functions
export interface ConfigResponse {
  key: string;
  value: string;
}

export interface AnthropicCountTokens {
  base_url: string;
  api_key: string;
  version: string;
}

export interface AnthropicProxyIPConfig {
  enabled: boolean;
  proxy_ip: string;
}

export interface TelegramBreakerAlertConfig {
  enabled: boolean;
  bot_token: string;
  chat_id: string;
  api_base: string;
  proxy_url: string;
}

export interface ModelPriceSyncConfig {
  enabled: boolean;
  interval_minutes: number;
  source_url: string;
}

export interface SystemLogCleanupConfig {
  enabled: boolean;
  interval_minutes: number;
}

export const configAPI = {
  getConfig: (key: string) =>
    apiRequest<ConfigResponse>(`/config/${key}`),

  updateConfig: (key: string, data: unknown) =>
    apiRequest<ConfigResponse>(`/config/${key}`, {
      method: 'PUT',
      body: JSON.stringify({ value: JSON.stringify(data) }),
    }),

  runModelPriceSync: () =>
    apiRequest<{ status: string }>(`/config/model-price-sync/run`, {
      method: 'POST',
    }),

  runTelegramBreakerAlertTest: () =>
    apiRequest<{ status: string }>(`/config/breaker-alert-tg/test`, {
      method: 'POST',
    }),
};

// Logs API functions
export interface ChatLog {
  ID: number;
  CreatedAt: string;
  Name: string;
  ProviderModel: string;
  ProviderName: string;
  Status: string;
  Style: string;
  UserAgent: string;
  RemoteIP?: string;
  Error: string;
  Retry: number;
  // 后端字段为毫秒（chat_logs.proxy_time_ms/first_chunk_time_ms/chunk_time_ms）
  ProxyTimeMs: number;
  FirstChunkTimeMs: number;
  ChunkTimeMs: number;
  Tps: number;
  ChatIO: boolean;
  Size: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cached_tokens: number;
  total_cost: number;
  // 后端存的是 JSON 字符串（或空字符串）；前端使用时需要做解析/兜底
  prompt_tokens_details: PromptTokensDetails | string | null;
  key_name: string;
}

export interface PromptTokensDetails {
  cached_tokens: number;
}

export interface ChatIO {
  ID: number;
  CreatedAt: string;
  UpdatedAt: string;
  DeletedAt?: unknown;
  LogId: number;
  Input: string;
  OfString?: string | null;
  OfStringArray?: string[] | null;
  summary?: boolean;
  input_bytes?: number;
  output_bytes?: number;
  output_items?: number;
  truncated_input?: boolean;
  truncated_output?: boolean;
  truncated_output_items?: boolean;
}

export interface RequestStreamEventData {
  id: number;
  created_at: string;
  auth_key_id: number;
  auth_key_name: string;
  provider_name: string;
  model_name: string;
  status: string;
  latency_ms: number;
  proxy_time_ms: number;
  first_chunk_time_ms: number;
  chunk_time_ms: number;
  stream_like: boolean;
}

type RequestStreamHandlers = {
  onRequest?: (event: RequestStreamEventData) => void;
  onHeartbeat?: (payload: unknown) => void;
  onHello?: (payload: unknown) => void;
  onErrorEvent?: (payload: unknown) => void;
};

type RawChatIO = ChatIO & {
  OutputString?: string | null;
  OutputStringArray?: unknown;
};

export interface LogsResponse {
  data: ChatLog[];
  total: number;
  page: number;
  page_size: number;
  pages: number;
}

export interface SystemLogSnapshot {
  path: string;
  exists: boolean;
  size: number;
  updated_at?: string;
  content: string;
  lines: number;
  process?: {
    memory_bytes: number;
    cpu_percent: number;
  };
}

export interface RequestAmountPoint {
  hour: number;
  requests: number;
  amount: number;
}

export interface RequestAmountSummary {
  total_requests: number;
  total_amount: number;
  range: string;
  points: RequestAmountPoint[];
}

export interface ModelUsageSummaryItem {
  model: string;
  total_tokens: number;
  total_cost: number;
}

export interface DailyModelCostSeries {
  model: string;
  amounts: number[];
  total: number;
}

export interface DailyModelCostSummary {
  range: string;
  dates: string[];
  labels: string[];
  totals: number[];
  series: DailyModelCostSeries[];
}

export async function getUserAgents(): Promise<string[]> {
  return apiRequest<string[]>('/user-agents');
}

export async function getLogs(
  page: number = 1,
  pageSize: number = 10,
  filters: {
    name?: string;
    providerModel?: string;
    providerName?: string;
    status?: string;
    style?: string;
    authKeyId?: string;
    startAt?: string;
    endAt?: string;
  } = {}
): Promise<LogsResponse> {
  const params = new URLSearchParams();
  params.append("page", page.toString());
  params.append("page_size", pageSize.toString());

  if (filters.name) params.append("name", filters.name);
  if (filters.providerModel) params.append("provider_model", filters.providerModel);
  if (filters.providerName) params.append("provider_name", filters.providerName);
  if (filters.status) params.append("status", filters.status);
  if (filters.style) params.append("style", filters.style);
  if (filters.authKeyId) params.append("auth_key_id", filters.authKeyId);
  if (filters.startAt) params.append("start_at", filters.startAt);
  if (filters.endAt) params.append("end_at", filters.endAt);

  const query = `/logs?${params.toString()}`;
  const mode = getStoredAuthTokenMode();
  if (mode === "auth_key") {
    return authKeyRequest<LogsResponse>(`/auth-key${query}`);
  }
  return apiRequest<LogsResponse>(query);
}

export async function getSystemLogs(limit: number = 200): Promise<SystemLogSnapshot> {
  const params = new URLSearchParams();
  params.append("limit", String(limit));
  return apiRequest<SystemLogSnapshot>(`/system-logs?${params.toString()}`);
}

export async function clearSystemLogs(): Promise<{ path: string }> {
  return apiRequest<{ path: string }>('/system-logs/clear', {
    method: 'POST',
  });
}

export async function getRequestAmountTrend(): Promise<RequestAmountSummary> {
  return apiRequest<RequestAmountSummary>('/metrics/request-amount');
}

export async function getModelUsageSummary(): Promise<ModelUsageSummaryItem[]> {
  return apiRequest<ModelUsageSummaryItem[]>('/metrics/model-usage');
}

export async function getDailyModelCostTrend(days: number = 7, top: number = 5): Promise<DailyModelCostSummary> {
  const params = new URLSearchParams();
  params.append("days", days.toString());
  params.append("top", top.toString());
  return apiRequest<DailyModelCostSummary>(`/metrics/daily-model-cost?${params.toString()}`);
}

export async function getChatIO(
  logId: number | string,
  options: {
    mode?: "summary" | "full";
    inputLimit?: number;
    outputLimit?: number;
    outputItemsLimit?: number;
  } = {}
): Promise<ChatIO> {
  const params = new URLSearchParams();
  const requestMode = options.mode ?? "summary";
  if (requestMode) params.append("mode", requestMode);
  if (options.inputLimit) params.append("input_limit", options.inputLimit.toString());
  if (options.outputLimit) params.append("output_limit", options.outputLimit.toString());
  if (options.outputItemsLimit) params.append("output_items_limit", options.outputItemsLimit.toString());

  const query = params.toString();
  const path = query ? `/logs/${logId}/chat-io?${query}` : `/logs/${logId}/chat-io`;
  const tokenMode = getStoredAuthTokenMode();
  const raw = tokenMode === "auth_key"
    ? await authKeyRequest<RawChatIO>(`/auth-key${path}`)
    : await apiRequest<RawChatIO>(path);
  return normalizeChatIO(raw);
}

const parseSSEPayload = (raw: string): unknown => {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  try {
    return JSON.parse(trimmed) as unknown;
  } catch {
    return trimmed;
  }
};

const dispatchSSEBlock = (block: string, handlers: RequestStreamHandlers): void => {
  const normalizedBlock = block.replace(/\r/g, "");
  const lines = normalizedBlock.split("\n");
  let eventName = "message";
  const dataLines: string[] = [];

  for (const line of lines) {
    if (!line || line.startsWith(":")) continue;
    if (line.startsWith("event:")) {
      eventName = line.slice(6).trim() || "message";
      continue;
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trimStart());
    }
  }

  if (dataLines.length === 0) return;
  const payload = parseSSEPayload(dataLines.join("\n"));

  if (eventName === "request") {
    handlers.onRequest?.(payload as RequestStreamEventData);
    return;
  }
  if (eventName === "heartbeat") {
    handlers.onHeartbeat?.(payload);
    return;
  }
  if (eventName === "hello") {
    handlers.onHello?.(payload);
    return;
  }
  if (eventName === "error") {
    handlers.onErrorEvent?.(payload);
  }
};

export async function streamRequestEvents(
  options: {
    signal: AbortSignal;
    afterId?: number;
    pollMs?: number;
    batch?: number;
    heartbeatSec?: number;
  } & RequestStreamHandlers
): Promise<void> {
  const params = new URLSearchParams();
  if (typeof options.afterId === "number" && Number.isFinite(options.afterId) && options.afterId > 0) {
    params.append("after_id", Math.floor(options.afterId).toString());
  }
  if (typeof options.pollMs === "number" && Number.isFinite(options.pollMs) && options.pollMs > 0) {
    params.append("poll_ms", Math.floor(options.pollMs).toString());
  }
  if (typeof options.batch === "number" && Number.isFinite(options.batch) && options.batch > 0) {
    params.append("batch", Math.floor(options.batch).toString());
  }
  if (typeof options.heartbeatSec === "number" && Number.isFinite(options.heartbeatSec) && options.heartbeatSec > 0) {
    params.append("heartbeat_sec", Math.floor(options.heartbeatSec).toString());
  }

  const token = getStoredAuthToken();
  const query = params.toString();
  const endpoint = query ? `${API_BASE}/stream/requests?${query}` : `${API_BASE}/stream/requests`;

  const response = await fetch(endpoint, {
    method: "GET",
    headers: {
      Accept: "text/event-stream",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    signal: options.signal,
  });

  if (response.status === 401) {
    window.location.href = "/login";
    throw new Error("Unauthorized");
  }
  if (!response.ok) {
    throw new Error(`API request failed: ${response.status} ${response.statusText}`);
  }
  if (!response.body) {
    throw new Error("SSE stream is unavailable");
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });

      let boundaryIndex = buffer.indexOf("\n\n");
      while (boundaryIndex >= 0) {
        const block = buffer.slice(0, boundaryIndex);
        buffer = buffer.slice(boundaryIndex + 2);
        if (block.trim().length > 0) {
          dispatchSSEBlock(block, options);
        }
        boundaryIndex = buffer.indexOf("\n\n");
      }
    }

    const remain = decoder.decode();
    if (remain) {
      buffer += remain;
    }
    if (buffer.trim().length > 0) {
      dispatchSSEBlock(buffer, options);
    }
  } finally {
    reader.releaseLock();
  }
}

const parseChatIOStringArray = (value: unknown): string[] => {
  if (!value) return [];
  if (Array.isArray(value)) return value.filter((item): item is string => typeof item === "string");
  if (typeof value !== "string") return [];

  const trimmed = value.trim();
  if (!trimmed) return [];
  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (Array.isArray(parsed)) {
      return parsed.filter((item): item is string => typeof item === "string");
    }
    if (typeof parsed === "string" && parsed.trim() !== "") {
      return [parsed];
    }
    return [];
  } catch {
    return [trimmed];
  }
};

const normalizeChatIO = (raw: RawChatIO): ChatIO => {
  const ofString = typeof raw.OfString === "string"
    ? raw.OfString
    : (typeof raw.OutputString === "string" ? raw.OutputString : "");

  const ofStringArray = parseChatIOStringArray(
    raw.OfStringArray ?? raw.OutputStringArray
  );

  return {
    ...raw,
    OfString: ofString || null,
    OfStringArray: ofStringArray.length > 0 ? ofStringArray : null,
  };
};

// Clean logs API
export interface CleanLogsResult {
  deleted_count: number;
}

export async function cleanLogs(params: {
  type: 'count' | 'days';
  value: number;
}): Promise<CleanLogsResult> {
  return apiRequest<CleanLogsResult>('/logs/cleanup', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

// Test API functions
export async function testCountTokens(): Promise<void> {
  return apiRequest<void>('/test/count_tokens');
}

// 健康检查 API（不需要认证，直接访问根路径）
export interface ComponentStatus {
  status: "healthy" | "degraded" | "unhealthy";
  message?: string;
  responseTimeMs?: number;
}

// 请求块状态（每个块代表一次请求）
export interface ModelHealthRequestBlock {
  success: boolean; // 请求是否成功
  timestamp: string; // 请求时间（ISO 8601）
}

// 模型健康状态
export interface ModelHealth {
  modelName: string;
  providerModel: string;
  status: "healthy" | "degraded" | "unhealthy" | "unknown";
  totalRequests: number;
  failedRequests: number;
  successRate: number; // 0-100 之间
  avgResponseTimeMs: number;
  lastCheck: string;
  autoDisabledUntil?: string;
  requestBlocks: ModelHealthRequestBlock[]; // 最近100次请求，从旧到新
}

export interface ProviderHealth {
  id: number;
  name: string;
  status: "healthy" | "degraded" | "unhealthy" | "unknown";
  lastCheck: string;
  responseTimeMs: number;
  errorRate: number;
  totalRequests: number;
  failedRequests: number;
  models: ModelHealth[]; // 该提供商下的模型列表
}

export interface SystemHealth {
  status: "healthy" | "degraded" | "unhealthy";
  timestamp: string;
  uptime: number; // 总运行时间（秒），基于首次部署时间
  processUptime: number; // 当前进程运行时间（秒）
  firstDeployTime: string; // 首次部署时间（ISO 8601）
  components: {
    database: ComponentStatus;
    providers: {
      status: "healthy" | "degraded" | "unhealthy";
      total: number;
      healthy: number;
      degraded: number;
      unhealthy: number;
      details: ProviderHealth[];
    };
  };
}

export async function getSystemHealthDetail(timeWindowMinutes?: number): Promise<SystemHealth> {
  const params = timeWindowMinutes ? `?window=${timeWindowMinutes}` : "";
  const response = await fetch(`/api/health/detail${params}`);
  if (!response.ok) {
    throw new Error(`健康检查失败: ${response.status}`);
  }
  return response.json();
}

export async function getPrometheusMetrics(): Promise<string> {
  const response = await fetch("/api/metrics");
  if (!response.ok) {
    throw new Error(`获取指标失败: ${response.status}`);
  }
  return response.text();
}

export interface VersionReleaseInfo {
  tagName: string;
  name: string;
  publishedAt: string;
  htmlUrl: string;
  body: string;
}

export interface VersionUpdateCheck {
  currentVersion: string;
  latestVersion?: string;
  hasUpdate: boolean;
  release?: VersionReleaseInfo;
  backendFetchSuccess?: boolean;
  suggestBrowserFetch?: boolean;
  fetchSource?: "backend" | "browser";
}

const GITHUB_TAGS_API_URL = "https://api.github.com/repos/raciott/llmio/tags?per_page=1";
const GITHUB_COMMIT_API_URL_PATTERN = "https://api.github.com/repos/raciott/llmio/commits/%s";
const GITHUB_TAGS_PAGE_URL = "https://github.com/raciott/llmio/tags";

interface GithubTagItemResp {
  name?: string;
  commit?: {
    sha?: string;
  };
}

interface GithubCommitResp {
  html_url?: string;
  commit?: {
    message?: string;
  };
}

interface NormalizedVersion {
  major: number;
  minor: number;
  patch: number;
  pre: string;
  valid: boolean;
}

export async function checkVersionUpdate(): Promise<VersionUpdateCheck> {
  const info = await apiRequest<VersionUpdateCheck>("/version/update-check");
  if (!info.suggestBrowserFetch) {
    return info;
  }

  try {
    return await checkVersionUpdateViaBrowser(info.currentVersion);
  } catch (error) {
    console.warn("浏览器直连 GitHub 版本检查失败，回退后端结果:", error);
    return info;
  }
}

async function checkVersionUpdateViaBrowser(currentVersion: string): Promise<VersionUpdateCheck> {
  const latest = await fetchLatestTagFromGitHubByBrowser();
  if (!latest) {
    return {
      currentVersion: currentVersion?.trim() || "dev",
      hasUpdate: false,
      backendFetchSuccess: false,
      suggestBrowserFetch: false,
      fetchSource: "browser",
    };
  }

  const tagName = latest.tagName.trim();
  return {
    currentVersion: currentVersion?.trim() || "dev",
    latestVersion: tagName,
    hasUpdate: isLatestVersionGreater(tagName, currentVersion),
    release: latest,
    backendFetchSuccess: false,
    suggestBrowserFetch: false,
    fetchSource: "browser",
  };
}

async function fetchLatestTagFromGitHubByBrowser(): Promise<VersionReleaseInfo | null> {
  const res = await fetch(GITHUB_TAGS_API_URL, {
    headers: {
      Accept: "application/vnd.github+json",
    },
    cache: "no-store",
  });
  if (!res.ok) {
    throw new Error(`github status=${res.status}`);
  }

  const payload = (await res.json()) as GithubTagItemResp[];
  if (!Array.isArray(payload) || payload.length === 0) {
    return null;
  }

  const tagName = (payload[0]?.name || "").trim();
  const commitSHA = (payload[0]?.commit?.sha || "").trim();
  if (!tagName) {
    return null;
  }

  let commitMessage = "";
  let commitHTMLURL = "";
  if (commitSHA) {
    const endpoint = GITHUB_COMMIT_API_URL_PATTERN.replace("%s", encodeURIComponent(commitSHA));
    try {
      const commitRes = await fetch(endpoint, {
        headers: {
          Accept: "application/vnd.github+json",
        },
        cache: "no-store",
      });
      if (commitRes.ok) {
        const commitPayload = (await commitRes.json()) as GithubCommitResp;
        commitMessage = (commitPayload.commit?.message || "").trim();
        commitHTMLURL = (commitPayload.html_url || "").trim();
      }
    } catch {
      // commit 详情失败时不影响主流程，继续使用 tag 信息。
    }
  }

  return {
    tagName,
    name: `最新标签 ${tagName}`,
    publishedAt: "",
    htmlUrl: commitHTMLURL || buildTagDetailURL(tagName),
    body: buildTagUpdateBody(tagName, commitSHA, commitMessage),
  };
}

function buildTagDetailURL(tagName: string): string {
  const trimmed = tagName.trim();
  if (!trimmed) {
    return GITHUB_TAGS_PAGE_URL;
  }
  return `https://github.com/raciott/llmio/tree/${encodeURIComponent(trimmed)}`;
}

function buildTagUpdateBody(tagName: string, commitSHA: string, commitMessage: string): string {
  const safeTag = tagName.trim();
  const safeSHA = commitSHA.trim();
  const safeMsg = commitMessage.trim();
  if (safeMsg) {
    return safeMsg;
  }
  if (!safeTag) {
    return "检测到新标签（当前仓库未发布 Release，版本检查基于 Tags）。";
  }
  if (safeSHA) {
    const shortSHA = safeSHA.length > 12 ? safeSHA.slice(0, 12) : safeSHA;
    return `检测到新标签：${safeTag}\n提交：${shortSHA}\n（当前仓库未发布 Release，版本检查基于 Tags）`;
  }
  return `检测到新标签：${safeTag}\n（当前仓库未发布 Release，版本检查基于 Tags）`;
}

function isLatestVersionGreater(latest: string, current: string): boolean {
  if (current.trim().toLowerCase() === "dev") {
    return latest.trim() !== "";
  }

  const latestVer = parseNormalizedVersion(latest);
  const currentVer = parseNormalizedVersion(current);

  if (!currentVer.valid) {
    return latestVer.valid;
  }
  if (!latestVer.valid) {
    return false;
  }

  if (latestVer.major !== currentVer.major) {
    return latestVer.major > currentVer.major;
  }
  if (latestVer.minor !== currentVer.minor) {
    return latestVer.minor > currentVer.minor;
  }
  if (latestVer.patch !== currentVer.patch) {
    return latestVer.patch > currentVer.patch;
  }

  const latestHasPre = latestVer.pre !== "";
  const currentHasPre = currentVer.pre !== "";
  if (latestHasPre !== currentHasPre) {
    return !latestHasPre;
  }
  if (latestHasPre && currentHasPre) {
    return latestVer.pre > currentVer.pre;
  }
  return false;
}

function parseNormalizedVersion(raw: string): NormalizedVersion {
  let value = (raw || "").trim();
  if (!value) {
    return invalidNormalizedVersion();
  }
  if (value.startsWith("v") || value.startsWith("V")) {
    value = value.slice(1);
  }
  if (!value) {
    return invalidNormalizedVersion();
  }

  const plusParts = value.split("+", 2);
  const mainPart = (plusParts[0] || "").trim();
  if (!mainPart) {
    return invalidNormalizedVersion();
  }

  const mainAndPre = mainPart.split("-", 2);
  const core = (mainAndPre[0] || "").trim();
  const pre = (mainAndPre[1] || "").trim().toLowerCase();
  if (!core) {
    return invalidNormalizedVersion();
  }

  const nums = core.split(".");
  if (nums.length < 2 || nums.length > 3) {
    return invalidNormalizedVersion();
  }
  if (nums.length === 2) {
    nums.push("0");
  }

  const major = Number.parseInt(nums[0], 10);
  const minor = Number.parseInt(nums[1], 10);
  const patch = Number.parseInt(nums[2], 10);
  if (Number.isNaN(major) || Number.isNaN(minor) || Number.isNaN(patch)) {
    return invalidNormalizedVersion();
  }
  if (major < 0 || minor < 0 || patch < 0) {
    return invalidNormalizedVersion();
  }

  return {
    major,
    minor,
    patch,
    pre,
    valid: true,
  };
}

function invalidNormalizedVersion(): NormalizedVersion {
  return {
    major: 0,
    minor: 0,
    patch: 0,
    pre: "",
    valid: false,
  };
}
