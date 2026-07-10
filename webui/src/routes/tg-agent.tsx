import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  Bot,
  CheckCircle2,
  ChevronDown,
  CircleDashed,
  ListChecks,
  RefreshCw,
  TerminalSquare,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import {
  deleteTelegramAgentSession,
  getTelegramAgentToolCallLogs,
  type TelegramAgentSessionSummary,
  type TelegramAgentToolLogResponse,
  type TelegramAgentToolLogStep,
} from "@/lib/api";

const POLL_INTERVAL_MS = 3_000;
const DEFAULT_LIMIT = 200;

const statusMeta: Record<string, { label: string; className: string; icon: typeof CheckCircle2 }> = {
  executing: {
    label: "执行中",
    className: "border-indigo-300/70 bg-indigo-50 text-indigo-700 dark:border-indigo-400/30 dark:bg-indigo-500/10 dark:text-indigo-300",
    icon: RefreshCw,
  },
  completed: {
    label: "已完成",
    className: "border-sky-300/70 bg-sky-50 text-sky-700 dark:border-sky-400/30 dark:bg-sky-500/10 dark:text-sky-300",
    icon: CheckCircle2,
  },
  executed: {
    label: "已执行",
    className: "border-emerald-300/70 bg-emerald-50 text-emerald-700 dark:border-emerald-400/30 dark:bg-emerald-500/10 dark:text-emerald-300",
    icon: CheckCircle2,
  },
  failed: {
    label: "失败",
    className: "border-rose-300/70 bg-rose-50 text-rose-700 dark:border-rose-400/30 dark:bg-rose-500/10 dark:text-rose-300",
    icon: TriangleAlert,
  },
};

const sourceLabel: Record<string, string> = {
  function_call: "函数调用",
  tool_action: "工具动作",
  scheduled_task: "定时任务",
};

const formatDateTime = (value?: string) => {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--";
  return date.toLocaleString();
};

const formatShortTime = (value?: string) => {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const getStatusMeta = (status: string) => statusMeta[status] ?? {
  label: status || "未知",
  className: "border-muted bg-muted text-muted-foreground",
  icon: CircleDashed,
};

const compactText = (value: string, limit = 220) => {
  const text = value.trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}...`;
};

const shortConversationID = (value?: string) => {
  const text = (value ?? "").trim();
  if (!text) return "未记录";
  if (text.length <= 18) return text;
  return `${text.slice(0, 10)}...${text.slice(-6)}`;
};


const sessionIdentity = (chatID: number, conversationID?: string) => `${chatID}:${(conversationID ?? "").trim() || "__unrecorded__"}`;

const formatJsonLike = (value: string) => {
  const text = value.trim();
  if (!text) return "";
  try {
    return JSON.stringify(JSON.parse(text) as unknown, null, 2);
  } catch {
    return text;
  }
};

const getStepTitle = (step: TelegramAgentToolLogStep) => {
  if (step.action_summary.trim()) return step.action_summary.trim().split("\n")[0];
  if (step.tool_name.trim()) return step.tool_name.trim();
  return (sourceLabel[step.source] ?? step.source) || "Agent 步骤";
};

const sortStepsAsc = (steps: TelegramAgentToolLogStep[]) => (
  [...steps].sort((left, right) => {
    const leftTime = new Date(left.created_at).getTime();
    const rightTime = new Date(right.created_at).getTime();
    if (leftTime !== rightTime) return leftTime - rightTime;
    return left.id - right.id;
  })
);

function StatusBadge({ status }: { status: string }) {
  const meta = getStatusMeta(status);
  const Icon = meta.icon;
  return (
    <Badge variant="outline" className={cn("gap-1.5 rounded-full px-2 py-0.5 font-medium", meta.className)}>
      <Icon className={cn("size-3.5", status === "executing" && "animate-spin")} />
      {meta.label}
    </Badge>
  );
}

function SessionButton({
  session,
  selected,
  deleting,
  onClick,
  onDelete,
}: {
  session: TelegramAgentSessionSummary;
  selected: boolean;
  deleting: boolean;
  onClick: () => void;
  onDelete: () => void;
}) {
  return (
    <div
      className={cn(
        "flex w-full items-start gap-2 rounded-lg border px-3 py-3 transition-colors",
        selected ? "border-primary/50 bg-primary/10" : "border-border bg-card hover:bg-muted/60",
      )}
    >
      <button type="button" onClick={onClick} className="min-w-0 flex-1 text-left">
        <div className="flex items-center gap-3">
          <div className="min-w-0">
            <div className="truncate text-sm font-medium">会话 {shortConversationID(session.conversation_id)}</div>
            <div className="mt-1 text-xs text-muted-foreground">{formatDateTime(session.latest_at)}</div>
          </div>
        </div>
      </button>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
        disabled={deleting}
        onClick={onDelete}
        aria-label="删除会话"
      >
        <Trash2 className="size-4" />
      </Button>
    </div>
  );
}

function StepDetail({ title, value }: { title: string; value: string }) {
  const formatted = formatJsonLike(value);
  if (!formatted.trim()) return null;
  return (
    <div className="mt-3">
      <div className="text-xs font-medium text-muted-foreground">{title}</div>
      <pre className="mt-1 max-h-40 overflow-auto rounded-md border bg-muted/40 p-3 text-xs leading-relaxed text-foreground">
        {compactText(formatted, 2400)}
      </pre>
    </div>
  );
}

function TimelineStep({
  step,
  last,
  expanded,
  onToggle,
}: {
  step: TelegramAgentToolLogStep;
  last: boolean;
  expanded: boolean;
  onToggle: () => void;
}) {
  const meta = getStatusMeta(step.status);
  const Icon = meta.icon;
  return (
    <div className="relative flex gap-3">
      {!last ? <div className="absolute left-[15px] top-8 h-full w-px bg-border" /> : null}
      <div className={cn("relative z-10 flex size-8 shrink-0 items-center justify-center rounded-full border bg-background", meta.className)}>
        <Icon className="size-4" />
      </div>
      <div className="min-w-0 flex-1 rounded-lg border bg-card p-4 shadow-sm">
        <button type="button" className="w-full text-left" aria-expanded={expanded} onClick={onToggle}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="max-w-full truncate text-sm font-semibold">{getStepTitle(step)}</h3>
                <StatusBadge status={step.status} />
              </div>
              <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                <span>{formatDateTime(step.created_at)}</span>
                <span>会话 {shortConversationID(step.conversation_id)}</span>
                <span>{sourceLabel[step.source] ?? step.source}</span>
                <span>{step.tool_name || step.action_kind || "未命名工具"}</span>
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
              <span>{formatShortTime(step.created_at)}</span>
              <ChevronDown className={cn("size-4 transition-transform", expanded && "rotate-180")} />
            </div>
          </div>
        </button>
        {expanded ? (
          <div className="min-w-0">
            {step.action_summary.trim() ? <p className="mt-3 whitespace-pre-wrap text-sm text-foreground">{compactText(step.action_summary, 700)}</p> : null}
            {step.error.trim() ? <p className="mt-3 rounded-md border border-rose-300/60 bg-rose-50 p-3 text-sm text-rose-700 dark:border-rose-400/30 dark:bg-rose-500/10 dark:text-rose-300">{compactText(step.error, 900)}</p> : null}
            <StepDetail title="参数" value={step.arguments} />
            <StepDetail title="结果" value={step.result} />
            <div className="mt-3 flex flex-wrap gap-3 text-xs text-muted-foreground">
              {step.executed_at ? <span>执行：{formatDateTime(step.executed_at)}</span> : null}
            </div>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function DeleteSessionDialog({
  session,
  deleting,
  onOpenChange,
  onConfirm,
}: {
  session: TelegramAgentSessionSummary | null;
  deleting: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open={Boolean(session)} onOpenChange={onOpenChange}>
      <AlertDialogContent className="overflow-hidden p-0 sm:max-w-xl">
        <div className="border-b bg-rose-50/70 px-5 py-4 dark:bg-rose-500/10">
          <AlertDialogHeader className="text-left">
            <div className="flex items-start gap-3">
              <div className="flex size-10 shrink-0 items-center justify-center rounded-full border border-rose-300/60 bg-rose-100 text-rose-700 dark:border-rose-400/30 dark:bg-rose-500/15 dark:text-rose-300">
                <Trash2 className="size-5" />
              </div>
              <div className="min-w-0">
                <AlertDialogTitle className="text-base">删除这个 TG 对话？</AlertDialogTitle>
                <AlertDialogDescription className="mt-1">
                  将删除该会话的上下文和工具调用记录。
                </AlertDialogDescription>
              </div>
            </div>
          </AlertDialogHeader>
        </div>

        {session ? (
          <div className="space-y-3 px-5 py-4">
            <div className="rounded-lg border bg-muted/30 px-4 py-3">
              <div className="truncate text-sm font-semibold text-foreground">
                会话 {shortConversationID(session.conversation_id)}
              </div>
              <div className="mt-2 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
                <span>Chat：{session.chat_id}</span>
                <span>步骤：{session.total_steps}</span>
                <span>失败：{session.failed}</span>
                <span>最近：{formatDateTime(session.latest_at)}</span>
              </div>
              <div className="mt-3 rounded-md border bg-background/70 px-3 py-2 text-xs leading-relaxed text-muted-foreground">
                最近工具：{session.latest_tool_name || "未记录"}
              </div>
            </div>
            <p className="text-xs text-muted-foreground">
              这会让后续 Agent 对话无法再读取该会话的历史上下文。此操作无法撤销。
            </p>
          </div>
        ) : null}

        <AlertDialogFooter className="border-t px-5 py-4">
          <AlertDialogCancel disabled={deleting}>取消</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            disabled={deleting}
            onClick={(event) => {
              event.preventDefault();
              onConfirm();
            }}
          >
            {deleting ? "删除中..." : "确认删除"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export default function TelegramAgentPage() {
  const [snapshot, setSnapshot] = useState<TelegramAgentToolLogResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [selectedConversationID, setSelectedConversationID] = useState<string>("all");
  const [expandedStepIDs, setExpandedStepIDs] = useState<Set<number>>(() => new Set());
  const [deletingConversationID, setDeletingConversationID] = useState("");
  const [deleteSessionTarget, setDeleteSessionTarget] = useState<TelegramAgentSessionSummary | null>(null);
  const timelineViewportRef = useRef<HTMLDivElement | null>(null);

  const fetchLogs = useCallback(async (initial = false) => {
    if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
    if (initial) setLoading(true);
    else setRefreshing(true);

    try {
      const data = await getTelegramAgentToolCallLogs({
        limit: DEFAULT_LIMIT,
      });
      setSnapshot(data);
      if (selectedConversationID !== "all" && !data.sessions.some((session) => session.conversation_id === selectedConversationID)) {
        setSelectedConversationID("all");
      }
    } catch (error) {
      console.error("获取 TG Agent 日志失败", error);
      if (initial) toast.error(error instanceof Error ? error.message : "获取 TG Agent 日志失败");
    } finally {
      if (initial) setLoading(false);
      else setRefreshing(false);
    }
  }, [selectedConversationID]);

  useEffect(() => {
    void fetchLogs(true);
  }, [fetchLogs]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      void fetchLogs(false);
    }, POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [fetchLogs]);

  const toggleStepExpanded = useCallback((stepID: number) => {
    setExpandedStepIDs((previous) => {
      const next = new Set(previous);
      if (next.has(stepID)) {
        next.delete(stepID);
      } else {
        next.add(stepID);
      }
      return next;
    });
  }, []);

  const handleDeleteSession = useCallback(async (session: TelegramAgentSessionSummary) => {
    const conversationID = session.conversation_id.trim();
    const deletingKey = sessionIdentity(session.chat_id, conversationID);

    setDeletingConversationID(deletingKey);
    try {
      await deleteTelegramAgentSession(conversationID, session.chat_id);
      setSnapshot((previous) => {
        if (!previous) return previous;
        return {
          ...previous,
          sessions: previous.sessions.filter((item) => sessionIdentity(item.chat_id, item.conversation_id) !== deletingKey),
          steps: previous.steps.filter((step) => sessionIdentity(step.chat_id, step.conversation_id) !== deletingKey),
        };
      });
      setExpandedStepIDs((previous) => {
        const remainingStepIDs = new Set((snapshot?.steps ?? [])
          .filter((step) => sessionIdentity(step.chat_id, step.conversation_id) !== deletingKey)
          .map((step) => step.id));
        const next = new Set<number>();
        previous.forEach((stepID) => {
          if (remainingStepIDs.has(stepID)) next.add(stepID);
        });
        return next;
      });
      if (selectedConversationID === conversationID) {
        setSelectedConversationID("all");
      }
      toast.success("会话已删除");
      setDeleteSessionTarget(null);
      void fetchLogs(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除会话失败");
    } finally {
      setDeletingConversationID("");
    }
  }, [fetchLogs, selectedConversationID, snapshot?.steps]);

  const sessions = snapshot?.sessions ?? [];
  const visibleSteps = useMemo(() => {
    const steps = snapshot?.steps ?? [];
    const filtered = selectedConversationID === "all"
      ? steps
      : steps.filter((step) => step.conversation_id === selectedConversationID);
    return sortStepsAsc(filtered);
  }, [selectedConversationID, snapshot?.steps]);

  const currentStep = visibleSteps.at(-1);
  const latestStepKey = currentStep ? `${currentStep.id}:${currentStep.status}:${currentStep.updated_at}` : "";

  useEffect(() => {
    if (!latestStepKey || loading) return;
    const viewport = timelineViewportRef.current;
    if (!viewport) return;
    window.requestAnimationFrame(() => {
      viewport.scrollTo({ top: viewport.scrollHeight, behavior: "smooth" });
    });
  }, [latestStepKey, loading]);

  return (
    <div className="mx-auto flex h-full w-full max-w-7xl flex-col gap-5 overflow-hidden px-4 py-6 lg:px-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Bot className="size-4" />
            Telegram Agent
          </div>
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void fetchLogs(false)}
          disabled={refreshing}
        >
          <RefreshCw className={cn("mr-2 size-4", refreshing && "animate-spin")} />
          刷新
        </Button>
      </div>

      <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[320px_1fr]">
        <aside className="flex min-h-0 flex-col gap-3 rounded-lg border bg-card p-3 shadow-sm">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <ListChecks className="size-4" />
              会话
            </div>
            <Badge variant="outline" className="rounded-full">{sessions.length}</Badge>
          </div>
          <Button
            variant={selectedConversationID === "all" ? "default" : "outline"}
            className="justify-start"
            onClick={() => setSelectedConversationID("all")}
          >
            全部会话
          </Button>
          <div className="min-h-0 flex-1 space-y-2 overflow-auto pr-1">
            {sessions.map((session) => (
              <SessionButton
                key={`${session.chat_id}:${session.conversation_id}`}
                session={session}
                selected={selectedConversationID === session.conversation_id}
                deleting={deletingConversationID === sessionIdentity(session.chat_id, session.conversation_id)}
                onClick={() => setSelectedConversationID(session.conversation_id)}
                onDelete={() => setDeleteSessionTarget(session)}
              />
            ))}
            {!loading && sessions.length === 0 ? (
              <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
                暂无 TG Agent 工具调用记录
              </div>
            ) : null}
          </div>
        </aside>

        <section className="flex min-h-0 flex-col overflow-hidden rounded-lg border bg-background p-4 shadow-sm">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b pb-4">
            <div>
              <div className="flex items-center gap-2 text-sm font-semibold">
                <TerminalSquare className="size-4" />
                执行时间线
              </div>
              <div className="mt-1 text-xs text-muted-foreground">
                {currentStep ? `当前步骤：${getStepTitle(currentStep)}` : "当前没有可展示步骤"}
              </div>
            </div>
            {currentStep ? <StatusBadge status={currentStep.status} /> : null}
          </div>

          <div ref={timelineViewportRef} className="mt-4 min-h-0 flex-1 space-y-4 overflow-y-auto pr-1">
            {loading ? (
              <div className="flex min-h-[360px] items-center justify-center text-sm text-muted-foreground">
                <RefreshCw className="mr-2 size-4 animate-spin" />
                正在读取 TG Agent 执行日志...
              </div>
            ) : visibleSteps.length > 0 ? (
              visibleSteps.map((step, index) => (
                <TimelineStep
                  key={step.id}
                  step={step}
                  last={index === visibleSteps.length - 1}
                  expanded={expandedStepIDs.has(step.id)}
                  onToggle={() => toggleStepExpanded(step.id)}
                />
              ))
            ) : (
              <div className="flex min-h-[360px] flex-col items-center justify-center gap-3 text-center text-sm text-muted-foreground">
                <Bot className="size-10" />
                当前筛选条件下没有执行步骤
              </div>
            )}
          </div>
        </section>
      </div>
      <DeleteSessionDialog
        session={deleteSessionTarget}
        deleting={deleteSessionTarget ? deletingConversationID === sessionIdentity(deleteSessionTarget.chat_id, deleteSessionTarget.conversation_id) : false}
        onOpenChange={(open) => {
          if (!open && deletingConversationID === "") setDeleteSessionTarget(null);
        }}
        onConfirm={() => {
          if (deleteSessionTarget) void handleDeleteSession(deleteSessionTarget);
        }}
      />
    </div>
  );
}
