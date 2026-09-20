import { useQuery } from "@tanstack/preact-query";

// Data layer for the IM admin console. Mirrors the airway-ui data helpers
// (same {code, data, message} envelope) but adds what a standalone console
// needs and the islands runtime does not: the mount-path prefix, the admin
// bearer token, and a global handler for expired sessions.

type WindowWithAdmin = Window & { IM_ADMIN?: { base?: string } };

/** Mount-path prefix injected by the Go page handler ("" at the root). */
export const BASE = (window as WindowWithAdmin).IM_ADMIN?.base ?? "";
const API_BASE = `${BASE}/admin/api`;

export type ApiResponse<T> = { code: number; data: T; message: string | null };

export class ApiError extends Error {
  code: number;
  status: number;
  constructor(code: number, status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

export type AdminSession = {
  token: string;
  username: string;
  expires_at: string;
};

const SESSION_KEY = "im-admin-session";

export function loadSession(): AdminSession | null {
  try {
    const raw = sessionStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const session = JSON.parse(raw) as AdminSession;
    if (!session.token || !session.expires_at) return null;
    if (new Date(session.expires_at).getTime() <= Date.now()) return null;
    return session;
  } catch {
    return null;
  }
}

export function saveSession(session: AdminSession): void {
  sessionStorage.setItem(SESSION_KEY, JSON.stringify(session));
}

export function clearSession(): void {
  sessionStorage.removeItem(SESSION_KEY);
}

/** Global hook fired when a request comes back unauthenticated. */
let onUnauthorized: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null): void {
  onUnauthorized = fn;
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const session = loadSession();
  const headers = new Headers(init?.headers);
  if (session) headers.set("Authorization", `Bearer ${session.token}`);
  if (init?.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  let res: Response;
  try {
    res = await fetch(API_BASE + path, { ...init, headers });
  } catch (e) {
    throw new ApiError(-1, 0, `network error: ${(e as Error).message}`);
  }

  let body: ApiResponse<T> | null = null;
  try {
    body = (await res.json()) as ApiResponse<T>;
  } catch {
    // non-JSON response (proxy error page, truncated body, …)
  }

  if (res.status === 401 && session) {
    onUnauthorized?.();
  }
  if (!res.ok) {
    throw new ApiError(body?.code ?? res.status, res.status, body?.message || `HTTP ${res.status}`);
  }
  if (!body) {
    throw new ApiError(-1, res.status, "empty response body");
  }
  if (body.code !== 0) {
    throw new ApiError(body.code, res.status, body.message || "request failed");
  }
  return body.data;
}

export function apiPost<T>(path: string, payload?: unknown): Promise<T> {
  return apiFetch<T>(path, {
    method: "POST",
    body: JSON.stringify(payload ?? {}),
  });
}

export function useApiQuery<T>(key: unknown[], path: string, refetchInterval?: number) {
  return useQuery<T>({
    queryKey: key,
    queryFn: () => apiFetch<T>(path),
    refetchInterval,
  });
}
