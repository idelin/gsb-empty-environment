import { Disambiguation, resolveLocal } from './convert';
import { TransitionTable } from './table';

export interface IntervalEndpointChoice {
  /** How to resolve the start if it falls in a fold. */
  readonly start?: Disambiguation;
  /** How to resolve the end if it falls in a fold. */
  readonly end?: Disambiguation;
}

export interface ResolvedInterval {
  readonly startUtcMs: number;
  readonly endUtcMs: number;
  /** Always > 0; guaranteed by validation. */
  readonly durationMs: number;
}

export class NonPositiveIntervalError extends Error {
  constructor(readonly startUtcMs: number, readonly endUtcMs: number) {
    super(`Interval must have positive length, got [${startUtcMs}, ${endUtcMs}]`);
    this.name = 'NonPositiveIntervalError';
  }
}

/**
 * Resolve a local-time interval to UTC. Each endpoint is resolved on its own
 * (fold endpoints need an explicit choice, gap endpoints throw), then the
 * resulting UTC pair is validated to have strictly positive length — a
 * wall-clock interval like 01:30→01:15 across a fall-back can otherwise
 * silently invert.
 */
export function resolveInterval(
  table: TransitionTable,
  startLocalMs: number,
  endLocalMs: number,
  choice: IntervalEndpointChoice = {},
): ResolvedInterval {
  const startUtcMs = resolveLocal(table, startLocalMs, choice.start);
  const endUtcMs = resolveLocal(table, endLocalMs, choice.end);
  if (endUtcMs <= startUtcMs) {
    throw new NonPositiveIntervalError(startUtcMs, endUtcMs);
  }
  return { startUtcMs, endUtcMs, durationMs: endUtcMs - startUtcMs };
}
