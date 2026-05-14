/**
 * Low-level HTTP client.
 *
 * Reads the JWT from a getter (so we can break the import cycle with the auth
 * store — the store imports this module via endpoints.ts). Throws `ApiError`
 * on non-2xx and on network failure. Returns parsed JSON or `undefined` for
 * 204.
 */

import { ApiError } from "./errors";

type TokenGetter = () => string | null;
type UnauthorizedHandler = () => void;

let tokenGetter: TokenGetter = () => null;
let onUnauthorized: UnauthorizedHandler = () => {};

export function configureClient(opts: {
  getToken?: TokenGetter;
  onUnauthorized?: UnauthorizedHandler;
}): void {
  if (opts.getToken) tokenGetter = opts.getToken;
  if (opts.onUnauthorized) onUnauthorized = opts.onUnauthorized;
}

export type QueryValue = string | number | boolean | undefined | null;
export type QueryMap = { [k: string]: QueryValue };

export interface ApiFetchOptions {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
  query?: QueryMap;
  /** Skip auth header even if a token is available. */
  anonymous?: boolean;
  signal?: AbortSignal;
}

function buildQuery(q: QueryMap | undefined): string {
  if (!q) return "";
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === "") continue;
    params.append(k, String(v));
  }
  const s = params.toString();
  return s ? `?${s}` : "";
}

export async function apiFetch<T>(path: string, opts: ApiFetchOptions = {}): Promise<T> {
  const url = path + buildQuery(opts.query);
  const headers: Record<string, string> = { Accept: "application/json" };

  if (!opts.anonymous) {
    const token = tokenGetter();
    if (token) headers["Authorization"] = `Bearer ${token}`;
  }

  let body: BodyInit | undefined;
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }

  let res: Response;
  try {
    res = await fetch(url, {
      method: opts.method ?? "GET",
      headers,
      body,
      signal: opts.signal,
    });
  } catch (cause) {
    throw ApiError.network(cause);
  }

  if (res.status === 204) {
    return undefined as T;
  }

  const isJson = (res.headers.get("content-type") ?? "").includes("application/json");
  let payload: unknown;
  if (isJson) {
    try {
      payload = await res.json();
    } catch (cause) {
      // Don't swallow parse errors on 2xx — callers expect a real body. We
      // tolerate broken JSON on error responses so we can still surface the
      // status code.
      if (res.ok) throw ApiError.network(cause);
      payload = null;
    }
  } else {
    payload = await res.text();
  }

  if (!res.ok) {
    const err = ApiError.fromBody(res.status, payload);
    if (err.status === 401) onUnauthorized();
    throw err;
  }
  return payload as T;
}

/** GET wrapper preserving generic inference. */
export const apiGet = <T>(path: string, query?: QueryMap, signal?: AbortSignal) =>
  apiFetch<T>(path, { method: "GET", query, signal });

export const apiPost = <T>(path: string, body?: unknown, query?: QueryMap) =>
  apiFetch<T>(path, { method: "POST", body, query });

export const apiPatch = <T>(path: string, body: unknown) =>
  apiFetch<T>(path, { method: "PATCH", body });

export const apiPut = <T>(path: string, body: unknown) =>
  apiFetch<T>(path, { method: "PUT", body });

export const apiDelete = (path: string) => apiFetch<void>(path, { method: "DELETE" });
