const AUTH_TOKEN_KEY = "authToken";

export function normalizeAuthToken(rawToken: string | null | undefined): string {
  const trimmed = (rawToken ?? "").trim();
  if (!trimmed) return "";
  return trimmed.replace(/^Bearer\s+/i, "").trim();
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
    return "";
  }
  localStorage.setItem(AUTH_TOKEN_KEY, normalized);
  return normalized;
}

export function clearStoredAuthToken(): void {
  localStorage.removeItem(AUTH_TOKEN_KEY);
}

export function hasStoredAuthToken(): boolean {
  return getStoredAuthToken().length > 0;
}
