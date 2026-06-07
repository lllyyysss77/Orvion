import { useCallback, useEffect, useState } from "react";
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
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import {
  CalendarClock,
  Pencil,
  Plus,
  RefreshCw,
  Send,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import {
  createTelegramAgentScheduledTask,
  deleteTelegramAgentScheduledTask,
  getTelegramAgentScheduledTasks,
  updateTelegramAgentScheduledTask,
  updateTelegramAgentScheduledTaskStatus,
  type TelegramAgentScheduledTask,
  type TelegramAgentScheduledTaskPayload,
  type TelegramAgentScheduleType,
} from "@/lib/api";

type ScheduledTaskFormState = {
  name: string;
  prompt: string;
  enabled: boolean;
  schedule_type: TelegramAgentScheduleType;
  interval_minutes: string;
  time_of_day: string;
  timezone: string;
  push_to_conversation: boolean;
  chat_id: string;
};

const defaultScheduledTaskForm: ScheduledTaskFormState = {
  name: "",
  prompt: "",
  enabled: true,
  schedule_type: "interval",
  interval_minutes: "60",
  time_of_day: "09:00",
  timezone: "Local",
  push_to_conversation: true,
  chat_id: "",
};

const formatDateTime = (value?: string) => {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--";
  return date.toLocaleString();
};

const compactText = (value: string, limit = 220) => {
  const text = value.trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}...`;
};

const formatSchedule = (task: TelegramAgentScheduledTask) => {
  if (task.schedule_type === "daily") {
    return `每天 ${task.time_of_day || "09:00"}`;
  }
  return `每 ${Math.max(task.interval_minutes || 0, 1)} 分钟`;
};

const formatTaskStatus = (task: TelegramAgentScheduledTask) => {
  if (task.running) return "执行中";
  if (!task.enabled) return "已停用";
  if (task.last_status === "error") return "失败";
  if (task.last_status === "success") return "正常";
  return "待执行";
};

const taskStatusClassName = (task: TelegramAgentScheduledTask) => {
  if (task.running) return "border-indigo-300/70 bg-indigo-50 text-indigo-700 dark:border-indigo-400/30 dark:bg-indigo-500/10 dark:text-indigo-300";
  if (!task.enabled) return "border-slate-300 bg-slate-50 text-slate-600 dark:border-slate-700 dark:bg-slate-900/60 dark:text-slate-300";
  if (task.last_status === "error") return "border-rose-300/70 bg-rose-50 text-rose-700 dark:border-rose-400/30 dark:bg-rose-500/10 dark:text-rose-300";
  return "border-emerald-300/70 bg-emerald-50 text-emerald-700 dark:border-emerald-400/30 dark:bg-emerald-500/10 dark:text-emerald-300";
};

const scheduledTaskToForm = (task?: TelegramAgentScheduledTask): ScheduledTaskFormState => {
  if (!task) return { ...defaultScheduledTaskForm };
  return {
    name: task.name,
    prompt: task.prompt,
    enabled: task.enabled,
    schedule_type: task.schedule_type,
    interval_minutes: String(task.interval_minutes || 60),
    time_of_day: task.time_of_day || "09:00",
    timezone: task.timezone || "Local",
    push_to_conversation: task.push_to_conversation,
    chat_id: task.chat_id ? String(task.chat_id) : "",
  };
};

const scheduledTaskFormToPayload = (form: ScheduledTaskFormState): TelegramAgentScheduledTaskPayload => ({
  name: form.name.trim(),
  prompt: form.prompt.trim(),
  enabled: form.enabled,
  schedule_type: form.schedule_type,
  interval_minutes: form.schedule_type === "interval" ? Math.max(Number.parseInt(form.interval_minutes, 10) || 0, 1) : 0,
  time_of_day: form.schedule_type === "daily" ? form.time_of_day.trim() : "",
  timezone: form.timezone.trim() || "Local",
  push_to_conversation: form.push_to_conversation,
  chat_id: form.chat_id.trim() ? Number.parseInt(form.chat_id.trim(), 10) || 0 : 0,
});

function ScheduledTaskRow({
  task,
  toggling,
  deleting,
  onEdit,
  onToggle,
  onDelete,
}: {
  task: TelegramAgentScheduledTask;
  toggling: boolean;
  deleting: boolean;
  onEdit: () => void;
  onToggle: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="grid gap-3 rounded-lg border bg-card px-3 py-3 shadow-sm md:grid-cols-[minmax(0,1.2fr)_minmax(9rem,0.65fr)_minmax(8rem,0.55fr)_auto] md:items-center">
      <div className="min-w-0">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <div className="truncate text-sm font-semibold">{task.name}</div>
          <Badge variant="outline" className={cn("rounded-full px-2 py-0.5", taskStatusClassName(task))}>
            {formatTaskStatus(task)}
          </Badge>
        </div>
        <div className="mt-1 truncate text-xs text-muted-foreground">{compactText(task.prompt, 120)}</div>
      </div>
      <div className="text-xs text-muted-foreground">
        <div className="font-medium text-foreground">{formatSchedule(task)}</div>
        <div className="mt-1">下次 {formatDateTime(task.next_run_at)}</div>
      </div>
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Send className={cn("size-4", task.push_to_conversation ? "text-emerald-600 dark:text-emerald-300" : "text-muted-foreground")} />
        <span>{task.push_to_conversation ? "推送到对话" : "仅记录结果"}</span>
      </div>
      <div className="flex items-center justify-end gap-2">
        <Switch checked={task.enabled} disabled={toggling || deleting || task.running} onCheckedChange={onToggle} />
        <Button type="button" variant="ghost" size="icon" className="size-8" onClick={onEdit} aria-label="编辑任务">
          <Pencil className="size-4" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 text-muted-foreground hover:text-destructive"
          disabled={deleting || task.running}
          onClick={onDelete}
          aria-label="删除任务"
        >
          <Trash2 className="size-4" />
        </Button>
      </div>
    </div>
  );
}

function ScheduledTaskDialog({
  open,
  editingTask,
  form,
  saving,
  onOpenChange,
  onChange,
  onSubmit,
}: {
  open: boolean;
  editingTask: TelegramAgentScheduledTask | null;
  form: ScheduledTaskFormState;
  saving: boolean;
  onOpenChange: (open: boolean) => void;
  onChange: (next: ScheduledTaskFormState) => void;
  onSubmit: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[86vh] w-[92vw] !max-w-3xl flex-col overflow-hidden p-0">
        <DialogHeader className="border-b px-5 py-4 pr-12">
          <DialogTitle className="flex items-center gap-2 text-base">
            <CalendarClock className="size-4" />
            {editingTask ? "编辑定时任务" : "新建定时任务"}
          </DialogTitle>
        </DialogHeader>
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
          <div className="grid gap-4 md:grid-cols-2">
            <label className="space-y-2 text-sm">
              <span className="font-medium">任务名称</span>
              <Input value={form.name} onChange={(event) => onChange({ ...form, name: event.target.value })} placeholder="每日状态总结" />
            </label>
            <label className="space-y-2 text-sm">
              <span className="font-medium">定时类型</span>
              <Select value={form.schedule_type} onValueChange={(value) => onChange({ ...form, schedule_type: value as TelegramAgentScheduleType })}>
                <SelectTrigger className="h-9 bg-background">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="interval">按间隔执行</SelectItem>
                  <SelectItem value="daily">每天固定时间</SelectItem>
                </SelectContent>
              </Select>
            </label>
          </div>

          <label className="space-y-2 text-sm">
            <span className="font-medium">任务内容</span>
            <Textarea
              className="min-h-32 resize-none"
              value={form.prompt}
              onChange={(event) => onChange({ ...form, prompt: event.target.value })}
              placeholder="检查最近错误日志并总结需要关注的问题"
            />
          </label>

          <div className="grid gap-4 md:grid-cols-2">
            {form.schedule_type === "interval" ? (
              <label className="space-y-2 text-sm">
                <span className="font-medium">间隔分钟</span>
                <Input
                  type="number"
                  min={1}
                  value={form.interval_minutes}
                  onChange={(event) => onChange({ ...form, interval_minutes: event.target.value })}
                />
              </label>
            ) : (
              <label className="space-y-2 text-sm">
                <span className="font-medium">每天时间</span>
                <Input
                  type="time"
                  value={form.time_of_day}
                  onChange={(event) => onChange({ ...form, time_of_day: event.target.value })}
                />
              </label>
            )}
            <label className="space-y-2 text-sm">
              <span className="font-medium">目标 Chat ID</span>
              <Input
                value={form.chat_id}
                onChange={(event) => onChange({ ...form, chat_id: event.target.value })}
                placeholder="留空使用 TG 默认 Chat"
              />
            </label>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            <label className="flex items-center justify-between gap-3 rounded-lg border bg-muted/30 px-3 py-3 text-sm">
              <span className="font-medium">启用任务</span>
              <Switch checked={form.enabled} onCheckedChange={(checked) => onChange({ ...form, enabled: checked })} />
            </label>
            <label className="flex items-center justify-between gap-3 rounded-lg border bg-muted/30 px-3 py-3 text-sm">
              <span className="font-medium">推送到 Agent 对话</span>
              <Switch checked={form.push_to_conversation} onCheckedChange={(checked) => onChange({ ...form, push_to_conversation: checked })} />
            </label>
          </div>
        </div>
        <DialogFooter className="border-t px-5 py-4">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            取消
          </Button>
          <Button type="button" onClick={onSubmit} disabled={saving}>
            {saving ? "保存中..." : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteScheduledTaskDialog({
  task,
  deleting,
  onOpenChange,
  onConfirm,
}: {
  task: TelegramAgentScheduledTask | null;
  deleting: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open={Boolean(task)} onOpenChange={onOpenChange}>
      <AlertDialogContent className="overflow-hidden p-0 sm:max-w-xl">
        <div className="border-b bg-rose-50/70 px-5 py-4 dark:bg-rose-500/10">
          <AlertDialogHeader className="text-left">
            <div className="flex items-start gap-3">
              <div className="flex size-10 shrink-0 items-center justify-center rounded-full border border-rose-300/60 bg-rose-100 text-rose-700 dark:border-rose-400/30 dark:bg-rose-500/15 dark:text-rose-300">
                <TriangleAlert className="size-5" />
              </div>
              <div className="min-w-0">
                <AlertDialogTitle className="text-base">删除这个定时任务？</AlertDialogTitle>
                <AlertDialogDescription className="mt-1">
                  删除后任务不会再执行，已有执行日志不会被自动清理。
                </AlertDialogDescription>
              </div>
            </div>
          </AlertDialogHeader>
        </div>

        {task ? (
          <div className="space-y-3 px-5 py-4">
            <div className="rounded-lg border bg-muted/30 px-4 py-3">
              <div className="truncate text-sm font-semibold text-foreground">{task.name}</div>
              <div className="mt-2 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
                <span>计划：{formatSchedule(task)}</span>
                <span>下次：{formatDateTime(task.next_run_at)}</span>
                <span>推送：{task.push_to_conversation ? "推送到 Agent 对话" : "仅记录结果"}</span>
                <span>状态：{formatTaskStatus(task)}</span>
              </div>
              {task.prompt.trim() ? (
                <div className="mt-3 rounded-md border bg-background/70 px-3 py-2 text-xs leading-relaxed text-muted-foreground">
                  {compactText(task.prompt, 180)}
                </div>
              ) : null}
            </div>
            <p className="text-xs text-muted-foreground">
              请确认这是你要移除的任务。此操作无法撤销。
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

export default function TelegramAgentSchedulesPage() {
  const [scheduledTasks, setScheduledTasks] = useState<TelegramAgentScheduledTask[]>([]);
  const [tasksLoading, setTasksLoading] = useState(true);
  const [taskDialogOpen, setTaskDialogOpen] = useState(false);
  const [editingTask, setEditingTask] = useState<TelegramAgentScheduledTask | null>(null);
  const [taskForm, setTaskForm] = useState<ScheduledTaskFormState>(() => ({ ...defaultScheduledTaskForm }));
  const [savingTask, setSavingTask] = useState(false);
  const [togglingTaskID, setTogglingTaskID] = useState<number | null>(null);
  const [deletingTaskID, setDeletingTaskID] = useState<number | null>(null);
  const [deleteTaskTarget, setDeleteTaskTarget] = useState<TelegramAgentScheduledTask | null>(null);

  const fetchScheduledTasks = useCallback(async (initial = false) => {
    if (initial) setTasksLoading(true);
    try {
      const data = await getTelegramAgentScheduledTasks();
      setScheduledTasks(data);
    } catch (error) {
      console.error("获取 TG Agent 定时任务失败", error);
      toast.error(error instanceof Error ? error.message : "获取 TG Agent 定时任务失败");
    } finally {
      if (initial) setTasksLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchScheduledTasks(true);
  }, [fetchScheduledTasks]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      void fetchScheduledTasks(false);
    }, 30_000);
    return () => window.clearInterval(timer);
  }, [fetchScheduledTasks]);

  const openCreateTaskDialog = useCallback(() => {
    setEditingTask(null);
    setTaskForm({ ...defaultScheduledTaskForm });
    setTaskDialogOpen(true);
  }, []);

  const openEditTaskDialog = useCallback((task: TelegramAgentScheduledTask) => {
    setEditingTask(task);
    setTaskForm(scheduledTaskToForm(task));
    setTaskDialogOpen(true);
  }, []);

  const handleSaveTask = useCallback(async () => {
    const payload = scheduledTaskFormToPayload(taskForm);
    if (!payload.name) {
      toast.error("任务名称不能为空");
      return;
    }
    if (!payload.prompt) {
      toast.error("任务内容不能为空");
      return;
    }
    if (payload.schedule_type === "daily" && !/^\d{2}:\d{2}$/.test(payload.time_of_day)) {
      toast.error("每天时间格式无效");
      return;
    }
    if (taskForm.chat_id.trim() && !/^-?\d+$/.test(taskForm.chat_id.trim())) {
      toast.error("目标 Chat ID 只能填写数字");
      return;
    }

    setSavingTask(true);
    try {
      const saved = editingTask
        ? await updateTelegramAgentScheduledTask(editingTask.id, payload)
        : await createTelegramAgentScheduledTask(payload);
      setScheduledTasks((previous) => {
        const exists = previous.some((item) => item.id === saved.id);
        if (!exists) return [saved, ...previous];
        return previous.map((item) => (item.id === saved.id ? saved : item));
      });
      setTaskDialogOpen(false);
      toast.success(editingTask ? "定时任务已更新" : "定时任务已创建");
      void fetchScheduledTasks(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存定时任务失败");
    } finally {
      setSavingTask(false);
    }
  }, [editingTask, fetchScheduledTasks, taskForm]);

  const handleToggleTask = useCallback(async (task: TelegramAgentScheduledTask) => {
    setTogglingTaskID(task.id);
    try {
      const updated = await updateTelegramAgentScheduledTaskStatus(task.id, !task.enabled);
      setScheduledTasks((previous) => previous.map((item) => (item.id === updated.id ? updated : item)));
      toast.success(updated.enabled ? "定时任务已启用" : "定时任务已停用");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新任务状态失败");
    } finally {
      setTogglingTaskID(null);
    }
  }, []);

  const handleDeleteTask = useCallback(async (task: TelegramAgentScheduledTask) => {
    setDeletingTaskID(task.id);
    try {
      await deleteTelegramAgentScheduledTask(task.id);
      setScheduledTasks((previous) => previous.filter((item) => item.id !== task.id));
      setDeleteTaskTarget(null);
      toast.success("定时任务已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除定时任务失败");
    } finally {
      setDeletingTaskID(null);
    }
  }, []);

  return (
    <div className="mx-auto flex h-full w-full max-w-7xl flex-col gap-5 overflow-hidden px-4 py-6 lg:px-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <CalendarClock className="size-4" />
            Agent 定时任务
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => void fetchScheduledTasks(false)}>
            <RefreshCw className="mr-2 size-4" />
            刷新
          </Button>
          <Button type="button" size="sm" onClick={openCreateTaskDialog}>
            <Plus className="mr-2 size-4" />
            新建任务
          </Button>
        </div>
      </div>

      <section className="min-h-0 flex-1 overflow-hidden rounded-lg border bg-background p-4 shadow-sm">
        <div className="h-full space-y-2 overflow-y-auto pr-1">
          {tasksLoading ? (
            <div className="flex min-h-[360px] items-center justify-center rounded-lg border border-dashed text-sm text-muted-foreground">
              <RefreshCw className="mr-2 size-4 animate-spin" />
              正在读取定时任务...
            </div>
          ) : scheduledTasks.length > 0 ? (
            scheduledTasks.map((task) => (
              <ScheduledTaskRow
                key={task.id}
                task={task}
                toggling={togglingTaskID === task.id}
                deleting={deletingTaskID === task.id}
                onEdit={() => openEditTaskDialog(task)}
                onToggle={() => void handleToggleTask(task)}
                onDelete={() => setDeleteTaskTarget(task)}
              />
            ))
          ) : (
            <div className="flex min-h-[360px] items-center justify-center rounded-lg border border-dashed text-sm text-muted-foreground">
              暂无 Agent 定时任务
            </div>
          )}
        </div>
      </section>

      <ScheduledTaskDialog
        open={taskDialogOpen}
        editingTask={editingTask}
        form={taskForm}
        saving={savingTask}
        onOpenChange={setTaskDialogOpen}
        onChange={setTaskForm}
        onSubmit={() => void handleSaveTask()}
      />
      <DeleteScheduledTaskDialog
        task={deleteTaskTarget}
        deleting={deleteTaskTarget ? deletingTaskID === deleteTaskTarget.id : false}
        onOpenChange={(open) => {
          if (!open && deletingTaskID === null) setDeleteTaskTarget(null);
        }}
        onConfirm={() => {
          if (deleteTaskTarget) void handleDeleteTask(deleteTaskTarget);
        }}
      />
    </div>
  );
}
