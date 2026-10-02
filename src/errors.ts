/**
 * Central error-code catalog. Every failure surfaced by the engine carries one
 * of these codes and is counted under the `errors.total{code=...}` metric.
 */
export const ErrorCodes = {
  WAL_CHECKSUM_MISMATCH: "E_WAL_CHECKSUM",
  WAL_TORN_WRITE: "E_WAL_TORN",
  WAL_UNKNOWN_RECORD_TYPE: "E_WAL_TYPE",
  WAL_IO: "E_WAL_IO",
  RECOVERY_PAGE_MISSING: "E_RECO_PAGE",
  RECOVERY_INVALID_LSN: "E_RECO_LSN",
  RECOVERY_CORRUPT_STATE: "E_RECO_STATE",
  METRIC_TYPE_CONFLICT: "E_METRIC_CONFLICT",
  METRIC_NOT_FOUND: "E_METRIC_NOT_FOUND",
  METRIC_INVALID_VALUE: "E_METRIC_VALUE",
  METRIC_LABEL_MISMATCH: "E_METRIC_LABEL",
  BUFFER_PAGE_NOT_FOUND: "E_BUFFER_MISS",
  BUFFER_PIN_VIOLATION: "E_BUFFER_PIN",
  TXN_INVALID_STATE: "E_TXN_STATE",
} as const;

export type ErrorCode = (typeof ErrorCodes)[keyof typeof ErrorCodes];

export class EngineError extends Error {
  readonly code: ErrorCode;
  readonly context: Record<string, unknown> | undefined;

  constructor(code: ErrorCode, message: string, context?: Record<string, unknown>) {
    super(`[${code}] ${message}`);
    this.name = "EngineError";
    this.code = code;
    this.context = context;
  }
}
