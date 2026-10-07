import { ERRORS, type ErrorCode } from "./generated.js";

/** Codes raised by the SDK itself rather than the server. */
export type ClientErrorCode = "SERVER_NOT_RUNNING" | "CONNECTION_LOST";

/**
 * Every failure the SDK reports. `code` is an error code from spec/commands.json or a
 * ClientErrorCode; `data` holds the extra fields the server sent, like `screen` or `candidates`.
 */
export class DroidlineError extends Error {
  readonly code: ErrorCode | ClientErrorCode | (string & {});
  readonly retryable: boolean;
  readonly data: Record<string, unknown>;

  constructor(message: string, code: string, retryable = false, data: Record<string, unknown> = {}) {
    super(message);
    this.name = "DroidlineError";
    this.code = code;
    this.retryable = retryable;
    this.data = data;
  }
}

export function errorFromResponse(resp: Record<string, unknown>): DroidlineError {
  const { id: _id, ok: _ok, error, msg, retryable, ...data } = resp;
  const code = typeof error === "string" && error ? error : "INTERNAL";
  const known = (ERRORS as Record<string, { retryable: boolean } | undefined>)[code];
  const flag = typeof retryable === "boolean" ? retryable : (known?.retryable ?? false);
  return new DroidlineError(typeof msg === "string" && msg ? msg : code, code, flag, data);
}
