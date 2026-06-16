import { BrowserRouter as Router, Navigate, Routes, Route } from 'react-router-dom';
import { Suspense, lazy, useEffect, useState, type ReactNode } from 'react';
import { ThemeProvider } from "@/components/theme-provider"
import Loading from "@/components/loading"
import ErrorBoundary from "@/components/error-boundary"
import { Toaster } from './components/ui/sonner';
import { configAPI, type TelegramAgentConfig } from './lib/api';
import { getStoredAuthTokenMode } from './lib/auth';

// 懒加载路由组件
const Layout = lazy(() => import('./routes/layout'));
const Home = lazy(() => import('./routes/home'));
const HealthPage = lazy(() => import('./routes/health'));
const ProvidersPage = lazy(() => import('./routes/providers'));
const ModelsPage = lazy(() => import('./routes/models'));
const ModelChatTestPage = lazy(() => import('./routes/model-chat'));
const LogsPage = lazy(() => import('./routes/logs'));
const LogChatPage = lazy(() => import('./routes/log-chat'));
const SystemLogsPage = lazy(() => import('./routes/system-logs'));
const LoginPage = lazy(() => import('./routes/login'));
const ConfigPage = lazy(() => import('./routes/config'));
const AuthKeysPage = lazy(() => import('./routes/auth-keys'));
const SkillsPage = lazy(() => import('./routes/skills'));
const TelegramAgentPage = lazy(() => import('./routes/tg-agent'));
const TelegramAgentSchedulesPage = lazy(() => import('./routes/tg-agent-schedules'));
const TELEGRAM_AGENT_CONFIG_CHANGED_EVENT = "telegram-agent-config-changed";

// 简单的加载组件
const PageLoader = () => (
  <div className="flex items-center justify-center min-h-screen">
    <Loading message="加载中..." />
  </div>
);

function AgentFeatureGate({ children }: { children: ReactNode }) {
  const [enabled, setEnabled] = useState<boolean | null>(null);

  useEffect(() => {
    if (getStoredAuthTokenMode() === "auth_key") {
      setEnabled(false);
      return undefined;
    }

    let active = true;
    const fetchConfig = async () => {
      try {
        const response = await configAPI.getConfig("telegram_agent");
        if (!active) return;
        const parsed = response.value ? JSON.parse(response.value) as Partial<TelegramAgentConfig> : {};
        setEnabled(parsed.enabled !== false);
      } catch {
        if (active) setEnabled(true);
      }
    };

    const handleConfigChanged = (event: Event) => {
      const customEvent = event as CustomEvent<{ enabled?: boolean }>;
      setEnabled(customEvent.detail?.enabled !== false);
    };

    void fetchConfig();
    window.addEventListener(TELEGRAM_AGENT_CONFIG_CHANGED_EVENT, handleConfigChanged as EventListener);
    return () => {
      active = false;
      window.removeEventListener(TELEGRAM_AGENT_CONFIG_CHANGED_EVENT, handleConfigChanged as EventListener);
    };
  }, []);

  if (enabled === null) {
    return <PageLoader />;
  }
  if (!enabled) {
    return <Navigate to="/" replace />;
  }
  return children;
}

function App() {
  return (
    <ErrorBoundary>
      <ThemeProvider>
        <Router>
          {/* 内层 ErrorBoundary 让单个页面崩溃后"重试"可以重新挂载路由子树,
              而不用刷新整个应用。外层 ErrorBoundary 负责兜底 Provider 或路由本身的崩溃。 */}
          <ErrorBoundary>
            <Suspense fallback={<PageLoader />}>
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                <Route path="/" element={<Layout />}>
                  <Route index element={<Home />} />
                  <Route path="health-ui" element={<HealthPage />} />
                  <Route path="providers" element={<ProvidersPage />} />
                  <Route path="models" element={<ModelsPage />} />
                  <Route path="model-chat" element={<ModelChatTestPage />} />
                  <Route path="logs" element={<LogsPage />} />
                  <Route path="logs/:logId/chat-io" element={<LogChatPage />} />
                  <Route path="system-logs" element={<SystemLogsPage />} />
                  <Route path="config" element={<ConfigPage />} />
                  <Route path="auth-keys" element={<AuthKeysPage />} />
                  <Route path="skills" element={<AgentFeatureGate><SkillsPage /></AgentFeatureGate>} />
                  <Route path="action" element={<AgentFeatureGate><TelegramAgentPage /></AgentFeatureGate>} />
                  <Route path="tg-agent" element={<Navigate to="/action" replace />} />
                  <Route path="tg-agent-schedules" element={<AgentFeatureGate><TelegramAgentSchedulesPage /></AgentFeatureGate>} />
                </Route>
              </Routes>
            </Suspense>
          </ErrorBoundary>
        </Router>
        <Toaster richColors position='top-center' />
      </ThemeProvider>
    </ErrorBoundary>
  );
}

export default App;
