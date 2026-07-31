import { lazy, Suspense, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import Loading from "@/components/loading";
import { getChatIO, type ChatIO } from "@/lib/api";
import { toast } from "sonner";

const LogChatContent = lazy(() => import("./log-chat-content"));

export default function LogChatPage() {
  const { logId } = useParams<{ logId: string }>();
  const navigate = useNavigate();
  const [chatIO, setChatIO] = useState<ChatIO | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingFull, setLoadingFull] = useState(false);
  const [loadErrorMessage, setLoadErrorMessage] = useState<string | null>(null);

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
        const data = await getChatIO(logId, {
          mode: "summary",
          inputLimit: 100_000,
          outputLimit: 100_000,
          outputItemsLimit: 20,
        });
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

  const contentTruncated = Boolean(
    chatIO?.truncated_input || chatIO?.truncated_output || chatIO?.truncated_output_items,
  );

  const loadFullContent = async () => {
    if (!logId || loadingFull) return;
    setLoadingFull(true);
    try {
      setChatIO(await getChatIO(logId, { mode: "full" }));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "完整内容加载失败");
    } finally {
      setLoadingFull(false);
    }
  };

  if (loading) {
    return <div className="flex h-full items-center justify-center"><Loading message="加载会话详情" /></div>;
  }

  return (
    <div className="space-y-6 h-full overflow-y-auto overflow-x-hidden">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">会话详情</h1>
          <p className="text-sm text-muted-foreground">日志 ID：{logId}</p>
        </div>
        <Button variant="outline" onClick={() => navigate(-1)}>返回</Button>
      </div>

      {loadErrorMessage && (
        <Card>
          <CardHeader><CardTitle>加载失败</CardTitle><CardDescription>{loadErrorMessage}</CardDescription></CardHeader>
          <CardContent><Button onClick={() => navigate(-1)}>回到日志列表</Button></CardContent>
        </Card>
      )}

      {!loadErrorMessage && chatIO && (
        <div className="space-y-6">
          {contentTruncated && (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-muted/35 px-4 py-3">
              <div className="text-sm text-muted-foreground">
                内容较大，当前显示摘要（输入 {chatIO.input_bytes?.toLocaleString() ?? 0} 字节，输出 {chatIO.output_bytes?.toLocaleString() ?? 0} 字节）
              </div>
              <Button type="button" variant="outline" size="sm" disabled={loadingFull} onClick={() => void loadFullContent()}>
                {loadingFull ? "加载中..." : "加载完整内容"}
              </Button>
            </div>
          )}
          <Suspense fallback={<Loading message="正在准备会话内容" />}>
            <LogChatContent chatIO={chatIO} />
          </Suspense>
        </div>
      )}
    </div>
  );
}
