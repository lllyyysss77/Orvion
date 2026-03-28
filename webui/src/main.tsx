import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { hasStoredAuthToken } from './lib/auth'

const renderApp = () => {
  const path = window.location.pathname;
  if (path === '/login' || path.startsWith('/login/')) {
    return <App />;
  }

  if (!hasStoredAuthToken()) {
    window.location.href = '/login';
    return null;
  }

  return <App />;
};

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {renderApp()}
  </StrictMode>,
)
