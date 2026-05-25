const AUTH_TOKEN_KEY = "authToken";
const AUTH_TOKEN_MODE_KEY = "authTokenMode";

export type AuthTokenMode = "admin" | "auth_key";

export function normalizeAuthToken(rawToken: string | null | undefined): string {
  const trimmed = (rawToken ?? "").trim();
  if (!trimmed) return "";
  return trimmed.replace(/^Bearer\s+/i, "").trim();
}

function normalizeAuthTokenMode(rawMode: string | null | undefined): AuthTokenMode | "" {
  const mode = (rawMode ?? "").trim().toLowerCase();
  if (mode === "admin") return "admin";
  if (mode === "auth_key") return "auth_key";
  return "";
}

export function getStoredAuthToken(): string {
  const raw = localStorage.getItem(AUTH_TOKEN_KEY);
  const normalized = normalizeAuthToken(raw);
  if (!normalized) {
    if (raw) localStorage.removeItem(AUTH_TOKEN_KEY);
    return "";
  }
  if (raw !== normalized) {
    localStorage.setItem(AUTH_TOKEN_KEY, normalized);
  }
  return normalized;
}

export function setStoredAuthToken(rawToken: string): string {
  const normalized = normalizeAuthToken(rawToken);
  if (!normalized) {
    localStorage.removeItem(AUTH_TOKEN_KEY);
    localStorage.removeItem(AUTH_TOKEN_MODE_KEY);
    return "";
  }
  localStorage.setItem(AUTH_TOKEN_KEY, normalized);
  return normalized;
}

export function getStoredAuthTokenMode(): AuthTokenMode | "" {
  const rawMode = localStorage.getItem(AUTH_TOKEN_MODE_KEY);
  const normalized = normalizeAuthTokenMode(rawMode);
  if (!normalized) {
    if (rawMode) localStorage.removeItem(AUTH_TOKEN_MODE_KEY);
    return "";
  }
  if (rawMode !== normalized) {
    localStorage.setItem(AUTH_TOKEN_MODE_KEY, normalized);
  }
  return normalized;
}

export function setStoredAuthTokenMode(mode: AuthTokenMode): void {
  localStorage.setItem(AUTH_TOKEN_MODE_KEY, mode);
}

export function clearStoredAuthToken(): void {
  localStorage.removeItem(AUTH_TOKEN_KEY);
  localStorage.removeItem(AUTH_TOKEN_MODE_KEY);
}

export function hasStoredAuthToken(): boolean {
  return getStoredAuthToken().length > 0;
}

export function clearStoredAuthTokenAndRedirect(): never {
  clearStoredAuthToken();
  if (window.location.pathname !== "/login") {
    window.location.replace("/login");
  }
  throw new Error("Unauthorized");
}
