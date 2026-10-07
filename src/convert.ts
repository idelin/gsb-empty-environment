import { MAX_OFFSET_MINUTES, TransitionTable } from './table';

/**
 * Local civil time is represented as "local epoch milliseconds": the value
 * `Date.UTC(y, m, d, h, mi, s, ms)` of the wall-clock reading. This keeps the
 * library free of any system-timezone dependence.
 */

/** Milliseconds a UTC candidate can drift from the local reading at most. */
const CANDIDATE_WINDOW_MS = MAX_OFFSET_MINUTES * 60_000;

export type LocalToUtcResult =
  | { readonly kind: 'none' }
  | { readonly kind: 'unique'; readonly utcMs: number }
  | { readonly kind: 'ambiguous'; readonly earlierUtcMs: number; readonly laterUtcMs: number };

export class NonexistentLocalTimeError extends Error {
  constructor(readonly localMs: number) {
    super(`Local time ${localMs} does not exist (falls in a forward-shift gap)`);
    this.name = 'NonexistentLocalTimeError';
  }
}

export class AmbiguousLocalTimeError extends Error {
  constructor(readonly localMs: number, readonly earlierUtcMs: number, readonly laterUtcMs: number) {
    super(`Local time ${localMs} is ambiguous (falls in a backward-shift fold); choose 'earlier' or 'later'`);
    this.name = 'AmbiguousLocalTimeError';
  }
}

export type Disambiguation = 'earlier' | 'later';

/**
 * Map a UTC instant to local civil time using the table.
 */
export function utcToLocal(table: TransitionTable, utcMs: number): { localMs: number; offsetMinutes: number } {
  const offsetMinutes = table.offsetAtUtc(utcMs);
  return { localMs: utcMs + offsetMinutes * 60_000, offsetMinutes };
}

/**
 * Map a local civil reading to its UTC candidate(s).
 *
 * - gap (forward shift): returns `{ kind: 'none' }` — the local time never occurs;
 * - fold (backward shift): returns `{ kind: 'ambiguous' }` with both UTC instants;
 * - otherwise the single matching UTC instant.
 */
export function localToUtc(table: TransitionTable, localMs: number): LocalToUtcResult {
  const candidates: number[] = [];
  // A period i can only match if its UTC range intersects
  // [localMs - WINDOW, localMs + WINDOW]; binary-search the first such period.
  const first = table.periodIndexAtUtc(localMs - CANDIDATE_WINDOW_MS);
  for (let i = first; i < table.periodCount; i += 1) {
    if (table.periodStart(i) >= localMs + CANDIDATE_WINDOW_MS) break;
    const candidate = localMs - table.periodOffset(i) * 60_000;
    if (candidate >= table.periodStart(i) && candidate < table.periodEnd(i)) {
      candidates.push(candidate);
    }
  }
  candidates.sort((a, b) => a - b);
  if (candidates.length === 0) return { kind: 'none' };
  if (candidates.length === 1) return { kind: 'unique', utcMs: candidates[0] };
  // A well-formed real-world table yields at most 2; for pathological inputs
  // report the outermost pair.
  return { kind: 'ambiguous', earlierUtcMs: candidates[0], laterUtcMs: candidates[candidates.length - 1] };
}

/**
 * Resolve a local civil reading to a single UTC instant.
 * Throws {@link NonexistentLocalTimeError} in a gap and
 * {@link AmbiguousLocalTimeError} in a fold unless `disambiguation` is given.
 */
export function resolveLocal(table: TransitionTable, localMs: number, disambiguation?: Disambiguation): number {
  const result = localToUtc(table, localMs);
  switch (result.kind) {
    case 'none':
      throw new NonexistentLocalTimeError(localMs);
    case 'unique':
      return result.utcMs;
    case 'ambiguous':
      if (disambiguation === 'earlier') return result.earlierUtcMs;
      if (disambiguation === 'later') return result.laterUtcMs;
      throw new AmbiguousLocalTimeError(localMs, result.earlierUtcMs, result.laterUtcMs);
  }
}
