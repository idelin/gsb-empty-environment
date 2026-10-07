import { utcToLocal } from './convert';
import { TransitionTable } from './table';

export interface UtcMapping {
  readonly utcMs: number;
  readonly localMs: number;
  readonly offsetMinutes: number;
}

/**
 * Enumerate the UTC→local mapping over `[fromUtcMs, toUtcMs)` at `stepMs`
 * intervals. Primary use is verification: gaps appear as local ranges no
 * sample lands on, folds as local ranges sampled twice.
 */
export function enumerateUtcMapping(
  table: TransitionTable,
  fromUtcMs: number,
  toUtcMs: number,
  stepMs: number,
): UtcMapping[] {
  if (!(stepMs > 0)) throw new RangeError(`stepMs must be positive, got ${stepMs}`);
  const out: UtcMapping[] = [];
  for (let utcMs = fromUtcMs; utcMs < toUtcMs; utcMs += stepMs) {
    const { localMs, offsetMinutes } = utcToLocal(table, utcMs);
    out.push({ utcMs, localMs, offsetMinutes });
  }
  return out;
}
