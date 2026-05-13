import type { ApiErrorBody, ApiErrorCode } from "./types";

export class ApiError extends Error {
  public readonly status: number;
  public readonly code: ApiErrorCode | "network" | "unknown";
  public readonly details: Record<string, unknown> | undefined;

  constructor(
    status: number,
    code: ApiErrorCode | "network" | "unknown",
    message: string,
    details?: Record<string, unknown>,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }

  static fromBody(status: number, body: unknown): ApiError {
    const env = body as Partial<ApiErrorBody> | null;
    const err = env?.error;
    if (err && typeof err.code === "string" && typeof err.message === "string") {
      return new ApiError(status, err.code, err.message, err.details);
    }
    return new ApiError(status, "unknown", `HTTP ${status}`);
  }

  static network(cause: unknown): ApiError {
    const msg = cause instanceof Error ? cause.message : String(cause);
    return new ApiError(0, "network", `Network error: ${msg}`);
  }
}

export function isAuthError(e: unknown): boolean {
  return e instanceof ApiError && (e.code === "unauthorized" || e.status === 401);
}
