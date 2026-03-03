import { useEffect, useState } from "react";
import { Link, Outlet, useNavigate, useLocation } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  FaHome,
  FaCloud,
  FaRobot,
  FaFileAlt,
  FaSignOutAlt,
  FaCog,
  FaKey,
  FaHeartbeat,
  FaCrown,
  FaUserShield,
} from "react-icons/fa";
import { getVersion, checkLatestRelease, type GitHubRelease } from "@/lib/api";
import { clearStoredAuthToken, getStoredAuthToken } from "@/lib/auth";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

export default function Layout() {
  const [version, setVersion] = useState("dev");
  const [latestRelease, setLatestRelease] = useState<GitHubRelease | null>(null);
  const [showUpdateDialog, setShowUpdateDialog] = useState(false);
  const navigate = useNavigate();
  const location = useLocation(); // 用于高亮当前选中的菜单
  const token = getStoredAuthToken();
  const isAuthKeyToken = ["sk-github.com/racio/orvion-", "sk-github.com/racio/llmio-"].some((prefix) =>
    token.startsWith(prefix)
  );

  useEffect(() => {
    if (isAuthKeyToken) {
      return undefined;
    }
    let active = true;

    const fetchVersion = async () => {
      try {
        const value = await getVersion();
        if (active && value) {
          setVersion(value);
        }
      } catch {
        // Keep default version when API is unreachable or unauthorized.
      }
    };

    void fetchVersion();

    return () => {
      active = false;
    };
  }, []);

  // Check for updates when on home page
  useEffect(() => {
    if (isAuthKeyToken) {
      return;
    }
    if (location.pathname === '/') {
      const checkForUpdates = async () => {
        try {
          const release = await checkLatestRelease('raciott', 'llmio');
          if (release && release.tag_name !== version) {
            setLatestRelease(release);
            setShowUpdateDialog(true);
          }
        } catch (error) {
          console.error('Failed to check for updates:', error);
        }
      };

      void checkForUpdates();
    }
  }, [location.pathname, version]);

  const handleLogout = () => {
    clearStoredAuthToken();
    navigate("/login");
  };

  const navSections = [
    {
      title: "01 概览",
      items: [
        { to: "/", label: "首页", icon: <FaHome /> },
        { to: "/health-ui", label: "健康监控", icon: <FaHeartbeat /> },
      ],
    },
    {
      title: "02 运营",
      items: [
        { to: "/providers", label: "提供商管理", icon: <FaCloud /> },
        { to: "/models", label: "模型管理", icon: <FaRobot /> },
        { to: "/logs", label: "请求日志", icon: <FaFileAlt /> },
      ],
    },
    {
      title: "03 Auth订阅",
      items: [
        { to: "/codex-official", label: "Codex 官方", icon: <FaCrown /> },
        { to: "/iflow-auth", label: "iFlow 认证", icon: <FaUserShield /> },
      ],
    },
    {
      title: "04 系统",
      items: [
        { to: "/auth-keys", label: "API Key 管理", icon: <FaKey /> },
        { to: "/config", label: "系统配置", icon: <FaCog /> },
      ],
    },
  ];

  return (
    <div className="flex flex-col h-screen w-full dark:bg-gray-900 transition-colors duration-300">
      
      {/* 1. 顶部栏 Header */}
      <header className="bg-background/80 backdrop-blur flex items-center flex-shrink-0 z-20 border-b border-border/70">
        <div className="mx-auto flex w-full max-w-[1400px] items-center justify-between px-4 py-3">
          <div className="flex items-center gap-3 ml-2">
            <div>
              <div className="text-lg font-semibold text-foreground">Orvion</div>
              <div className="text-[11px] text-muted-foreground">多提供商网关</div>
            </div>
            {!isAuthKeyToken && (
              <span className="ml-3 hidden items-center rounded-full bg-primary/10 px-3 py-1 text-[10px] font-semibold tracking-[0.2em] text-primary sm:inline-flex">
                ADMIN MODE
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
            <Badge
              variant="outline"
              className="text-muted-foreground cursor-pointer hover:bg-accent transition-colors"
              onClick={() => latestRelease && setShowUpdateDialog(true)}
              title={latestRelease ? `有新版本 ${latestRelease.tag_name} 可用` : '当前版本'}
            >
              {version}
              {latestRelease && (
                <span className="ml-1 text-xs text-red-500">●</span>
              )}
            </Badge>
          
          <Button 
            variant="ghost" 
            onClick={handleLogout}
            className="gap-2"
          >
            <FaSignOutAlt />
          </Button>
        </div>
        </div>
      </header>

      {/* 2. 下方主体区域 */}
      <div className="flex-1 min-w-0">
        <div className="mx-auto flex h-full w-full max-w-[1400px] px-4">
          <div className="flex w-full min-w-0 gap-4 py-4">
        
        {/* 左侧侧边栏 Sidebar */}
        {!isAuthKeyToken && (
          <aside className="w-60 shrink-0 self-start rounded-3xl border border-sidebar-border bg-sidebar/90 p-4 shadow-[0_18px_40px_rgba(0,0,0,0.06)]">
            <nav className="space-y-6">
              {navSections.map((section) => (
                <div key={section.title} className="space-y-2">
                  <div className="text-[11px] font-semibold tracking-[0.2em] text-muted-foreground">
                    {section.title}
                  </div>
                  <ul className="space-y-1">
                    {section.items.map((item) => {
                      const isActive = location.pathname === item.to;
                      return (
                        <li key={item.to}>
                          <Link
                            to={item.to}
                            className={`group flex items-center gap-3 rounded-2xl px-3 py-2 text-sm transition-colors ${
                              isActive
                                ? "bg-primary/10 text-primary ring-1 ring-primary/20"
                                : "text-muted-foreground hover:bg-sidebar-accent/70 hover:text-foreground"
                            }`}
                          >
                            <span className="text-base">{item.icon}</span>
                            <span className="font-medium">{item.label}</span>
                          </Link>
                        </li>
                      );
                    })}
                  </ul>
                </div>
              ))}
            </nav>
          </aside>
        )}

        {/* 右侧主内容区域 */}
        <main className="flex-1 min-w-0 rounded-3xl border border-border/60 bg-card/60 p-3 md:p-5">
          <div className="mx-auto max-w-full h-full min-w-0 overflow-x-hidden">
            <Outlet />
          </div>
        </main>
          </div>
        </div>
      </div>

      {/* Update Dialog */}
      <Dialog open={showUpdateDialog} onOpenChange={setShowUpdateDialog}>
        <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>发现新版本 {latestRelease?.tag_name}</DialogTitle>
            <DialogDescription>
              当前版本: {version} → 最新版本: {latestRelease?.tag_name}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div>
              <h4 className="font-semibold mb-2">更新内容：</h4>
              <div className="bg-muted p-4 rounded-md text-sm whitespace-pre-wrap max-h-96 overflow-y-auto">
                {latestRelease?.body || '暂无更新说明'}
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                onClick={() => setShowUpdateDialog(false)}
              >
                稍后提醒
              </Button>
              <Button
                onClick={() => {
                  window.open(latestRelease?.html_url, '_blank');
                  setShowUpdateDialog(false);
                }}
              >
                查看详情
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
