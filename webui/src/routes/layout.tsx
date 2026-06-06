import { useEffect, useRef, useState, type CSSProperties } from "react";
import { Link, Outlet, useLocation, useNavigate } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn, openExternalUrl } from "@/lib/utils";
import iconSvg from "@/assets/icon.svg";
import {
  Activity,
  Bot,
  Boxes,
  ChevronsUpDown,
  Cloud,
  FileTerminal,
  House,
  KeyRound,
  LogOut,
  MessageSquareText,
  PanelLeft,
  ScrollText,
  Settings2,
  Sparkles,
  UserCircle2,
  X,
} from "lucide-react";
import { checkVersionUpdate, configAPI, getVersion, type TelegramAgentConfig, type VersionUpdateCheck } from "@/lib/api";
import { clearStoredAuthToken, getStoredAuthTokenMode } from "@/lib/auth";

const SIDEBAR_STORAGE_KEY = "orvion_sidebar_collapsed";
const UI_FONT_STORAGE_KEY = "orvion_ui_font";
const TELEGRAM_AGENT_CONFIG_CHANGED_EVENT = "telegram-agent-config-changed";
type UIFontOption = "default" | "kunming_seagull" | "fenyuan" | "lxgw_wenkai";
const UI_FONT_CLASS_MAP: Record<UIFontOption, string> = {
  default: "main-content-font-default",
  kunming_seagull: "main-content-font-kunming",
  fenyuan: "main-content-font-fenyuan",
  lxgw_wenkai: "main-content-font-lxgw-wenkai",
};
const DESKTOP_SIDEBAR_WIDTH = "14rem";
const DESKTOP_SIDEBAR_COLLAPSED_WIDTH = "4rem";
const SIDEBAR_GROUP_PADDING = "p-1.5";
const SIDEBAR_BUTTON_BASE =
  "flex h-10 w-full items-center gap-2 overflow-hidden rounded-[18px] px-2.5 py-2 text-left text-[13px] outline-none ring-sidebar-ring transition-all duration-200 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2";

const navSections = [
  {
    title: "仪表盘",
    items: [
      { to: "/", label: "仪表板", icon: House },
      { to: "/health-ui", label: "健康监控", icon: Activity },
    ],
  },
  {
    title: "管理",
    items: [
      { to: "/providers", label: "提供商", icon: Cloud },
      { to: "/models", label: "模型", icon: Boxes },
      { to: "/auth-keys", label: "API 密钥", icon: KeyRound },
      { to: "/skills", label: "Skills", icon: Sparkles },
      { to: "/tg-agent", label: "TG Agent", icon: Bot },
    ],
  },
  {
    title: "项目",
    items: [
      { to: "/model-chat", label: "测试场", icon: MessageSquareText },
      { to: "/logs", label: "请求日志", icon: ScrollText },
      { to: "/system-logs", label: "系统状态", icon: FileTerminal },
    ],
  },
  {
    title: "设置",
    items: [{ to: "/config", label: "系统配置", icon: Settings2 }],
  },
] as const;

const authKeyNavSections = [
  {
    title: "概览",
    items: [
      { to: "/", label: "API 概览", icon: House },
      { to: "/logs", label: "请求日志", icon: ScrollText },
    ],
  },
] as const;

function getInitialSidebarCollapsed() {
  if (typeof window === "undefined") {
    return false;
  }
  return window.localStorage.getItem(SIDEBAR_STORAGE_KEY) === "1";
}

function isUIFontOption(value: string | null | undefined): value is UIFontOption {
  return value === "default" || value === "kunming_seagull" || value === "fenyuan" || value === "lxgw_wenkai";
}

function getInitialUIFont(): UIFontOption {
  if (typeof window === "undefined") {
    return "default";
  }
  const value = window.localStorage.getItem(UI_FONT_STORAGE_KEY);
  if (isUIFontOption(value)) {
    return value;
  }
  return "default";
}

export default function Layout() {
  const [version, setVersion] = useState("dev");
  const [versionUpdate, setVersionUpdate] = useState<VersionUpdateCheck | null>(null);
  const [updatePopoverOpen, setUpdatePopoverOpen] = useState(false);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(getInitialSidebarCollapsed);
  const [uiFont, setUIFont] = useState<UIFontOption>(getInitialUIFont);
  const [telegramAgentEnabled, setTelegramAgentEnabled] = useState(true);
  const [navigationProgressVisible, setNavigationProgressVisible] = useState(false);
  const [navigationProgressValue, setNavigationProgressValue] = useState(0);
  const navigate = useNavigate();
  const location = useLocation();
  const effectiveUIFont: UIFontOption = uiFont;
  const layoutFontClass = UI_FONT_CLASS_MAP[effectiveUIFont];
  const isAuthKeyToken = getStoredAuthTokenMode() === "auth_key";
  const progressTimersRef = useRef<number[]>([]);
  const progressStartedRef = useRef(false);

  useEffect(() => {
    if (isAuthKeyToken) {
      return undefined;
    }

    let active = true;

    const fetchVersionAndUpdate = async () => {
      const fetchCurrentVersion = async () => {
        try {
          const value = await getVersion();
          if (active && value) {
            setVersion(value);
          }
        } catch {
          // 保持默认版本号。
        }
      };

      try {
        const config = await configAPI.getConfig("github_version_check");
        if (!active) {
          return;
        }
        if (config.value) {
          const parsed = JSON.parse(config.value) as { enabled?: boolean };
          if (parsed.enabled === false) {
            setVersionUpdate(null);
            setUpdatePopoverOpen(false);
            await fetchCurrentVersion();
            return;
          }
        }

        const info = await checkVersionUpdate();
        if (!active) {
          return;
        }
        if (info.currentVersion) {
          setVersion(info.currentVersion);
        }
        setVersionUpdate(info.hasUpdate ? info : null);
      } catch (error) {
        console.error("检查版本更新失败:", error);
        setVersionUpdate(null);
        setUpdatePopoverOpen(false);
        await fetchCurrentVersion();
      }
    };

    void fetchVersionAndUpdate();

    return () => {
      active = false;
    };
  }, [isAuthKeyToken]);

  useEffect(() => {
    if (!versionUpdate?.hasUpdate) {
      setUpdatePopoverOpen(false);
    }
  }, [versionUpdate?.hasUpdate]);

  useEffect(() => {
    if (isAuthKeyToken) {
      return;
    }
    window.localStorage.setItem(SIDEBAR_STORAGE_KEY, sidebarCollapsed ? "1" : "0");
  }, [isAuthKeyToken, sidebarCollapsed]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(UI_FONT_STORAGE_KEY, uiFont);
  }, [uiFont]);

  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }
    document.documentElement.dataset.uiFont = effectiveUIFont;
  }, [effectiveUIFont]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return undefined;
    }
    const handleFontChanged = (event: Event) => {
      const customEvent = event as CustomEvent<{ font?: UIFontOption }>;
      const next = customEvent.detail?.font;
      if (isUIFontOption(next)) {
        setUIFont(next);
      }
    };
    window.addEventListener("ui-font-changed", handleFontChanged as EventListener);
    return () => window.removeEventListener("ui-font-changed", handleFontChanged as EventListener);
  }, []);

  useEffect(() => {
    if (isAuthKeyToken) {
      return;
    }
    let active = true;
    const fetchUIFont = async () => {
      try {
        const response = await configAPI.getConfig("ui_font");
        if (!response.value || !active) {
          return;
        }
        const parsed = JSON.parse(response.value) as { font?: string };
        if (isUIFontOption(parsed.font)) {
          setUIFont(parsed.font);
        }
      } catch {
        // 保持本地配置兜底
      }
    };
    void fetchUIFont();
    return () => {
      active = false;
    };
  }, [isAuthKeyToken]);

  useEffect(() => {
    if (isAuthKeyToken) {
      return undefined;
    }

    let active = true;
    const fetchTelegramAgentConfig = async () => {
      try {
        const response = await configAPI.getConfig("telegram_agent");
        if (!active) {
          return;
        }
        const parsed = response.value ? JSON.parse(response.value) as Partial<TelegramAgentConfig> : {};
        setTelegramAgentEnabled(parsed.enabled !== false);
      } catch {
        if (active) {
          setTelegramAgentEnabled(true);
        }
      }
    };

    const handleTelegramAgentConfigChanged = (event: Event) => {
      const customEvent = event as CustomEvent<{ enabled?: boolean }>;
      setTelegramAgentEnabled(customEvent.detail?.enabled !== false);
    };

    void fetchTelegramAgentConfig();
    window.addEventListener(TELEGRAM_AGENT_CONFIG_CHANGED_EVENT, handleTelegramAgentConfigChanged as EventListener);
    return () => {
      active = false;
      window.removeEventListener(TELEGRAM_AGENT_CONFIG_CHANGED_EVENT, handleTelegramAgentConfigChanged as EventListener);
    };
  }, [isAuthKeyToken]);

  useEffect(() => {
    if (isAuthKeyToken) {
      return undefined;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() === "b" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        if (window.innerWidth >= 768) {
          setSidebarCollapsed((value) => !value);
        } else {
          setMobileSidebarOpen((value) => !value);
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isAuthKeyToken]);

  useEffect(() => {
    setMobileSidebarOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!progressStartedRef.current) {
      return;
    }

    progressTimersRef.current.forEach((timer) => window.clearTimeout(timer));
    progressTimersRef.current = [];
    setNavigationProgressValue(100);

    const completeTimer = window.setTimeout(() => {
      setNavigationProgressVisible(false);
      setNavigationProgressValue(0);
      progressStartedRef.current = false;
    }, 260);

    progressTimersRef.current.push(completeTimer);

    return () => {
      window.clearTimeout(completeTimer);
    };
  }, [location.pathname]);

  useEffect(() => {
    return () => {
      progressTimersRef.current.forEach((timer) => window.clearTimeout(timer));
      progressTimersRef.current = [];
    };
  }, []);

  useEffect(() => {
    if (!mobileSidebarOpen) {
      return undefined;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setMobileSidebarOpen(false);
      }
    };

    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    window.addEventListener("keydown", handleKeyDown);

    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      document.body.style.overflow = prevOverflow;
    };
  }, [mobileSidebarOpen]);

  const handleLogout = () => {
    clearStoredAuthToken();
    navigate("/login");
  };

  const handleToggleSidebar = () => {
    if (window.innerWidth >= 768) {
      setSidebarCollapsed((value) => !value);
      return;
    }
    setMobileSidebarOpen((value) => !value);
  };

  const getIsActive = (to: string) => (
    to === "/" ? location.pathname === "/" : location.pathname === to || location.pathname.startsWith(`${to}/`)
  );

  const startNavigationProgress = () => {
    progressTimersRef.current.forEach((timer) => window.clearTimeout(timer));
    progressTimersRef.current = [];
    progressStartedRef.current = true;
    setNavigationProgressVisible(true);
    setNavigationProgressValue(12);

    const timers = [
      window.setTimeout(() => setNavigationProgressValue(46), 40),
      window.setTimeout(() => setNavigationProgressValue(72), 180),
      window.setTimeout(() => setNavigationProgressValue(86), 420),
    ];
    progressTimersRef.current = timers;
  };

  const renderNavGroups = (mobile = false) => {
    const collapsed = !mobile && sidebarCollapsed;
    const baseSections = isAuthKeyToken ? authKeyNavSections : navSections;
    const sections = baseSections
      .map((section) => ({
        ...section,
        items: isAuthKeyToken || telegramAgentEnabled
          ? section.items
          : section.items.filter((item) => item.to !== "/skills" && item.to !== "/tg-agent"),
      }))
      .filter((section) => section.items.length > 0);

    return sections.map((section) => (
      <div key={section.title} className={SIDEBAR_GROUP_PADDING}>
        <div
          className={cn(
            "flex h-7 items-center px-2 text-[11px] font-medium text-sidebar-foreground/70 transition-all duration-200",
            collapsed && "-mt-8 opacity-0 pointer-events-none"
          )}
        >
          {section.title}
        </div>
        <ul className="flex w-full min-w-0 flex-col gap-0.5">
          {section.items.map((item) => {
            const Icon = item.icon;
            const isActive = getIsActive(item.to);
            return (
              <li key={item.to} className="group/menu-item relative">
                <Link
                  to={item.to}
                  onClick={() => {
                    if (!isActive) {
                      startNavigationProgress();
                    }
                    if (mobile) {
                      setMobileSidebarOpen(false);
                    }
                  }}
                  title={collapsed ? item.label : undefined}
                  className={cn(
                    SIDEBAR_BUTTON_BASE,
                    isActive && "bg-primary font-medium text-primary-foreground",
                    collapsed && "mx-auto h-9 w-9 justify-center rounded-full p-0"
                  )}
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  <span className={cn("truncate", collapsed && "hidden")}>{item.label}</span>
                </Link>
              </li>
            );
          })}
        </ul>
      </div>
    ));
  };

  const renderUserMenu = (mobile = false) => {
    const collapsed = !mobile && sidebarCollapsed;

    return (
      <Popover>
        <PopoverTrigger asChild>
          <button
            type="button"
            className={cn(
              "flex h-10 w-full items-center gap-2 rounded-[18px] px-2.5 py-2 text-left text-[13px] outline-none transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring",
              collapsed && "mx-auto h-9 w-9 justify-center rounded-full p-0"
            )}
            title={collapsed ? "控制台用户" : undefined}
          >
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/12 text-primary">
              <UserCircle2 className="h-4 w-4" />
            </div>
            <div className={cn("grid flex-1 text-left text-[13px] leading-tight", collapsed && "hidden")}>
              <span className="truncate font-semibold">控制台用户</span>
              <span className="truncate text-[11px] text-sidebar-foreground/70">当前会话</span>
            </div>
            <ChevronsUpDown className={cn("ml-auto h-4 w-4 shrink-0", collapsed && "hidden")} />
          </button>
        </PopoverTrigger>
        <PopoverContent
          side={mobile ? "bottom" : "right"}
          align="end"
          sideOffset={8}
          className="w-56 rounded-lg border-border/70 bg-popover/98 p-1"
        >
          <div className="flex items-center gap-3 rounded-md px-3 py-2 text-left text-sm">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/12 text-primary">
              <UserCircle2 className="h-5 w-5" />
            </div>
            <div className="grid flex-1 leading-tight">
              <span className="truncate font-semibold">控制台用户</span>
              <span className="truncate text-xs text-muted-foreground">当前会话</span>
            </div>
          </div>
          <div className="my-1 h-px bg-border/70" />
          {!isAuthKeyToken ? (
            <Link
              to="/config"
              className="flex items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors hover:bg-accent hover:text-accent-foreground"
            >
              <Settings2 className="h-4 w-4" />
              系统配置
            </Link>
          ) : null}
          <button
            type="button"
            onClick={handleLogout}
            className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            <LogOut className="h-4 w-4" />
            退出登录
          </button>
        </PopoverContent>
      </Popover>
    );
  };

  const renderDesktopSidebar = () => {
    return (
      <aside
        className={cn(
          "fixed top-14 bottom-0 left-0 z-40 hidden overflow-hidden transition-[width] duration-200 ease-linear md:block",
          sidebarCollapsed ? "w-16" : "w-56"
        )}
      >
        <div className="h-full pr-2 pb-0 pl-0">
          <div className="flex h-full w-full flex-col rounded-tr-[24px] rounded-br-none rounded-l-none border border-l-0 border-sidebar-border/70 bg-sidebar text-sidebar-foreground shadow-sm">
            <div className="relative flex min-h-0 flex-1 flex-col pt-2">
              <div className={cn("flex min-h-0 flex-1 flex-col gap-1 overflow-auto", sidebarCollapsed && "pt-3")}>
                {renderNavGroups()}
              </div>
              <div className="pointer-events-none absolute inset-x-0 bottom-0 h-8 bg-gradient-to-t from-sidebar to-transparent" />
            </div>
            <div className="flex flex-col gap-1 p-1.5">{renderUserMenu()}</div>
          </div>
        </div>
      </aside>
    );
  };

  const renderMobileSidebar = () => {
    return (
      <div
        className={cn(
          "fixed inset-0 z-50 md:hidden",
          mobileSidebarOpen ? "pointer-events-auto" : "pointer-events-none"
        )}
      >
        <button
          type="button"
          aria-label="关闭侧边导航"
          className={cn(
            "absolute inset-0 bg-black/35 transition-opacity",
            mobileSidebarOpen ? "opacity-100" : "opacity-0"
          )}
          onClick={() => setMobileSidebarOpen(false)}
        />
        <aside
          className={cn(
            "relative h-full w-64 max-w-[88vw] transition-transform duration-200",
            mobileSidebarOpen ? "translate-x-0" : "-translate-x-full"
          )}
        >
          <div className="flex h-full w-full flex-col bg-sidebar text-sidebar-foreground shadow-xl">
            <div className="flex items-center justify-between px-4 py-3 border-b border-sidebar-border/70">
              <div className="flex items-center gap-2">
              <div className="flex size-10 items-center justify-center overflow-hidden rounded bg-transparent">
                <img src={iconSvg} alt="Orvion" className="size-10 object-cover" />
              </div>
                <span className="text-sm font-semibold">Orvion</span>
              </div>
              <Button
                variant="ghost"
                size="icon"
                className="size-8 rounded-md"
                onClick={() => setMobileSidebarOpen(false)}
                aria-label="关闭侧边导航"
              >
                <X className="h-4 w-4" />
              </Button>
            </div>
            <div className="flex min-h-0 flex-1 flex-col gap-1 overflow-auto">{renderNavGroups(true)}</div>
            <div className="flex flex-col gap-1 border-t border-sidebar-border/70 p-1.5">{renderUserMenu(true)}</div>
          </div>
        </aside>
      </div>
    );
  };

  const sidebarOffsetStyle = {
    "--sidebar-offset": sidebarCollapsed
      ? DESKTOP_SIDEBAR_COLLAPSED_WIDTH
      : DESKTOP_SIDEBAR_WIDTH,
  } as CSSProperties;

  return (
    <div className={cn("min-h-screen bg-background text-foreground", layoutFontClass)}>
      <div className="pointer-events-none fixed inset-x-0 top-0 z-[80] h-[3px] overflow-hidden">
        <div
          className={cn(
            "h-full rounded-r-full bg-primary shadow-[0_0_14px_rgba(180,104,56,0.45)] transition-[width,opacity] duration-300 ease-out",
            navigationProgressVisible ? "opacity-100" : "opacity-0"
          )}
          style={{ width: `${navigationProgressValue}%` }}
        />
      </div>
      <header className="bg-background/95 supports-[backdrop-filter]:bg-background/60 fixed top-0 z-50 w-full border-b border-border/60 backdrop-blur">
        <div className="flex h-14 items-center justify-between px-4 md:px-6">
          <div className="flex items-center gap-2 pl-0 md:pl-2">
            <Button
              variant="ghost"
              size="icon"
              className="size-8 md:-ml-4"
              onClick={handleToggleSidebar}
              aria-label="切换侧边栏"
            >
              <PanelLeft className="h-4 w-4" />
            </Button>

            <Link to="/" className="flex items-center gap-2">
              <div className="flex size-10 shrink-0 items-center justify-center overflow-hidden rounded bg-transparent">
                <img src={iconSvg} alt="Orvion" className="size-10 object-cover" />
              </div>
              <span className="text-sm leading-none font-semibold text-foreground">Orvion</span>
            </Link>
          </div>

          <div className="flex items-center gap-2 pr-0 md:pr-2">
            <Badge
              variant="outline"
              className="hidden rounded-full border-border/70 bg-background/80 px-3 py-1 text-xs text-muted-foreground md:inline-flex"
            >
              <span>{version}</span>
              {versionUpdate?.hasUpdate ? (
                <Popover open={updatePopoverOpen} onOpenChange={setUpdatePopoverOpen}>
                  <PopoverTrigger asChild>
                    <button
                      type="button"
                      aria-label="查看更新内容"
                      className="ml-2 h-2.5 w-2.5 rounded-full bg-amber-400 ring-2 ring-amber-100 transition hover:scale-110"
                    />
                  </PopoverTrigger>
                  <PopoverContent
                    side="bottom"
                    align="end"
                    sideOffset={10}
                    className="w-[min(90vw,28rem)] rounded-2xl border-border/70 bg-card/95 p-0"
                  >
                    <div className="space-y-3 p-4">
                      <div className="space-y-1">
                        <div className="text-sm font-semibold text-foreground">
                          发现新版本 {versionUpdate.release?.tagName ?? "latest"}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          当前版本：{version}，最新版本：{versionUpdate.release?.tagName ?? "latest"}
                        </div>
                        {versionUpdate.release?.publishedAt ? (
                          <div className="text-xs text-muted-foreground">
                            发布时间：{new Date(versionUpdate.release.publishedAt).toLocaleString()}
                          </div>
                        ) : null}
                      </div>
                      <div className="rounded-xl border border-border/70 bg-muted/40 p-3 text-xs whitespace-pre-wrap text-muted-foreground max-h-72 overflow-y-auto">
                        {versionUpdate.release?.body?.trim() || "暂无更新说明"}
                      </div>
                      <div className="flex justify-end">
                        <Button
                          size="sm"
                          onClick={() => {
                            void openExternalUrl(versionUpdate.release?.htmlUrl ?? "https://github.com/raciott/llmio/tags");
                            setUpdatePopoverOpen(false);
                          }}
                        >
                          查看标签页
                        </Button>
                      </div>
                    </div>
                  </PopoverContent>
                </Popover>
              ) : null}
            </Badge>

            {!isAuthKeyToken ? (
              <Button variant="ghost" size="icon" className="size-8" asChild>
                <Link to="/config" aria-label="系统配置">
                  <Settings2 className="h-4 w-4" />
                </Link>
              </Button>
            ) : null}

            {isAuthKeyToken ? (
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                onClick={handleLogout}
                aria-label="退出登录"
              >
                <LogOut className="h-4 w-4" />
              </Button>
            ) : null}
          </div>
        </div>
      </header>

      {renderDesktopSidebar()}
      {renderMobileSidebar()}

      <main className="pt-14">
        <div className="h-[calc(100vh-3.5rem)] transition-[padding-left] duration-200 ease-linear md:pl-[var(--sidebar-offset)]" style={sidebarOffsetStyle}>
          <div className="h-full p-3 md:p-4">
            <div className="mx-auto h-full max-w-[1680px] min-w-0 overflow-hidden">
              <Outlet />
            </div>
          </div>
        </div>
      </main>

    </div>
  );
}
