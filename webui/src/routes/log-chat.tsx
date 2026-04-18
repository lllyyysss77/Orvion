import { useEffect, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import Loading from "@/components/loading";
import { getChatIO, type ChatIO } from "@/lib/api";
import { getStoredAuthToken } from "@/lib/auth";
import { buildReplayCurlSnippet, inferGatewayEndpoint, maskAuthToken } from "@/lib/curl";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { duotoneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import { toast } from "sonner";
import { Copy } from "lucide-react";

type SyntaxStyle = typeof duotoneLight;

interface JsonBlockProps {
  title: string;
  raw: string;
  syntaxStyle: SyntaxStyle;
}

interface FormattedJson {
  text: string;
  parsed: boolean;
  empty: boolean;
}

function formatJson(raw: string): FormattedJson {
  if (!raw || raw.trim().length === 0) {
    return { text: "(无内容)", parsed: false, empty: true };
  }

  try {
    const parsedJson = JSON.parse(raw);
    return {
      text: JSON.stringify(parsedJson, null, 2),
      parsed: true,
      empty: false
    };
  } catch {
    return {
      text: raw,
      parsed: false,
      empty: false
    };
  }
}

function JsonBlock({ title, raw, syntaxStyle }: JsonBlockProps) {
  const { text, parsed, empty } = formatJson(raw);

  return (
    <Card>
      <CardHeader className="space-y-2">
        <CardTitle className="text-base font-semibold">{title}</CardTitle>
        <CardDescription>
          {empty ? "暂无数据" : parsed ? "格式化 JSON 预览" : "原始数据（非 JSON 或解析失败）"}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <JsonContent text={text} parsed={parsed} empty={empty} syntaxStyle={syntaxStyle} />
      </CardContent>
    </Card>
  );
}

interface OutputPreviewProps {
  index: number;
  raw: string;
  syntaxStyle: SyntaxStyle;
}

function OutputPreview({ index, raw, syntaxStyle }: OutputPreviewProps) {
  const { text, parsed, empty } = formatJson(raw);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <p className="text-sm font-medium text-muted-foreground">响应片段 {index + 1}</p>
        {!parsed && !empty && <span className="text-xs text-muted-foreground">原始字符串</span>}
        {empty && <span className="text-xs text-muted-foreground">暂无数据</span>}
      </div>
      <JsonContent text={text} parsed={parsed} empty={empty} syntaxStyle={syntaxStyle} />
    </div>
  );
}

interface DefaultOutputProps {
  raw: string;
  syntaxStyle: SyntaxStyle;
}

function DefaultOutput({ raw, syntaxStyle }: DefaultOutputProps) {
  const { text, parsed, empty } = formatJson(raw);

  return (
    <div className="space-y-2">
      {!parsed && !empty && <span className="text-xs text-muted-foreground">原始字符串</span>}
      {empty && <span className="text-xs text-muted-foreground">暂无数据</span>}
      <JsonContent text={text} parsed={parsed} empty={empty} syntaxStyle={syntaxStyle} />
    </div>
  );
}

interface JsonContentProps {
  text: string;
  parsed: boolean;
  empty: boolean;
  syntaxStyle: SyntaxStyle;
}

function JsonContent({ text, parsed, empty, syntaxStyle }: JsonContentProps) {
  if (parsed && !empty) {
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
            maxWidth: "100%"
          }}
        >
          {text}
        </SyntaxHighlighter>
      </div>
    );
  }

  return (
    <pre className="whitespace-pre font-mono text-sm leading-6 bg-muted/70 border rounded-md p-4 overflow-x-auto w-full max-w-full min-w-0">{text}</pre>
  );
}

function useSyntaxStyle(): SyntaxStyle {
  return duotoneLight;
}

type LogChatLocationState = {
  style?: string;
};

export default function LogChatPage() {
  const { logId } = useParams<{ logId: string }>();
  const location = useLocation();
  const navigate = useNavigate();
  const [chatIO, setChatIO] = useState<ChatIO | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingFull, setLoadingFull] = useState(false);
  const [copyingCurlVariant, setCopyingCurlVariant] = useState<"masked" | "raw" | null>(null);
  const [loadErrorMessage, setLoadErrorMessage] = useState<string | null>(null);
  const logStyle = ((location.state as LogChatLocationState | null)?.style ?? "").trim();
  const outputList = chatIO?.OfStringArray ?? [];
  const hasArrayOutput = outputList.length > 0;
  const singleOutput = chatIO?.OfString ?? "";
  const syntaxStyle = useSyntaxStyle();
  const isSummary = chatIO?.summary;
  const isTruncated = Boolean(
    chatIO?.truncated_input || chatIO?.truncated_output || chatIO?.truncated_output_items
  );

  useEffect(() => {
    if (!logId) {
      const message = "缺少日志 ID";
      toast.error(message);
      setLoadErrorMessage(message);
      setLoading(false);
      return;
    }

    // 支持数字 ID 和 UUID 格式
    const isValidId = logId.length > 0;
    if (!isValidId) {
      const message = "日志 ID 无效";
      toast.error(message);
      setLoadErrorMessage(message);
      setLoading(false);
      return;
    }

    const fetchChatIO = async () => {
      try {
        const data = await getChatIO(logId, {
          mode: "summary",
          inputLimit: 20000,
          outputLimit: 20000,
          outputItemsLimit: 50
        });
        setChatIO(data);
        setLoadErrorMessage(null);
      } catch (fetchError) {
        let message = "获取会话日志失败";
        if (fetchError instanceof Error) {
          if (fetchError.message.includes("chat io not found")) {
            message = "暂无会话记录，可能未开启 IO 记录";
          } else {
            message = fetchError.message;
          }
        }
        toast.error(message);
        setLoadErrorMessage(message);
      } finally {
        setLoading(false);
      }
    };

    fetchChatIO();
  }, [logId]);

  const handleLoadFull = async () => {
    if (!logId || loadingFull) return;
    setLoadingFull(true);
    try {
      const data = await getChatIO(logId, { mode: "full" });
      setChatIO(data);
      setLoadErrorMessage(null);
    } catch (fetchError) {
      let message = "获取完整会话日志失败";
      if (fetchError instanceof Error) {
        message = fetchError.message;
      }
      toast.error(message);
      setLoadErrorMessage(message);
    } finally {
      setLoadingFull(false);
    }
  };

  const getReplayInput = async (): Promise<string> => {
    const currentInput = (chatIO?.Input ?? "").trim();
    if (currentInput && !chatIO?.truncated_input) {
      return currentInput;
    }
    if (!logId) {
      throw new Error("缺少日志 ID");
    }
    const fullData = await getChatIO(logId, { mode: "full" });
    setChatIO(fullData);
    const fullInput = (fullData.Input ?? "").trim();
    if (!fullInput) {
      throw new Error("请求输入为空");
    }
    return fullInput;
  };

  const handleCopyReplayCurl = async (masked: boolean) => {
    if (copyingCurlVariant) return;
    setCopyingCurlVariant(masked ? "masked" : "raw");
    try {
      const requestBody = await getReplayInput();
      const endpoint = inferGatewayEndpoint(logStyle, requestBody);
      const rawAuthToken = getStoredAuthToken();
      const authToken = masked ? maskAuthToken(rawAuthToken) : (rawAuthToken || "YOUR_AUTH_TOKEN");
      const curlSnippet = buildReplayCurlSnippet({
        baseUrl: window.location.origin,
        endpoint,
        authToken,
        requestBody,
      });
      await navigator.clipboard.writeText(curlSnippet);
      toast.success(masked ? "已复制 cURL（掩码版）" : "已复制 cURL（真实版）");
    } catch (error) {
      const message = error instanceof Error ? error.message : "未知错误";
      toast.error(`复制 cURL 失败: ${message}`);
    } finally {
      setCopyingCurlVariant(null);
    }
  };

  if (loading) {
    return <Loading message="加载会话详情" />;
  }

  return (
    <div className="space-y-6 h-full overflow-y-auto overflow-x-hidden">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">会话详情</h1>
          <p className="text-sm text-muted-foreground">日志 ID：{logId}</p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Button
            variant="outline"
            size="sm"
            className="h-8"
            onClick={() => void handleCopyReplayCurl(true)}
            disabled={copyingCurlVariant !== null || Boolean(loadErrorMessage)}
          >
            <Copy className="size-3.5" />
            复制 cURL（掩码）
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-8"
            onClick={() => void handleCopyReplayCurl(false)}
            disabled={copyingCurlVariant !== null || Boolean(loadErrorMessage)}
          >
            <Copy className="size-3.5" />
            复制 cURL（真实）
          </Button>
          <Button variant="outline" onClick={() => navigate(-1)}>
            返回
          </Button>
        </div>
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
          {isSummary && (
            <Card>
              <CardHeader>
                <CardTitle className="text-base font-semibold">摘要模式</CardTitle>
                <CardDescription>
                  为避免大文本加载卡顿，当前仅展示摘要。{isTruncated ? "内容已截断。" : ""}
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-3 text-sm text-muted-foreground">
                <div className="flex flex-wrap gap-4">
                  <span>输入大小：{chatIO.input_bytes ?? 0} 字节</span>
                  <span>输出大小：{chatIO.output_bytes ?? 0} 字节</span>
                  <span>输出条目：{chatIO.output_items ?? outputList.length}</span>
                </div>
                <div>
                  <Button onClick={handleLoadFull} disabled={loadingFull}>
                    {loadingFull ? "加载完整内容中..." : "加载完整内容"}
                  </Button>
                </div>
              </CardContent>
            </Card>
          )}

          <JsonBlock title="请求输入" raw={chatIO.Input} syntaxStyle={syntaxStyle} />

          <Card>
            <CardHeader>
              <CardTitle className="text-base font-semibold">响应输出</CardTitle>
              <CardDescription>
                {hasArrayOutput
                  ? "列表中的每一项都会尝试以 JSON 格式展示"
                  : "如果数据无法解析为 JSON，将保留原始内容"}
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {hasArrayOutput ? (
                outputList.map((entry, index) => (
                  <OutputPreview key={`chat-io-${index}`} index={index} raw={entry} syntaxStyle={syntaxStyle} />
                ))
              ) : (
                <DefaultOutput raw={singleOutput} syntaxStyle={syntaxStyle} />
              )}
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}
