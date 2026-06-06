import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ChevronRight,
  FileCode2,
  FileText,
  Folder,
  FolderInput,
  RefreshCw,
  Save,
  Search,
  Sparkles,
  Trash2,
} from "lucide-react";
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
import { Card, CardContent } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import Loading from "@/components/loading";
import {
  configAPI,
  getModelOptions,
  skillAPI,
  type Model,
  type SkillFileContent,
  type SkillFileNode,
  type SkillItem,
  type TelegramAgentConfig,
} from "@/lib/api";
import { cn } from "@/lib/utils";

const AUTO_VECTOR_MODEL = "__local_vector__";

const defaultTelegramAgentConfig: TelegramAgentConfig = {
  enabled: true,
  model: "",
  system_prompt: "你是 Orvion 的 Telegram 对话助手。请用简体中文回答，保持简洁、准确、友好。",
  max_history_messages: 20,
  max_tokens: 2048,
  edit_interval_ms: 1200,
  tool_confirmation_required: true,
  skills_enabled: false,
  skills_embedding_model: "",
};

type SearchMode = "keyword" | "embedding";

export default function SkillsPage() {
  const [loading, setLoading] = useState(true);
  const [skills, setSkills] = useState<SkillItem[]>([]);
  const [query, setQuery] = useState("");
  const [searchMode, setSearchMode] = useState<SearchMode>("keyword");
  const [skillsEnabled, setSkillsEnabled] = useState(false);
  const [agentConfig, setAgentConfig] = useState<TelegramAgentConfig>(defaultTelegramAgentConfig);
  const [modelOptions, setModelOptions] = useState<Model[]>([]);
  const [savingConfig, setSavingConfig] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [importName, setImportName] = useState("");
  const [importOverwrite, setImportOverwrite] = useState(false);
  const [uploadFiles, setUploadFiles] = useState<File[]>([]);
  const [uploading, setUploading] = useState(false);
  const [editorOpen, setEditorOpen] = useState(false);
  const [activeSkill, setActiveSkill] = useState<SkillItem | null>(null);
  const [fileTree, setFileTree] = useState<SkillFileNode[]>([]);
  const [fileContent, setFileContent] = useState<SkillFileContent | null>(null);
  const [draftContent, setDraftContent] = useState("");
  const [treeLoading, setTreeLoading] = useState(false);
  const [fileLoading, setFileLoading] = useState(false);
  const [savingFile, setSavingFile] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<SkillItem | null>(null);
  const [deleting, setDeleting] = useState(false);
  const zipInputRef = useRef<HTMLInputElement | null>(null);

  const embeddingModelOptions = useMemo(() => {
    const filtered = modelOptions.filter((model) => (model.Capabilities ?? []).includes("embedding"));
    return filtered.length > 0 ? filtered : modelOptions;
  }, [modelOptions]);

  const loadConfig = useCallback(async () => {
    const response = await configAPI.getConfig("telegram_agent");
    const parsed = response.value ? JSON.parse(response.value) as Partial<TelegramAgentConfig> : {};
    const next = {
      ...defaultTelegramAgentConfig,
      ...parsed,
      skills_enabled: parsed.skills_enabled === true,
      skills_embedding_model: parsed.skills_embedding_model || "",
    };
    setAgentConfig(next);
    setSkillsEnabled(next.skills_enabled === true);
  }, []);

  const loadSkills = useCallback(async (nextQuery = "", nextMode: SearchMode = "keyword") => {
    const response = await skillAPI.list({ query: nextQuery.trim(), search_mode: nextMode });
    setSkills(response.skills ?? []);
    setSkillsEnabled(response.skills_enabled === true);
  }, []);

  const loadPage = useCallback(async () => {
    try {
      setLoading(true);
      await Promise.all([loadConfig(), getModelOptions().then(setModelOptions)]);
      await loadSkills();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载 Skills 失败");
    } finally {
      setLoading(false);
    }
  }, [loadConfig, loadSkills]);

  useEffect(() => {
    void loadPage();
  }, [loadPage]);

  const handleSearch = async () => {
    try {
      await loadSkills(query, searchMode);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "检索 Skills 失败");
    }
  };

  const handleReload = async () => {
    try {
      setReloading(true);
      const response = await skillAPI.reload({ query: query.trim(), search_mode: searchMode });
      setSkills(response.skills ?? []);
      setSkillsEnabled(response.skills_enabled === true);
      toast.success(response.message || "Skills 已热重载");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "热重载 Skills 失败");
    } finally {
      setReloading(false);
    }
  };

  const handleSaveConfig = async () => {
    const nextConfig: TelegramAgentConfig = {
      ...agentConfig,
      skills_enabled: skillsEnabled,
      skills_embedding_model: (agentConfig.skills_embedding_model || "").trim(),
    };
    try {
      setSavingConfig(true);
      await configAPI.updateConfig("telegram_agent", nextConfig);
      setAgentConfig(nextConfig);
      toast.success("Skills 配置已保存");
      await loadSkills(query, searchMode);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存 Skills 配置失败");
    } finally {
      setSavingConfig(false);
    }
  };

  const handleToggleSkill = async (skill: SkillItem, enabled: boolean) => {
    try {
      const updated = await skillAPI.updateStatus(skill.name, enabled);
      setSkills((items) => items.map((item) => item.name === updated.name ? updated : item));
      setActiveSkill((current) => current?.name === updated.name ? updated : current);
      toast.success(`${updated.name} 已${updated.enabled ? "启用" : "禁用"}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新 Skill 状态失败");
    }
  };

  const loadSkillFile = useCallback(async (skillName: string, path: string) => {
    try {
      setFileLoading(true);
      const content = await skillAPI.fileContent(skillName, path);
      setFileContent(content);
      setDraftContent(content.content ?? "");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取 Skill 文件失败");
    } finally {
      setFileLoading(false);
    }
  }, []);

  const openSkillEditor = async (skill: SkillItem) => {
    setActiveSkill(skill);
    setEditorOpen(true);
    setFileTree([]);
    setFileContent(null);
    setDraftContent("");
    try {
      setTreeLoading(true);
      const response = await skillAPI.files(skill.name);
      const firstFile = findFirstSkillFile(response.files);
      setActiveSkill(response.skill);
      setFileTree(response.files);
      if (firstFile) {
        await loadSkillFile(response.skill.name, firstFile.path);
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载 Skill 文件树失败");
    } finally {
      setTreeLoading(false);
    }
  };

  const handleEditorOpenChange = (open: boolean) => {
    setEditorOpen(open);
    if (!open) {
      setActiveSkill(null);
      setFileTree([]);
      setFileContent(null);
      setDraftContent("");
      setTreeLoading(false);
      setFileLoading(false);
      setSavingFile(false);
    }
  };

  const handleSaveSkillFile = async () => {
    if (!activeSkill || !fileContent || !fileContent.editable) {
      return;
    }
    try {
      setSavingFile(true);
      const updated = await skillAPI.saveFile(activeSkill.name, {
        path: fileContent.path,
        content: draftContent,
      });
      setFileContent(updated);
      setDraftContent(updated.content ?? "");
      toast.success("Skill 文件已保存");
      await loadSkills(query, searchMode);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存 Skill 文件失败");
    } finally {
      setSavingFile(false);
    }
  };

  const handleDeleteSkill = async () => {
    if (!deleteTarget) {
      return;
    }
    try {
      setDeleting(true);
      const result = await skillAPI.delete(deleteTarget.name);
      toast.success(result.message || `已移除 ${deleteTarget.name}`);
      if (activeSkill?.name === deleteTarget.name) {
        handleEditorOpenChange(false);
      }
      setDeleteTarget(null);
      await loadSkills(query, searchMode);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "移除 Skill 失败");
    } finally {
      setDeleting(false);
    }
  };

  const handleUploadSkill = async () => {
    if (uploadFiles.length === 0) {
      toast.error("请先选择 ZIP 压缩包");
      return;
    }
    try {
      setUploading(true);
      const imported = await skillAPI.upload({
        files: uploadFiles,
        name: importName.trim() || undefined,
        overwrite: importOverwrite,
      });
      toast.success(`已上传并导入 ${imported.name}`);
      setUploadFiles([]);
      setImportName("");
      setUploadOpen(false);
      await loadSkills(query, searchMode);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "上传 Skill 失败");
    } finally {
      setUploading(false);
      if (zipInputRef.current) zipInputRef.current.value = "";
    }
  };

  if (loading) {
    return <Loading message="加载 Skills" className="min-h-[calc(100vh-12rem)]" />;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <Sparkles className="size-5 text-emerald-600" />
            <h2 className="text-2xl font-semibold tracking-tight">Skills 管理</h2>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">管理本地能力包、脚本执行和 Agent 语义检索。</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" className="h-9 gap-2" onClick={handleReload} disabled={reloading}>
            <RefreshCw className={cn("size-4", reloading && "animate-spin")} />
            热重载
          </Button>
          <Button className="h-9 gap-2" onClick={() => setUploadOpen(true)}>
            <FolderInput className="size-4" />
            导入
          </Button>
        </div>
      </div>

      <Card className="rounded-2xl border border-border/60 bg-card/90">
        <CardContent className="grid grid-cols-1 gap-3 p-4 lg:grid-cols-[minmax(9rem,0.55fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
          <div className="flex h-9 items-center justify-between rounded-lg border border-border/60 bg-muted/50 px-3">
            <Label className="text-xs text-muted-foreground">启用 Skills</Label>
            <Switch checked={skillsEnabled} onCheckedChange={setSkillsEnabled} />
          </div>
          <Select
            value={agentConfig.skills_embedding_model || AUTO_VECTOR_MODEL}
            onValueChange={(value) => setAgentConfig((current) => ({
              ...current,
              skills_embedding_model: value === AUTO_VECTOR_MODEL ? "" : value,
            }))}
          >
            <SelectTrigger className="h-9 bg-background">
              <SelectValue placeholder="选择向量模型" />
            </SelectTrigger>
            <SelectContent className="max-h-72">
              <SelectItem value={AUTO_VECTOR_MODEL}>本地向量检索</SelectItem>
              {embeddingModelOptions.map((model) => (
                <SelectItem key={`skill-vector-${model.ID}-${model.Name}`} value={model.Name}>
                  {model.Name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex min-w-0 gap-2">
            <Input
              className="h-9"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void handleSearch();
              }}
              placeholder="搜索 Skill"
            />
            <Select value={searchMode} onValueChange={(value) => setSearchMode(value as SearchMode)}>
              <SelectTrigger className="h-9 w-[7.5rem] shrink-0 bg-background">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="keyword">关键词</SelectItem>
                <SelectItem value="embedding">Embedding</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" className="h-9 gap-2" onClick={handleSearch}>
              <Search className="size-4" />
              检索
            </Button>
            <Button className="h-9 gap-2" onClick={handleSaveConfig} disabled={savingConfig}>
              <Save className="size-4" />
              保存
            </Button>
          </div>
        </CardContent>
      </Card>

      {skills.length === 0 ? (
        <Card className="rounded-2xl border border-dashed border-border/70 bg-card/70">
          <CardContent className="flex h-40 items-center justify-center text-sm text-muted-foreground">
            暂无匹配 Skill
          </CardContent>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {skills.map((skill) => (
            <Card
              key={`${skill.dir}-${skill.name}`}
              onClick={() => void openSkillEditor(skill)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  void openSkillEditor(skill);
                }
              }}
              role="button"
              tabIndex={0}
              className="group cursor-pointer rounded-2xl border border-border/60 bg-card/90 outline-none transition hover:-translate-y-0.5 hover:border-emerald-400/70 hover:shadow-sm focus-visible:ring-2 focus-visible:ring-ring"
            >
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex min-w-0 items-start gap-3">
                    <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-600">
                      <FileCode2 className="size-4" />
                    </div>
                    <div className="min-w-0">
                      <div className="truncate text-sm font-semibold" title={skill.name}>{skill.name}</div>
                      <p className="mt-1 line-clamp-2 min-h-8 text-xs leading-4 text-muted-foreground">
                        {skill.description || "暂无描述"}
                      </p>
                    </div>
                  </div>
                  <Switch
                    checked={skill.enabled}
                    onClick={(event) => event.stopPropagation()}
                    onCheckedChange={(checked) => void handleToggleSkill(skill, checked)}
                  />
                </div>
                <div className="mt-4 flex flex-wrap items-center gap-1.5">
                  <Badge variant={skill.enabled ? "default" : "secondary"} className="rounded-md">
                    {skill.enabled ? "启用" : "禁用"}
                  </Badge>
                  <Badge variant="outline" className="rounded-md">{skill.scripts.length} 个脚本</Badge>
                  {skill.score ? <Badge variant="outline" className="rounded-md">相似度 {skill.score.toFixed(3)}</Badge> : null}
                  {skill.triggers.slice(0, 2).map((trigger) => (
                    <Badge key={trigger} variant="secondary" className="rounded-md">{trigger}</Badge>
                  ))}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Dialog open={editorOpen} onOpenChange={handleEditorOpenChange}>
        <DialogContent className="flex h-[86vh] w-[94vw] !max-w-6xl flex-col overflow-hidden p-0">
          <DialogHeader className="border-b border-border/60 px-5 py-4 pr-12">
            <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
              <div className="min-w-0">
                <DialogTitle className="truncate text-lg">{activeSkill?.name || "Skill 编辑"}</DialogTitle>
                <div className="mt-2 truncate font-mono text-xs text-muted-foreground">{activeSkill?.dir || ""}</div>
              </div>
              {activeSkill ? (
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={activeSkill.enabled ? "default" : "secondary"} className="rounded-md">
                    {activeSkill.enabled ? "启用" : "禁用"}
                  </Badge>
                  <Badge variant="outline" className="rounded-md">{activeSkill.scripts.length} 个脚本</Badge>
                  <Badge variant="outline" className="rounded-md">{activeSkill.triggers.length} 个触发词</Badge>
                </div>
              ) : null}
            </div>
          </DialogHeader>

          <div className="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[18rem_minmax(0,1fr)]">
            <aside className="flex min-h-0 flex-col border-b border-border/60 bg-muted/20 lg:border-b-0 lg:border-r">
              <div className="border-b border-border/60 px-4 py-3">
                <div className="text-sm font-semibold">目录内容</div>
                <div className="mt-1 truncate text-xs text-muted-foreground">
                  {fileTree.length} 个顶层项目
                </div>
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto p-2">
                {treeLoading ? (
                  <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
                    加载文件树...
                  </div>
                ) : fileTree.length === 0 ? (
                  <div className="rounded-lg border border-dashed border-border/70 p-4 text-sm text-muted-foreground">
                    暂无文件
                  </div>
                ) : (
                  <SkillFileTree
                    nodes={fileTree}
                    activePath={fileContent?.path ?? ""}
                    onSelect={(path) => {
                      if (activeSkill) void loadSkillFile(activeSkill.name, path);
                    }}
                  />
                )}
              </div>
            </aside>

            <section className="flex min-h-0 flex-col">
              <div className="flex flex-col gap-2 border-b border-border/60 px-4 py-3 md:flex-row md:items-center md:justify-between">
                <div className="min-w-0">
                  <div className="truncate font-mono text-sm">
                    {fileContent?.path || "未选择文件"}
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {fileContent
                      ? fileContent.editable
                        ? `${formatBytes(fileContent.size)} ｜ UTF-8 文本`
                        : `${formatBytes(fileContent.size)} ｜ 不可在线编辑`
                      : "从左侧选择文件后编辑"}
                  </div>
                </div>
                <Button
                  className="h-8 gap-2"
                  onClick={() => void handleSaveSkillFile()}
                  disabled={!fileContent?.editable || savingFile || draftContent === (fileContent?.content ?? "")}
                >
                  <Save className="size-4" />
                  {savingFile ? "保存中..." : "保存"}
                </Button>
              </div>
              <div className="min-h-0 flex-1 p-4">
                {fileLoading ? (
                  <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                    读取文件...
                  </div>
                ) : fileContent ? (
                  fileContent.editable ? (
                    <Textarea
                      className="h-full min-h-[22rem] resize-none border-border/60 bg-background font-mono text-xs leading-5"
                      value={draftContent}
                      onChange={(event) => setDraftContent(event.target.value)}
                      spellCheck={false}
                    />
                  ) : (
                    <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-border/70 text-sm text-muted-foreground">
                      当前文件不是 UTF-8 文本，不能在线编辑
                    </div>
                  )
                ) : (
                  <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-border/70 text-sm text-muted-foreground">
                    请选择一个文件
                  </div>
                )}
              </div>
            </section>
          </div>

          <div className="flex flex-col gap-2 border-t border-border/60 px-5 py-3 md:flex-row md:items-center md:justify-between">
            <Button
              type="button"
              variant="destructive"
              className="h-9 gap-2"
              onClick={() => activeSkill && setDeleteTarget(activeSkill)}
              disabled={!activeSkill}
            >
              <Trash2 className="size-4" />
              移除 Skill
            </Button>
            <Button type="button" variant="outline" className="h-9" onClick={() => handleEditorOpenChange(false)}>
              关闭
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={uploadOpen} onOpenChange={setUploadOpen}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>上传 Skill</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <div className="rounded-lg border border-border/60 bg-muted/30 p-3">
              <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
                <div>
                  <div className="text-sm font-semibold">上传 Skill</div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    支持上传单个 ZIP 压缩包。
                  </div>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button type="button" variant="outline" className="h-8" onClick={() => zipInputRef.current?.click()}>
                    选择 ZIP
                  </Button>
                  <Button type="button" className="h-8" onClick={() => void handleUploadSkill()} disabled={uploading || uploadFiles.length === 0}>
                    {uploading ? "上传中..." : "上传导入"}
                  </Button>
                </div>
              </div>
              <input
                ref={zipInputRef}
                type="file"
                accept=".zip,application/zip,application/x-zip-compressed"
                className="hidden"
                onChange={(event) => setUploadFiles(Array.from(event.target.files ?? []).slice(0, 1))}
              />
              <div className="mt-3 rounded-md bg-background/70 px-3 py-2 text-xs text-muted-foreground">
                {uploadFiles.length > 0
                  ? `已选择 ${uploadFiles.length} 个文件：${uploadFiles[0]?.name ?? ""}${uploadFiles.length > 1 ? " ..." : ""}`
                  : "尚未选择上传文件"}
              </div>
              <div className="mt-3 grid gap-3 md:grid-cols-[1fr_auto] md:items-end">
                <div className="space-y-1">
                  <Label className="text-xs text-muted-foreground">导入名称</Label>
                  <Input value={importName} onChange={(event) => setImportName(event.target.value)} placeholder="留空则使用压缩包内 Skill 名称" />
                </div>
                <div className="flex h-9 items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3">
                  <Label className="text-xs text-muted-foreground">覆盖同名 Skill</Label>
                  <Switch checked={importOverwrite} onCheckedChange={setImportOverwrite} />
                </div>
              </div>
            </div>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setUploadOpen(false)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={Boolean(deleteTarget)} onOpenChange={(open) => { if (!open && !deleting) setDeleteTarget(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确定要移除这个 Skill 吗？</AlertDialogTitle>
            <AlertDialogDescription>
              此操作会删除「{deleteTarget?.name ?? ""}」对应的本地 Skill 目录，删除后无法在页面内恢复。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>取消</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              disabled={deleting}
              onClick={() => void handleDeleteSkill()}
            >
              {deleting ? "移除中..." : "确认移除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function SkillFileTree({
  nodes,
  activePath,
  onSelect,
  depth = 0,
}: {
  nodes: SkillFileNode[];
  activePath: string;
  onSelect: (path: string) => void;
  depth?: number;
}) {
  return (
    <div className={cn(depth > 0 && "ml-3 border-l border-border/50 pl-2")}>
      {nodes.map((node) => {
        const isDirectory = node.kind === "directory";
        const isActive = activePath === node.path;
        return (
          <div key={`${node.kind}-${node.path}`} className="space-y-1">
            <button
              type="button"
              className={cn(
                "flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-xs outline-none transition hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring",
                isActive && "bg-emerald-500/10 text-emerald-700",
                isDirectory && "cursor-default hover:bg-transparent",
              )}
              onClick={() => {
                if (!isDirectory) onSelect(node.path);
              }}
              aria-disabled={isDirectory}
              title={node.path}
            >
              {isDirectory ? (
                <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
              ) : (
                <span className="w-3 shrink-0" />
              )}
              {isDirectory ? (
                <Folder className="size-4 shrink-0 text-amber-500" />
              ) : (
                <FileText className="size-4 shrink-0 text-emerald-600" />
              )}
              <span className="min-w-0 flex-1 truncate font-mono">{node.name}</span>
            </button>
            {isDirectory && node.children.length > 0 ? (
              <SkillFileTree nodes={node.children} activePath={activePath} onSelect={onSelect} depth={depth + 1} />
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

function findFirstSkillFile(nodes: SkillFileNode[]): SkillFileNode | null {
  for (const node of nodes) {
    if (node.kind === "file" && ["skill.md", "skills.md"].includes(node.name.toLowerCase())) {
      return node;
    }
  }
  for (const node of nodes) {
    if (node.kind === "file") {
      return node;
    }
  }
  for (const node of nodes) {
    const child = findFirstSkillFile(node.children);
    if (child) {
      return child;
    }
  }
  return null;
}

function formatBytes(size: number): string {
  if (!Number.isFinite(size) || size <= 0) {
    return "0 B";
  }
  if (size < 1024) {
    return `${size} B`;
  }
  if (size < 1024 * 1024) {
    return `${(size / 1024).toFixed(1)} KB`;
  }
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}
