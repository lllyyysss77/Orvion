import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import Loading from "@/components/loading";
import { getChatIO, type ChatIO } from "@/lib/api";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { duotoneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "sonner";
import { ArrowUpFromLine, Bot, Braces, Cog, Info, User, Wrench, Zap } from "lucide-react";

type SyntaxStyle = typeof duotoneLight;
type ViewMode = "parsed" | "raw";
type JsonRecord = Record<string, unknown>;

interface FormattedJson {
  text: string;
  parsed: boolean;
  empty: boolean;
  value: unknown;
}

interface RequestMetaItem {
  label: string;
  value: string;
}

interface RequestChunk {
  label: string;
  value: string;
}

type MessageRole = "system" | "developer" | "user" | "assistant" | "tool" | "other";

interface RequestFieldItem {
  key: string;
  value: unknown;
}

type RequestTimelineKind = "system" | "developer" | "user" | "function_call" | "function_result";

interface RequestTimelineItem {
  id: string;
  kind: RequestTimelineKind;
  title: string;
  role: "system" | "developer" | "user";
  chunks: RequestChunk[];
}

interface ParsedRequestPayload {
  meta: RequestMetaItem[];
  timeline: RequestTimelineItem[];
  extraFields: RequestFieldItem[];
}

interface ParsedOutputBlock {
  id: string;
  title: string;
  body: string;
  images?: ParsedOutputImage[];
  videos?: ParsedOutputVideo[];
}

interface ParsedOutputPayload {
  blocks: ParsedOutputBlock[];
  extraFields: RequestFieldItem[];
}

interface ParsedOutputImage {
  src: string;
  mime: string;
  source: "base64" | "url";
}

interface ParsedOutputVideo {
  src: string;
  mime: string;
  source: "url";
}

function formatJson(raw: string): FormattedJson {
  if (!raw || raw.trim().length === 0) {
    return { text: "(无内容)", parsed: false, empty: true, value: null };
  }

  try {
    const parsedJson = JSON.parse(raw);
    return {
      text: JSON.stringify(parsedJson, null, 2),
      parsed: true,
      empty: false,
      value: parsedJson,
    };
  } catch {
    return {
      text: raw,
      parsed: false,
      empty: false,
      value: null,
    };
  }
}

function isRecord(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function toReadableText(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (value === null) return "null";
  if (value === undefined) return "";
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function pickFirstText(record: JsonRecord, keys: string[]): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim().length > 0) return value;
    if (isRecord(value)) {
      const nested = value.text ?? value.value ?? value.url;
      if (typeof nested === "string" && nested.trim().length > 0) return nested;
    }
  }
  return "";
}

function normalizeRole(raw: unknown): MessageRole {
  const role = typeof raw === "string" ? raw.toLowerCase().trim() : "";
  if (role === "system") return "system";
  if (role === "developer") return "developer";
  if (role === "user") return "user";
  if (role === "assistant") return "assistant";
  if (role === "tool") return "tool";
  return "other";
}

function normalizeContentToChunks(content: unknown): RequestChunk[] {
  if (typeof content === "string") {
    return [{ label: "内容", value: content }];
  }

  if (typeof content === "number" || typeof content === "boolean") {
    return [{ label: "内容", value: String(content) }];
  }

  if (Array.isArray(content)) {
    const chunks: RequestChunk[] = [];
    content.forEach((item, index) => {
      if (typeof item === "string") {
        chunks.push({ label: `片段 ${index + 1}`, value: item });
        return;
      }
      if (isRecord(item)) {
        const itemType = typeof item.type === "string" && item.type.trim() ? item.type : `片段 ${index + 1}`;
        const textValue = pickFirstText(item, ["text", "input_text", "output_text", "reasoning"]);
        if (textValue) {
          chunks.push({ label: itemType, value: textValue });
          return;
        }
        if (itemType.includes("image")) {
          const imageURL = pickFirstText(item, ["image_url", "url", "image", "source"]);
          if (imageURL) {
            chunks.push({ label: itemType, value: imageURL });
            return;
          }
        }
        chunks.push({ label: itemType, value: toReadableText(item) });
        return;
      }
      chunks.push({ label: `片段 ${index + 1}`, value: toReadableText(item) });
    });
    return chunks.length > 0 ? chunks : [{ label: "内容", value: "(空数组)" }];
  }

  if (isRecord(content)) {
    const textValue = pickFirstText(content, ["text", "input_text", "output_text", "reasoning"]);
    if (textValue) {
      return [{ label: "内容", value: textValue }];
    }
    return [{ label: "内容", value: toReadableText(content) }];
  }

  return [{ label: "内容", value: "(空)" }];
}


function formatToolPayload(raw: unknown): string {
  if (typeof raw === "string") {
    const trimmed = raw.trim();
    if (!trimmed) return "{}";
    try {
      const parsed = JSON.parse(trimmed) as unknown;
      const compact = JSON.stringify(parsed);
      if (compact && compact.length <= 220) {
        return compact;
      }
      return JSON.stringify(parsed, null, 2);
    } catch {
      return trimmed;
    }
  }
  if (isRecord(raw) || Array.isArray(raw)) {
    const compact = JSON.stringify(raw);
    if (compact && compact.length <= 220) {
      return compact;
    }
    return JSON.stringify(raw, null, 2);
  }
  if (raw === undefined) return "{}";
  return String(raw);
}

function timelineTitle(kind: RequestTimelineKind): string {
  switch (kind) {
    case "system":
      return "系统指令";
    case "developer":
      return "开发者指令";
    case "user":
      return "用户消息";
    case "function_call":
      return "函数调用";
    case "function_result":
      return "函数返回";
    default:
      return "用户消息";
  }
}

function appendTimelineMessage(
  timeline: RequestTimelineItem[],
  role: "system" | "developer" | "user",
  content: unknown,
  extraChunks?: RequestChunk[]
) {
  const chunks = normalizeContentToChunks(content);
  if (chunks.length === 0) return;
  const allChunks = extraChunks && extraChunks.length > 0 ? [...extraChunks, ...chunks] : chunks;
  timeline.push({
    id: `timeline-${timeline.length + 1}`,
    kind: role,
    title: timelineTitle(role),
    role,
    chunks: allChunks,
  });
}

function appendTimelineFunctionCall(timeline: RequestTimelineItem[], name: string, args: unknown, callId?: string) {
  const normalizedName = name.trim() || "unknown_function";
  const chunks: RequestChunk[] = [];
  if (callId?.trim()) {
    chunks.push({ label: "tool_call_id", value: callId.trim() });
  }
  chunks.push({ label: "调用", value: `${normalizedName}(${formatToolPayload(args)})` });
  timeline.push({
    id: `timeline-${timeline.length + 1}`,
    kind: "function_call",
    title: timelineTitle("function_call"),
    role: "user",
    chunks,
  });
}

function appendTimelineFunctionResult(timeline: RequestTimelineItem[], name: string, result: unknown, callId?: string) {
  const normalizedName = name.trim() || "unknown_function";
  const chunks: RequestChunk[] = [];
  chunks.push({ label: "函数", value: normalizedName });
  if (callId?.trim()) {
    chunks.push({ label: "tool_call_id", value: callId.trim() });
  }
  chunks.push({ label: "返回", value: formatToolPayload(result) });
  timeline.push({
    id: `timeline-${timeline.length + 1}`,
    kind: "function_result",
    title: timelineTitle("function_result"),
    role: "user",
    chunks,
  });
}

function parseTimelineArrayEntries(entries: unknown, timeline: RequestTimelineItem[]) {
  if (!Array.isArray(entries)) return;
  entries.forEach((entry) => {
    if (!isRecord(entry)) {
      appendTimelineMessage(timeline, "user", entry);
      return;
    }

    const role = normalizeRole(entry.role);
    const type = typeof entry.type === "string" ? entry.type.toLowerCase().trim() : "";

    if (Array.isArray(entry.tool_calls)) {
      entry.tool_calls.forEach((toolCall) => {
        if (!isRecord(toolCall)) return;
        const fn = isRecord(toolCall.function) ? toolCall.function : null;
        const name = typeof (fn?.name ?? toolCall.name) === "string" ? String(fn?.name ?? toolCall.name) : "";
        const args = fn?.arguments ?? toolCall.arguments ?? toolCall.input ?? {};
        const callId = typeof toolCall.id === "string" ? toolCall.id : undefined;
        appendTimelineFunctionCall(timeline, name, args, callId);
      });
    }

    if (isRecord(entry.function_call)) {
      const fn = entry.function_call;
      const name = typeof fn.name === "string" ? fn.name : "";
      const args = fn.arguments ?? fn.input ?? {};
      const callId = typeof fn.id === "string" ? fn.id : (typeof entry.tool_call_id === "string" ? entry.tool_call_id : undefined);
      appendTimelineFunctionCall(timeline, name, args, callId);
    }

    if (type === "function_call" || type === "tool_use" || type === "custom_tool_call") {
      const name = typeof entry.name === "string" ? entry.name : (typeof entry.tool_name === "string" ? entry.tool_name : "");
      const args = entry.arguments ?? entry.input ?? entry.params ?? {};
      const callId = typeof entry.call_id === "string" ? entry.call_id : (typeof entry.id === "string" ? entry.id : undefined);
      appendTimelineFunctionCall(timeline, name, args, callId);
      return;
    }

    if (type === "function_call_output" || type === "tool_result" || type === "tool_output") {
      const name = typeof entry.name === "string" ? entry.name : (typeof entry.tool_name === "string" ? entry.tool_name : "");
      const result = entry.output ?? entry.result ?? entry.content ?? entry.text ?? {};
      const callId = typeof entry.call_id === "string" ? entry.call_id : (typeof entry.tool_call_id === "string" ? entry.tool_call_id : undefined);
      appendTimelineFunctionResult(timeline, name, result, callId);
      return;
    }

    if (role === "tool") {
      const name = typeof entry.name === "string" ? entry.name : (typeof entry.tool_name === "string" ? entry.tool_name : "");
      const result = entry.content ?? entry.output ?? entry.result ?? entry.text ?? {};
      const callId = typeof entry.tool_call_id === "string" ? entry.tool_call_id : undefined;
      appendTimelineFunctionResult(timeline, name, result, callId);
      return;
    }

    if (role === "system" || role === "developer" || role === "user") {
      const extraChunks: RequestChunk[] = [];
      if (typeof entry.name === "string" && entry.name.trim()) {
        extraChunks.push({ label: "名称", value: entry.name.trim() });
      }
      const content = entry.content ?? entry.input ?? entry.parts ?? entry.text ?? entry;
      appendTimelineMessage(timeline, role, content, extraChunks);
      return;
    }

    if (entry.content !== undefined && Array.isArray(entry.content)) {
      parseTimelineArrayEntries(entry.content, timeline);
    }
  });
}

function buildParsedRequestPayload(value: unknown): ParsedRequestPayload | null {
  if (!isRecord(value)) return null;

  const record = value;
  const usedKeys = new Set<string>();
  const meta: RequestMetaItem[] = [];
  const timeline: RequestTimelineItem[] = [];

  const metaKeys: Array<[string, string]> = [
    ["model", "模型"],
    ["stream", "流式"],
    ["temperature", "温度"],
    ["top_p", "Top P"],
    ["max_tokens", "最大输出"],
    ["max_output_tokens", "最大输出"],
    ["tool_choice", "工具策略"],
    ["parallel_tool_calls", "并行工具"],
    ["response_format", "响应格式"],
  ];

  metaKeys.forEach(([key, label]) => {
    if (!(key in record)) return;
    const valueText = toReadableText(record[key]).trim();
    if (!valueText) return;
    meta.push({ label, value: valueText });
    usedKeys.add(key);
  });

  if (record.instructions !== undefined) {
    appendTimelineMessage(timeline, "system", record.instructions);
    usedKeys.add("instructions");
  }

  if (record.system !== undefined) {
    appendTimelineMessage(timeline, "system", record.system);
    usedKeys.add("system");
  }

  if (record.messages !== undefined) {
    parseTimelineArrayEntries(record.messages, timeline);
    usedKeys.add("messages");
  }

  if (record.input !== undefined) {
    if (Array.isArray(record.input)) {
      parseTimelineArrayEntries(record.input, timeline);
    } else {
      appendTimelineMessage(timeline, "user", record.input);
    }
    usedKeys.add("input");
  }

  if (record.tools !== undefined) {
    const toolsText = toReadableText(record.tools).trim();
    if (toolsText) {
      timeline.push({
        id: `timeline-${timeline.length + 1}`,
        kind: "developer",
        title: "开发者指令",
        role: "developer",
        chunks: [{ label: "工具配置", value: toolsText }],
      });
    }
    usedKeys.add("tools");
  }

  if (record.tool_calls !== undefined) {
    parseTimelineArrayEntries(record.tool_calls, timeline);
    usedKeys.add("tool_calls");
  }

  const extraFields: RequestFieldItem[] = Object.entries(record)
    .filter(([key]) => !usedKeys.has(key))
    .map(([key, fieldValue]) => ({ key, value: fieldValue }));

  return { meta, timeline, extraFields };
}

function roleToneClass(role: MessageRole): string {
  switch (role) {
    case "developer":
      return "border-violet-200 bg-violet-50/45";
    case "system":
      return "border-fuchsia-200 bg-fuchsia-50/45";
    case "user":
      return "border-sky-200 bg-sky-50/45";
    case "assistant":
      return "border-emerald-200 bg-emerald-50/45";
    case "tool":
      return "border-amber-200 bg-amber-50/45";
    default:
      return "border-muted bg-muted/35";
  }
}

function roleIcon(role: MessageRole) {
  switch (role) {
    case "developer":
      return <Cog className="h-4 w-4" />;
    case "system":
      return <Info className="h-4 w-4" />;
    case "user":
      return <User className="h-4 w-4" />;
    case "assistant":
      return <Bot className="h-4 w-4" />;
    case "tool":
      return <Wrench className="h-4 w-4" />;
    default:
      return <Braces className="h-4 w-4" />;
  }
}

function timelineToneClass(kind: RequestTimelineKind, role: "system" | "developer" | "user"): string {
  if (kind === "function_call") return "border-orange-200 bg-orange-50/50";
  if (kind === "function_result") return "border-teal-200 bg-teal-50/50";
  return roleToneClass(role);
}

function timelineHeaderClass(kind: RequestTimelineKind): string {
  if (kind === "function_call") return "text-orange-700";
  if (kind === "function_result") return "text-teal-700";
  return "";
}

function timelineIcon(kind: RequestTimelineKind, role: "system" | "developer" | "user") {
  if (kind === "function_call") return <Zap className="h-4 w-4" />;
  if (kind === "function_result") return <ArrowUpFromLine className="h-4 w-4" />;
  return roleIcon(role);
}

function wrapTechnicalTerms(text: string): string {
  if (!text.trim()) return text;
  const segments = text.split(/(`[^`]*`)/g);
  return segments
    .map((segment) => {
      if (segment.startsWith("`") && segment.endsWith("`")) {
        return segment;
      }
      let next = segment;
      next = next.replace(/(^|[\s(（])((?:\/[A-Za-z0-9._-]+){1,})(?=$|[\s),，。；;:：])/g, (_m, prefix: string, value: string) => `${prefix}\`${value}\``);
      next = next.replace(/\b[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+\b/g, (value: string) => `\`${value}\``);
      next = next.replace(/\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b/g, (value: string) => `\`${value}\``);
      return next;
    })
    .join("");
}

function extractContentTexts(content: unknown): string[] {
  if (typeof content === "string") {
    const trimmed = content.trim();
    return trimmed ? [trimmed] : [];
  }
  if (typeof content === "number" || typeof content === "boolean") {
    return [String(content)];
  }
  if (Array.isArray(content)) {
    const texts: string[] = [];
    content.forEach((item) => {
      if (typeof item === "string") {
        if (item.trim()) texts.push(item.trim());
        return;
      }
      if (isRecord(item)) {
        const primary = pickFirstText(item, ["text", "input_text", "output_text", "content", "description", "reasoning"]);
        if (primary.trim()) {
          texts.push(primary.trim());
          return;
        }
        if (item.content !== undefined) {
          texts.push(...extractContentTexts(item.content));
          return;
        }
        const fallback = toReadableText(item).trim();
        if (fallback) texts.push(fallback);
        return;
      }
      const fallback = toReadableText(item).trim();
      if (fallback) texts.push(fallback);
    });
    return texts;
  }
  if (isRecord(content)) {
    const primary = pickFirstText(content, ["text", "input_text", "output_text", "content", "description", "reasoning"]);
    if (primary.trim()) return [primary.trim()];
    if (content.content !== undefined) return extractContentTexts(content.content);
    const fallback = toReadableText(content).trim();
    return fallback ? [fallback] : [];
  }
  return [];
}

function buildSuggestionBlocks(suggestions: unknown): ParsedOutputBlock[] {
  if (!Array.isArray(suggestions)) return [];
  const blocks: ParsedOutputBlock[] = [];
  suggestions.forEach((item, index) => {
    if (!isRecord(item)) return;
    const title = typeof item.title === "string" && item.title.trim() ? item.title.trim() : `建议 ${index + 1}`;
    const description = typeof item.description === "string" ? item.description.trim() : "";
    const prompt = typeof item.prompt === "string" ? item.prompt.trim() : "";
    const appId = typeof item.appId === "string" ? item.appId.trim() : "";

    const lines: string[] = [];
    if (description) lines.push(wrapTechnicalTerms(description));
    if (prompt) {
      lines.push("");
      lines.push("任务提示：");
      lines.push(wrapTechnicalTerms(prompt));
    }
    if (appId) {
      lines.push("");
      lines.push(`应用：${wrapTechnicalTerms(appId)}`);
    }
    blocks.push({
      id: `suggestion-${index + 1}`,
      title,
      body: lines.join("\n"),
    });
  });
  return blocks;
}

function normalizeImageMimeType(raw: unknown): string {
  if (typeof raw !== "string") return "image/png";
  const value = raw.trim().toLowerCase();
  if (!value) return "image/png";
  if (value.startsWith("image/")) return value;
  return "image/png";
}

function normalizeVideoMimeType(raw: unknown): string {
  if (typeof raw !== "string") return "video/mp4";
  const value = raw.trim().toLowerCase();
  if (!value) return "video/mp4";
  if (value.startsWith("video/")) return value;
  return "video/mp4";
}

function isLikelyVideoUrl(raw: string): boolean {
  const value = raw.trim();
  if (!value) return false;
  if (/^data:video\//i.test(value)) return true;
  return /\.(mp4|m4v|webm|mov|mkv)(\?|#|$)/i.test(value);
}

function pickMediaUrl(record: JsonRecord, keys: string[]): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
    if (isRecord(value)) {
      const nested = pickFirstText(value, ["url", "video_url", "video", "src", "source"]);
      if (nested.trim()) return nested.trim();
    }
  }
  return "";
}

function buildVideoOutputBlocks(data: unknown): ParsedOutputBlock[] {
  if (!Array.isArray(data)) return [];
  const blocks: ParsedOutputBlock[] = [];

  data.forEach((entry, index) => {
    if (!isRecord(entry)) return;

    const mime = normalizeVideoMimeType(entry.type ?? entry.mime_type ?? entry.mime);
    const videoURL = pickMediaUrl(entry, ["url", "video_url", "video", "src", "source", "remixed_from_video_id"]);
    if (!isLikelyVideoUrl(videoURL)) return;

    const videos: ParsedOutputVideo[] = [
      {
        src: videoURL,
        mime,
        source: "url",
      },
    ];

    const bodyLines: string[] = [];
    bodyLines.push(`已识别视频输出 ${index + 1}`);
    bodyLines.push(`类型：${mime}`);
    bodyLines.push(`视频地址：${wrapTechnicalTerms(videoURL)}`);

    blocks.push({
      id: `video-${index + 1}`,
      title: `视频响应 ${index + 1}`,
      body: bodyLines.join("\n"),
      videos,
    });
  });

  return blocks;
}

function buildImageOutputBlocks(data: unknown): ParsedOutputBlock[] {
  if (!Array.isArray(data)) return [];
  const blocks: ParsedOutputBlock[] = [];

  data.forEach((entry, index) => {
    if (!isRecord(entry)) return;

    const mime = normalizeImageMimeType(entry.type);
    const b64 = typeof entry.b64_json === "string" ? entry.b64_json.trim() : "";
    const imageURL = typeof entry.url === "string" ? entry.url.trim() : "";
    const revisedURL = typeof entry.revised_url === "string" ? entry.revised_url.trim() : "";
    const sourceURL = imageURL || revisedURL;
    const images: ParsedOutputImage[] = [];

    if (b64) {
      images.push({
        src: `data:${mime};base64,${b64}`,
        mime,
        source: "base64",
      });
    }
    if (sourceURL) {
      images.push({
        src: sourceURL,
        mime,
        source: "url",
      });
    }
    if (images.length === 0) return;

    const bodyLines: string[] = [];
    bodyLines.push(`已识别图像输出 ${index + 1}`);
    bodyLines.push(`类型：${mime}`);
    if (b64) bodyLines.push(`Base64 长度：${b64.length}`);
    if (sourceURL) bodyLines.push(`图片地址：${wrapTechnicalTerms(sourceURL)}`);

    blocks.push({
      id: `image-${index + 1}`,
      title: `图像响应 ${index + 1}`,
      body: bodyLines.join("\n"),
      images,
    });
  });

  return blocks;
}

function buildParsedOutputPayload(value: unknown): ParsedOutputPayload | null {
  if (!isRecord(value)) return null;

  const record = value;
  const blocks: ParsedOutputBlock[] = [];
  const usedKeys = new Set<string>();

  const suggestionBlocks = buildSuggestionBlocks(record.suggestions);
  if (suggestionBlocks.length > 0) {
    blocks.push(...suggestionBlocks);
    usedKeys.add("suggestions");
  }

  const imageBlocks = buildImageOutputBlocks(record.data);
  if (imageBlocks.length > 0) {
    blocks.push(...imageBlocks);
    usedKeys.add("data");
  }

  const videoBlocks = buildVideoOutputBlocks(record.data);
  if (videoBlocks.length > 0) {
    blocks.push(...videoBlocks);
    usedKeys.add("data");
  }

  const directVideoUrl = pickMediaUrl(record, ["url", "video_url", "video", "src", "source", "remixed_from_video_id"]);
  if (isLikelyVideoUrl(directVideoUrl)) {
    blocks.push({
      id: "direct-video",
      title: "视频响应",
      body: `已识别视频输出\n类型：${normalizeVideoMimeType(record.type ?? record.mime_type ?? record.mime)}\n视频地址：${wrapTechnicalTerms(directVideoUrl)}`,
      videos: [
        {
          src: directVideoUrl,
          mime: normalizeVideoMimeType(record.type ?? record.mime_type ?? record.mime),
          source: "url",
        },
      ],
    });
    usedKeys.add("url");
    usedKeys.add("video_url");
    usedKeys.add("video");
    usedKeys.add("src");
    usedKeys.add("source");
    usedKeys.add("remixed_from_video_id");
  }

  const directTextKeys = ["output_text", "text", "content", "message"];
  directTextKeys.forEach((key) => {
    if (!(key in record)) return;
    const texts = extractContentTexts(record[key]);
    if (texts.length === 0) return;
    blocks.push({
      id: `direct-${key}`,
      title: "模型响应",
      body: wrapTechnicalTerms(texts.join("\n\n")),
    });
    usedKeys.add(key);
  });

  if (Array.isArray(record.choices)) {
    record.choices.forEach((choice, index) => {
      if (!isRecord(choice)) return;
      const message = choice.message;
      const texts = extractContentTexts(isRecord(message) ? message.content ?? message : message);
      if (texts.length === 0) return;
      blocks.push({
        id: `choice-${index + 1}`,
        title: `模型响应 ${index + 1}`,
        body: wrapTechnicalTerms(texts.join("\n\n")),
      });
    });
    usedKeys.add("choices");
  }

  if (Array.isArray(record.output)) {
    record.output.forEach((entry, index) => {
      if (!isRecord(entry)) return;
      const entryType = typeof entry.type === "string" && entry.type.trim() ? entry.type.trim() : `片段 ${index + 1}`;
      const source = entry.content ?? entry.output_text ?? entry.text ?? entry;
      const texts = extractContentTexts(source);
      if (texts.length === 0) return;
      blocks.push({
        id: `output-${index + 1}`,
        title: `模型响应 · ${entryType}`,
        body: wrapTechnicalTerms(texts.join("\n\n")),
      });
    });
    usedKeys.add("output");
  }

  if (Array.isArray(record.content)) {
    const texts = extractContentTexts(record.content);
    if (texts.length > 0) {
      blocks.push({
        id: "content-array",
        title: "模型响应",
        body: wrapTechnicalTerms(texts.join("\n\n")),
      });
    }
    usedKeys.add("content");
  }

  if (blocks.length === 0) {
    return null;
  }

  const extraFields: RequestFieldItem[] = Object.entries(record)
    .filter(([key]) => !usedKeys.has(key))
    .map(([key, fieldValue]) => ({ key, value: fieldValue }));

  return { blocks, extraFields };
}

function extractTextPiecesFromStreamEvent(record: JsonRecord): string[] {
  const pieces: string[] = [];
  const eventType = typeof record.type === "string" ? record.type.trim() : "";

  // Anthropic streaming events
  if (eventType === "content_block_start" && isRecord(record.content_block)) {
    const startText = typeof record.content_block.text === "string" ? record.content_block.text : "";
    if (startText) pieces.push(startText);
    return pieces;
  }
  if (eventType === "content_block_delta" && isRecord(record.delta)) {
    const deltaText = typeof record.delta.text === "string" ? record.delta.text : "";
    if (deltaText) pieces.push(deltaText);
    const thinkingText = typeof record.delta.thinking === "string" ? record.delta.thinking : "";
    if (thinkingText) pieces.push(thinkingText);
    return pieces;
  }
  if (eventType === "message_start" && isRecord(record.message) && Array.isArray(record.message.content)) {
    pieces.push(...extractContentTexts(record.message.content));
    return pieces;
  }

  // OpenAI chat streaming chunk
  if (Array.isArray(record.choices)) {
    record.choices.forEach((choice) => {
      if (!isRecord(choice)) return;
      if (isRecord(choice.delta)) {
        const deltaContent = choice.delta.content;
        if (typeof deltaContent === "string" && deltaContent) {
          pieces.push(deltaContent);
        } else if (Array.isArray(deltaContent)) {
          pieces.push(...extractContentTexts(deltaContent));
        }
      }
      if (isRecord(choice.message)) {
        pieces.push(...extractContentTexts(choice.message.content ?? choice.message));
      }
    });
    if (pieces.length > 0) return pieces;
  }

  // OpenAI responses style streaming events
  if (typeof record.delta === "string" && record.delta) {
    pieces.push(record.delta);
  }
  if (isRecord(record.delta)) {
    const deltaText = typeof record.delta.text === "string" ? record.delta.text : "";
    if (deltaText) pieces.push(deltaText);
  }
  if (typeof record.output_text === "string" && record.output_text) {
    pieces.push(record.output_text);
  }

  return pieces;
}

function parseEscapedJSONRecord(raw: string): JsonRecord | null {
  let current: unknown = raw;
  for (let i = 0; i < 3; i += 1) {
    if (isRecord(current)) {
      return current;
    }
    if (typeof current !== "string") {
      return null;
    }
    const text = current.trim();
    if (!text) return null;
    try {
      current = JSON.parse(text) as unknown;
    } catch {
      return null;
    }
  }
  return isRecord(current) ? current : null;
}

function decodeJSONStringLiteral(raw: string): string {
  try {
    return JSON.parse(`"${raw}"`) as string;
  } catch {
    return raw
      .replace(/\\\\/g, "\\")
      .replace(/\\"/g, "\"")
      .replace(/\\n/g, "\n")
      .replace(/\\r/g, "\r")
      .replace(/\\t/g, "\t");
  }
}

function sanitizeStreamJSONLine(raw: string): string {
  let line = raw.trim();
  if (!line) return "";
  if (line.startsWith("data:")) {
    line = line.slice(5).trim();
  }
  if (!line || line === "[DONE]") return "";
  if (line === "[" || line === "]") return "";
  if (line.startsWith("[")) {
    line = line.slice(1).trim();
  }
  if (line.endsWith("]")) {
    line = line.slice(0, -1).trim();
  }
  if (line.endsWith(",")) {
    line = line.slice(0, -1).trim();
  }
  return line;
}

function extractTextByRegexFallback(raw: string): string {
  const pieces: string[] = [];

  const anthropicDeltaRegex = /"type"\s*:\s*"\s*text_delta\s*"[\s\S]*?"text"\s*:\s*"((?:\\.|[^"\\])*)"/g;
  for (const match of raw.matchAll(anthropicDeltaRegex)) {
    const value = decodeJSONStringLiteral(match[1] || "");
    if (value) pieces.push(value);
  }
  if (pieces.length > 0) {
    return pieces.join("");
  }

  const openaiDeltaRegex = /"delta"\s*:\s*\{[\s\S]*?"content"\s*:\s*"((?:\\.|[^"\\])*)"/g;
  for (const match of raw.matchAll(openaiDeltaRegex)) {
    const value = decodeJSONStringLiteral(match[1] || "");
    if (value) pieces.push(value);
  }
  if (pieces.length > 0) {
    return pieces.join("");
  }

  return "";
}

function buildParsedOutputPayloadFromRaw(raw: string): ParsedOutputPayload | null {
  const formatted = formatJson(raw);
  if (formatted.parsed) {
    const payload = buildParsedOutputPayload(formatted.value);
    if (payload && payload.blocks.length > 0) {
      return payload;
    }
  }

  const streamPieces: string[] = [];
  const lines = raw.replace(/\r\n/g, "\n").split("\n");
  for (const originLine of lines) {
    const head = originLine.trim();
    if (!head || head.startsWith("event:") || head.startsWith("id:") || head.startsWith("retry:")) {
      continue;
    }
    const line = sanitizeStreamJSONLine(originLine);
    if (!line) {
      continue;
    }
    const parsedRecord = parseEscapedJSONRecord(line);
    if (parsedRecord) {
      streamPieces.push(...extractTextPiecesFromStreamEvent(parsedRecord));
    }
  }

  const text = streamPieces.join("").trim();
  if (!text) {
    const regexText = extractTextByRegexFallback(raw).trim();
    if (!regexText) {
      return null;
    }
    return {
      blocks: [
        {
          id: "stream-merged-text-regex",
          title: "模型响应",
          body: wrapTechnicalTerms(regexText),
        },
      ],
      extraFields: [],
    };
  }
  return {
    blocks: [
      {
        id: "stream-merged-text",
        title: "模型响应",
        body: wrapTechnicalTerms(text),
      },
    ],
    extraFields: [],
  };
}

interface JsonContentProps {
  text: string;
  parsed: boolean;
  empty: boolean;
  syntaxStyle: SyntaxStyle;
}

function JsonContent({ text, parsed, empty, syntaxStyle }: JsonContentProps) {
  const shouldUseSyntaxHighlight = parsed && !empty && text.length <= 200000;
  if (shouldUseSyntaxHighlight) {
    return (
      <div className="w-full max-w-full min-w-0 overflow-x-auto rounded-md border bg-muted/70 font-mono text-sm leading-6">
        <SyntaxHighlighter
          language="json"
          style={syntaxStyle}
          customStyle={{
            margin: 0,
            background: "transparent",
            padding: "1rem",
            fontSize: "0.875rem",
            lineHeight: "1.5rem",
            whiteSpace: "pre",
            minWidth: "100%",
            maxWidth: "100%",
          }}
        >
          {text}
        </SyntaxHighlighter>
      </div>
    );
  }

  return (
    <pre className="whitespace-pre-wrap break-all font-mono text-sm leading-6 bg-muted/70 border rounded-md p-4 overflow-x-auto w-full max-w-full min-w-0">
      {text}
    </pre>
  );
}

function MarkdownOutputText({ value }: { value: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        p: ({ children }) => <p className="mb-3 leading-7 last:mb-0">{children}</p>,
        code: ({ children, className, ...props }) => {
          const isBlock = typeof className === "string" && className.includes("language-");
          if (isBlock) {
            return (
              <code className={className} {...props}>
                {children}
              </code>
            );
          }
          return (
            <code className="rounded-md bg-muted px-2 py-0.5 font-mono text-[0.95em] text-foreground" {...props}>
              {children}
            </code>
          );
        },
      }}
    >
      {value}
    </ReactMarkdown>
  );
}

interface StructuredInputCardProps {
  raw: string;
  syntaxStyle: SyntaxStyle;
}

function StructuredInputCard({ raw, syntaxStyle }: StructuredInputCardProps) {
  const [viewMode, setViewMode] = useState<ViewMode>("parsed");
  const formatted = useMemo(() => formatJson(raw), [raw]);
  const payload = useMemo(() => buildParsedRequestPayload(formatted.value), [formatted.value]);

  useEffect(() => {
    if (!formatted.parsed || payload === null) {
      setViewMode("raw");
      return;
    }
    setViewMode("parsed");
  }, [formatted.parsed, payload]);

  const parsedAvailable = formatted.parsed && payload !== null;

  return (
    <Card>
      <CardHeader className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="space-y-1">
            <CardTitle className="text-base font-semibold">请求输入</CardTitle>
            <CardDescription>
              {parsedAvailable ? "已按角色和字段结构化解析" : "当前内容无法结构化解析，显示原始内容"}
            </CardDescription>
          </div>
          <div className="inline-flex rounded-full border bg-muted p-1">
            <button
              type="button"
              className={`rounded-full px-3 py-1 text-xs font-medium transition ${viewMode === "parsed" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}
              onClick={() => setViewMode("parsed")}
              disabled={!parsedAvailable}
            >
              解析视图
            </button>
            <button
              type="button"
              className={`rounded-full px-3 py-1 text-xs font-medium transition ${viewMode === "raw" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}
              onClick={() => setViewMode("raw")}
            >
              原始视图
            </button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {viewMode === "raw" || !parsedAvailable || payload === null ? (
          <JsonContent text={formatted.text} parsed={formatted.parsed} empty={formatted.empty} syntaxStyle={syntaxStyle} />
        ) : (
          <>
            {payload.meta.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {payload.meta.map((item) => (
                  <div key={item.label} className="inline-flex items-center gap-2 rounded-full border bg-muted/55 px-3 py-1 text-xs">
                    <span className="text-muted-foreground">{item.label}</span>
                    <span className="font-medium text-foreground">{item.value}</span>
                  </div>
                ))}
              </div>
            )}

            {payload.timeline.length > 0 ? (
              <div className="space-y-3">
                {payload.timeline.map((item, index) => (
                  <details
                    key={item.id}
                    open={index < 3}
                    className={`rounded-xl border ${timelineToneClass(item.kind, item.role)}`}
                  >
                    <summary className="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3">
                      <span className={`inline-flex items-center gap-2 text-sm font-semibold ${timelineHeaderClass(item.kind)}`}>
                        {timelineIcon(item.kind, item.role)}
                        {item.title}
                      </span>
                      <span className="text-xs text-muted-foreground">{item.chunks.length} 个片段</span>
                    </summary>
                    <div className="space-y-3 border-t border-border/50 bg-background/70 px-4 py-3">
                      {item.chunks.map((chunk, chunkIndex) => (
                        <div key={`${item.id}-chunk-${chunkIndex}`} className="space-y-1">
                          <div className="text-xs font-medium text-muted-foreground">{chunk.label}</div>
                          <pre className="whitespace-pre-wrap break-words rounded-md border bg-background px-3 py-2 text-sm leading-6">
                            {chunk.value || "(空)"}
                          </pre>
                        </div>
                      ))}
                    </div>
                  </details>
                ))}
              </div>
            ) : (
              <div className="rounded-md border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">未识别到消息结构</div>
            )}

            {payload.extraFields.length > 0 && (
              <div className="space-y-2">
                <div className="text-sm font-semibold">请求详情</div>
                {payload.extraFields.map((field) => (
                  <details key={field.key} className="rounded-lg border bg-muted/35">
                    <summary className="cursor-pointer list-none px-3 py-2 text-sm font-medium">{field.key}</summary>
                    <div className="border-t bg-background/75 p-3">
                      <JsonContent
                        text={toReadableText(field.value)}
                        parsed={typeof field.value === "object" && field.value !== null}
                        empty={field.value === null || field.value === undefined || toReadableText(field.value).trim() === ""}
                        syntaxStyle={syntaxStyle}
                      />
                    </div>
                  </details>
                ))}
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function StructuredOutputCard({ raw, syntaxStyle }: { raw: string; syntaxStyle: SyntaxStyle }) {
  const [viewMode, setViewMode] = useState<ViewMode>("parsed");
  const formatted = useMemo(() => formatJson(raw), [raw]);
  const payload = useMemo(() => buildParsedOutputPayloadFromRaw(raw), [raw]);
  const parsedAvailable = payload !== null && payload.blocks.length > 0;

  useEffect(() => {
    if (!parsedAvailable) {
      setViewMode("raw");
      return;
    }
    setViewMode("parsed");
  }, [parsedAvailable]);

  return (
    <Card>
      <CardHeader className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="space-y-1">
            <CardTitle className="text-base font-semibold">响应输出</CardTitle>
            <CardDescription>
              {parsedAvailable ? "已提取模型返回内容并结构化展示" : "当前内容无法解析，显示原始内容"}
            </CardDescription>
          </div>
          <div className="inline-flex rounded-full border bg-muted p-1">
            <button
              type="button"
              className={`rounded-full px-3 py-1 text-xs font-medium transition ${viewMode === "parsed" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}
              onClick={() => setViewMode("parsed")}
              disabled={!parsedAvailable}
            >
              解析视图
            </button>
            <button
              type="button"
              className={`rounded-full px-3 py-1 text-xs font-medium transition ${viewMode === "raw" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}
              onClick={() => setViewMode("raw")}
            >
              原始视图
            </button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {viewMode === "raw" || !parsedAvailable || payload === null ? (
          <JsonContent text={formatted.text} parsed={formatted.parsed} empty={formatted.empty} syntaxStyle={syntaxStyle} />
        ) : (
          <>
            {payload.blocks.map((block, index) => (
              <details
                key={block.id}
                open={index === 0}
                className="rounded-xl border border-emerald-200 bg-emerald-50/45"
              >
                <summary className="flex cursor-pointer list-none items-center justify-between gap-2 px-4 py-3 text-emerald-800">
                  <span className="text-base font-semibold">{block.title}</span>
                  <span className="text-xs text-emerald-700">展开详情</span>
                </summary>
                <div className="border-t border-emerald-200/70 bg-background/85 px-4 py-3 text-[15px] leading-7 text-foreground">
                  <MarkdownOutputText value={block.body} />
                  {block.images && block.images.length > 0 && (
                    <div className="mt-4 grid gap-3 sm:grid-cols-2">
                      {block.images.map((image, imageIndex) => (
                        <figure key={`${block.id}-image-${imageIndex}`} className="rounded-lg border bg-background p-2">
                          <img
                            src={image.src}
                            alt={`${block.title} 预览 ${imageIndex + 1}`}
                            className="max-h-[340px] w-full rounded object-contain bg-muted/25"
                            loading="lazy"
                          />
                          <figcaption className="mt-2 text-xs text-muted-foreground">
                            {image.source === "base64" ? "来源：Base64" : "来源：URL"} · {image.mime}
                          </figcaption>
                        </figure>
                      ))}
                    </div>
                  )}
                  {block.videos && block.videos.length > 0 && (
                    <div className="mt-4 grid gap-3">
                      {block.videos.map((video, videoIndex) => (
                        <figure key={`${block.id}-video-${videoIndex}`} className="rounded-lg border bg-background p-2">
                          <video
                            controls
                            preload="metadata"
                            className="max-h-[420px] w-full rounded bg-black"
                          >
                            <source src={video.src} type={video.mime} />
                            你的浏览器不支持视频播放。
                          </video>
                          <figcaption className="mt-2 text-xs text-muted-foreground">
                            {video.source === "url" ? "来源：URL" : "来源：未知"} · {video.mime}
                          </figcaption>
                        </figure>
                      ))}
                    </div>
                  )}
                </div>
              </details>
            ))}

            {payload.extraFields.length > 0 && (
              <div className="space-y-2">
                <div className="text-sm font-semibold">响应详情</div>
                {payload.extraFields.map((field) => (
                  <details key={field.key} className="rounded-lg border bg-muted/35">
                    <summary className="cursor-pointer list-none px-3 py-2 text-sm font-medium">{field.key}</summary>
                    <div className="border-t bg-background/75 p-3">
                      <JsonContent
                        text={toReadableText(field.value)}
                        parsed={typeof field.value === "object" && field.value !== null}
                        empty={field.value === null || field.value === undefined || toReadableText(field.value).trim() === ""}
                        syntaxStyle={syntaxStyle}
                      />
                    </div>
                  </details>
                ))}
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

export default function LogChatPage() {
  const { logId } = useParams<{ logId: string }>();
  const navigate = useNavigate();
  const [chatIO, setChatIO] = useState<ChatIO | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadErrorMessage, setLoadErrorMessage] = useState<string | null>(null);
  const syntaxStyle = duotoneLight;
  const outputList = chatIO?.OfStringArray ?? [];
  const hasArrayOutput = outputList.length > 0;
  const singleOutput = chatIO?.OfString ?? "";
  const normalizedOutputRaw = useMemo(() => {
    if (!hasArrayOutput) return singleOutput;
    return outputList.join("\n");
  }, [hasArrayOutput, outputList, singleOutput]);
  useEffect(() => {
    if (!logId) {
      const message = "缺少日志 ID";
      toast.error(message);
      setLoadErrorMessage(message);
      setLoading(false);
      return;
    }

    const fetchChatIO = async () => {
      try {
        const data = await getChatIO(logId, { mode: "full" });
        setChatIO(data);
        setLoadErrorMessage(null);
      } catch (fetchError) {
        const message = fetchError instanceof Error && fetchError.message.includes("chat io not found")
          ? "暂无会话记录，可能未开启 IO 记录"
          : fetchError instanceof Error
            ? fetchError.message
            : "获取会话日志失败";
        toast.error(message);
        setLoadErrorMessage(message);
      } finally {
        setLoading(false);
      }
    };

    void fetchChatIO();
  }, [logId]);

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loading message="加载会话详情" />
      </div>
    );
  }

  return (
    <div className="space-y-6 h-full overflow-y-auto overflow-x-hidden">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">会话详情</h1>
          <p className="text-sm text-muted-foreground">日志 ID：{logId}</p>
        </div>
        <Button variant="outline" onClick={() => navigate(-1)}>
          返回
        </Button>
      </div>

      {loadErrorMessage && (
        <Card>
          <CardHeader>
            <CardTitle>加载失败</CardTitle>
            <CardDescription>{loadErrorMessage}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => navigate(-1)}>回到日志列表</Button>
          </CardContent>
        </Card>
      )}

      {!loadErrorMessage && chatIO && (
        <div className="space-y-6">
          <StructuredInputCard raw={chatIO.Input} syntaxStyle={syntaxStyle} />

          <div className="space-y-4">
            <StructuredOutputCard raw={normalizedOutputRaw} syntaxStyle={syntaxStyle} />
          </div>
        </div>
      )}
    </div>
  );
}
