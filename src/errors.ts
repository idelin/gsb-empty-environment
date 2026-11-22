export const ErrorCode = {
  INVALID_ARGUMENT: "INVALID_ARGUMENT",
  INVALID_MANIFEST: "INVALID_MANIFEST",
  INVALID_REPRESENTATION: "INVALID_REPRESENTATION",
  UNSUPPORTED_ALGORITHM: "UNSUPPORTED_ALGORITHM",
  DIGEST_MISMATCH: "DIGEST_MISMATCH",
  LENGTH_MISMATCH: "LENGTH_MISMATCH",
  DOMAIN_MISMATCH: "DOMAIN_MISMATCH",
  SHARD_CONFLICT: "SHARD_CONFLICT",
  SHARD_RANGE: "SHARD_RANGE",
  SHARD_GAP: "SHARD_GAP",
  INVALID_SHARD: "INVALID_SHARD",
  INCOMPLETE_TRANSPORT: "INCOMPLETE_TRANSPORT",
  INCOMPLETE_DECODE: "INCOMPLETE_DECODE",
  TRAILING_BYTES: "TRAILING_BYTES",
  DECODER_BACKPRESSURE: "DECODER_BACKPRESSURE",
  PLACEHOLDER_MISSING: "PLACEHOLDER_MISSING",
  PLACEHOLDER_RANGE: "PLACEHOLDER_RANGE",
  PLACEHOLDER_TOKEN: "PLACEHOLDER_TOKEN",
  PLACEHOLDER_BINDING: "PLACEHOLDER_BINDING",
  PLACEHOLDER_VALUE: "PLACEHOLDER_VALUE",
  REVOKED: "REVOKED",
  CANCELLED: "CANCELLED",
  ABORTED: "ABORTED",
  NOT_PUBLISHED: "NOT_PUBLISHED",
  CREDENTIAL_INVALID: "CREDENTIAL_INVALID",
  INVALID_PROOF: "INVALID_PROOF",
  CACHE_MISS: "CACHE_MISS",
  VERIFICATION_FAILED: "VERIFICATION_FAILED",
} as const;

export type ErrorCodeValue = (typeof ErrorCode)[keyof typeof ErrorCode];

export interface PipelineErrorDetails {
  [key: string]: unknown;
}

/**
 * Every rejection from the pipeline is a PipelineError.  A stable `code` is
 * intentionally exposed instead of requiring callers to match on messages.
 */
export class PipelineError extends Error {
  readonly code: ErrorCodeValue | string;
  readonly details: PipelineErrorDetails;
  readonly cause?: unknown;

  constructor(
    code: ErrorCodeValue | string,
    message: string,
    details: PipelineErrorDetails = {},
    cause?: unknown,
  ) {
    super(message);
    this.name = "PipelineError";
    this.code = code;
    this.details = details;
    this.cause = cause;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export function isPipelineError(value: unknown): value is PipelineError {
  return value instanceof PipelineError;
}

export function pipelineError(
  code: ErrorCodeValue | string,
  message: string,
  details?: PipelineErrorDetails,
  cause?: unknown,
): PipelineError {
  return new PipelineError(code, message, details, cause);
}

export function asPipelineError(
  value: unknown,
  fallbackCode: ErrorCodeValue | string = ErrorCode.VERIFICATION_FAILED,
  fallbackMessage = "asset verification failed",
): PipelineError {
  if (value instanceof PipelineError) {
    return value;
  }
  if (value instanceof Error) {
    return new PipelineError(fallbackCode, value.message, {}, value);
  }
  return new PipelineError(fallbackCode, fallbackMessage, { value });
}
