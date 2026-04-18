type JsonRecord = Record<string, unknown>;

const shellQuote = (value: string): string => `'${value.replace(/'/g, `'\"'\"'`)}'`;

const parseJsonRecord = (raw: string): JsonRecord | null => {
  if (!raw || !raw.trim()) return null;
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as JsonRecord;
    }
    return null;
  } catch {
    return null;
  }
};

const hasNonEmpty = (value: unknown): boolean => {
  if (value == null) return false;
  if (typeof value === "string") return value.trim().length > 0;
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === "object") return Object.keys(value as JsonRecord).length > 0;
  return true;
};

const inferOpenAIEndpointByBody = (body: JsonRecord | null): string => {
  if (!body) return "/v1/chat/completions";
  if (hasNonEmpty(body.documents) && hasNonEmpty(body.query)) return "/v1/rerank";
  if (hasNonEmpty(body.image) || hasNonEmpty(body.mask)) return "/v1/images/edits";
  if (hasNonEmpty(body.video) || hasNonEmpty(body.duration) || hasNonEmpty(body.seconds) || hasNonEmpty(body.fps)) {
    return "/v1/videos";
  }
  if (hasNonEmpty(body.prompt) && !hasNonEmpty(body.messages)) {
    return "/v1/images/generations";
  }
  return "/v1/chat/completions";
};

export const inferGatewayEndpoint = (style: string | null | undefined, requestBody: string): string => {
  const normalizedStyle = (style ?? "").trim().toLowerCase();
  switch (normalizedStyle) {
    case "anthropic":
      return "/v1/messages";
    case "codex":
      return "/v1/responses";
    case "openai-embeddings":
    case "gemini-embeddings":
      return "/v1/embeddings";
    case "openai":
      return inferOpenAIEndpointByBody(parseJsonRecord(requestBody));
    default:
      return "/v1/chat/completions";
  }
};

export const maskAuthToken = (token: string): string => {
  const normalized = (token ?? "").trim();
  if (!normalized) return "YOUR_AUTH_TOKEN";
  if (normalized.length <= 10) return `${normalized.slice(0, 2)}***${normalized.slice(-2)}`;
  return `${normalized.slice(0, 6)}***${normalized.slice(-4)}`;
};

export const buildReplayCurlSnippet = (params: {
  baseUrl: string;
  endpoint: string;
  authToken: string;
  requestBody: string;
}): string => {
  const base = params.baseUrl.replace(/\/+$/, "");
  const endpoint = params.endpoint.startsWith("/") ? params.endpoint : `/${params.endpoint}`;
  const url = `${base}${endpoint}`;
  const token = (params.authToken ?? "").trim() || "YOUR_AUTH_TOKEN";
  const body = params.requestBody?.trim() || "{}";

  return [
    "cat > body.json <<'JSON'",
    body,
    "JSON",
    "",
    `curl -X POST ${shellQuote(url)} \\`,
    `  -H ${shellQuote(`Authorization: Bearer ${token}`)} \\`,
    `  -H ${shellQuote("Content-Type: application/json")} \\`,
    "  --data @body.json",
  ].join("\n");
};

