import type { BackoffConfig } from './types.js';

/**
 * Default deterministic backoff knobs. The base interval and ceiling mirror
 * the Kubernetes Job backoff constants (10s base, 6m ceiling). Unlike the
 * real controller there is no random jitter, so retry instants are exact and
 * testable.
 */
export const DEFAULT_BACKOFF: BackoffConfig = {
  initialIntervalMs: 10_000,
  multiplier: 2,
  maxIntervalMs: 360_000,
};

/** Fill in any backoff knob the caller did not override. */
export function resolveBackoff(
  overrides?: Partial<BackoffConfig>,
): BackoffConfig {
  return { ...DEFAULT_BACKOFF, ...overrides };
}

/**
 * Compute the delay applied before the next retry.
 *
 * `failureNumber` is one-based: the first counted failure of an index waits
 * `initialIntervalMs`, the second `initialIntervalMs * multiplier` and so on,
 * capped at `maxIntervalMs`.
 */
export function backoffDelayMs(
  failureNumber: number,
  config: BackoffConfig,
): number {
  if (failureNumber <= 1) {
    return config.initialIntervalMs;
  }
  const raw = config.initialIntervalMs * config.multiplier ** (failureNumber - 1);
  return Math.min(raw, config.maxIntervalMs);
}

/** Absolute timestamp until which an index must wait after a counted failure. */
export function backoffDeadline(
  failedAt: number,
  failureNumber: number,
  config: BackoffConfig,
): number {
  return failedAt + backoffDelayMs(failureNumber, config);
}
