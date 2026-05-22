// API client for /api/v1/* — thin fetch wrapper that:
//
//   1. Always sends credentials so the session cookie travels (the cookie is
//      HttpOnly + SameSite=Strict per D15/D16, so no Authorization header
//      handling is needed).
//   2. Parses the structured error envelope (code + message + extras) and
//      throws an ApiError that components can render uniformly.
//   3. Parses the RFC 5988 Link header for `prev` / `next` URLs so list
//      views can paginate without re-implementing the parser (D23).
//   4. Surfaces 401 to a single global handler — set via `setUnauthorizedHandler`
//      from main.tsx — so a session that expires mid-use bounces the user to
//      /login instead of leaving the admin chrome rendered over a sea of
//      "UNAUTHORIZED" error banners.

export interface ApiErrorPayload {
  code: string;
  message: string;
  [key: string]: unknown;
}

export class ApiError extends Error {
  status: number;
  code: string;
  payload: ApiErrorPayload | null;

  constructor(status: number, payload: ApiErrorPayload | null, fallbackMessage: string) {
    super(payload?.message ?? fallbackMessage);
    this.status = status;
    this.code = payload?.code ?? "UNKNOWN_ERROR";
    this.payload = payload;
  }
}

let unauthorizedHandler: (() => void) | null = null;

// setUnauthorizedHandler registers a callback invoked exactly once whenever
// any API call returns 401 — used to invalidate the cached operator and
// redirect to /login. Set from main.tsx; tests can clear by passing null.
export function setUnauthorizedHandler(fn: (() => void) | null) {
  unauthorizedHandler = fn;
}

function fireUnauthorized() {
  if (unauthorizedHandler) {
    try {
      unauthorizedHandler();
    } catch {
      /* swallow; the redirect side-effect must not throw further */
    }
  }
}

export interface PaginatedResult<T> {
  items: T[];
  links: { prev?: string; next?: string };
}

export async function apiFetch<T = unknown>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: {
      Accept: "application/json",
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      ...(init.headers ?? {}),
    },
    ...init,
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = null;
    }
  }

  if (!res.ok) {
    const payload = (body as ApiErrorPayload) ?? null;
    if (res.status === 401) fireUnauthorized();
    throw new ApiError(res.status, payload, `HTTP ${res.status}`);
  }

  return body as T;
}

// apiListFetch wraps apiFetch for list endpoints. It returns the parsed JSON
// body alongside any `prev` / `next` URLs extracted from the `Link` header so
// the caller can offer prev/next navigation without re-implementing RFC 5988.
export async function apiListFetch<T>(path: string): Promise<PaginatedResult<T>> {
  const res = await fetch(path, {
    credentials: "include",
    headers: { Accept: "application/json" },
  });
  const text = await res.text();
  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = null;
    }
  }
  if (!res.ok) {
    const payload = (body as ApiErrorPayload) ?? null;
    if (res.status === 401) fireUnauthorized();
    throw new ApiError(res.status, payload, `HTTP ${res.status}`);
  }
  return {
    items: (body ?? []) as T[],
    links: parseLinkHeader(res.headers.get("Link")),
  };
}

function parseLinkHeader(header: string | null): { prev?: string; next?: string } {
  if (!header) return {};
  const out: { prev?: string; next?: string } = {};
  // RFC 5988: link-value = "<" URI-Reference ">" *( ";" link-param )
  // We only care about rel="prev" and rel="next".
  for (const part of header.split(",")) {
    const m = part.trim().match(/^<([^>]+)>\s*;\s*rel="?(\w+)"?/);
    if (!m) continue;
    const [, url, rel] = m;
    if (rel === "prev" || rel === "next") out[rel] = url;
  }
  return out;
}
