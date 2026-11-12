import type { TrustChainErrorCode } from './types.ts';

/**
 * Error thrown for every trust chain validation failure. The stable
 * {@link code} lets callers branch programmatically instead of matching on
 * human-readable messages.
 */
export class TrustChainError extends Error {
  readonly code: TrustChainErrorCode;

  constructor(code: TrustChainErrorCode, message: string) {
    super(message);
    this.name = 'TrustChainError';
    this.code = code;
  }
}

/** Wrap a failure from an injected signature verifier. */
export function signatureError(cause: unknown): TrustChainError {
  const detail = cause instanceof Error ? cause.message : String(cause);
  return new TrustChainError(
    'INVALID_SIGNATURE',
    `signature verification failed: ${detail}`,
  );
}
