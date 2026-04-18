import { BrowserRouter as Router, Routes, Route } from 'react-router-dom';
import { Suspense, lazy } from 'react';
import { ThemeProvider } from "@/components/theme-provider"
import Loading from "@/components/loading"
import ErrorBoundary from "@/components/error-boundary"
import { Toaster } from './components/ui/sonner';

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

// 简单的加载组件
const PageLoader = () => (
  <div className="flex items-center justify-center min-h-screen">
    <Loading message="加载中..." />
  </div>
);

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
