/**
 * Local <-> UTC conversion driven entirely by an explicit offset transition
 * table. The system timezone is never consulted; every rule comes from the
 * table supplied by the caller.
 *
 * Conventions:
 *  - Instants (UTC) are epoch milliseconds (`number`).
 *  - Local wall-clock times are also expressed as epoch-like milliseconds,
 *    i.e. the value a UTC clock would show if the offset were zero.
 *  - Offsets are integer minutes east of UTC (non-integer-hour offsets such
 *    as +05:45 are fully supported).
 */

export interface OffsetTransition {
  /** UTC instant at which `offsetMinutes` takes effect. */
  readonly utc: number;
  /** Offset from UTC, in minutes, in effect at and after `utc`. */
  readonly offsetMinutes: number;
}

export interface ZoneTable {
  readonly name: string;
  /** Offset in effect before the first transition. */
  readonly initialOffsetMinutes: number;
  /** Transitions sorted ascending by `utc`. */
  readonly transitions: readonly OffsetTransition[];
}

export type LocalResolution =
  | { readonly kind: "none" } // local time falls in a gap (spring forward)
  | { readonly kind: "single"; readonly utc: number }
  | { readonly kind: "ambiguous"; readonly earlierUtc: number; readonly laterUtc: number }; // fold (fall back)

export type FoldChoice = "earlier" | "later";

export class NonExistentLocalTimeError extends Error {
  constructor(
    public readonly zone: string,
    public readonly local: number,
    public readonly gapStartLocal: number,
    public readonly gapEndLocal: number,
  ) {
    super(
      `Local time ${local} does not exist in zone "${zone}": ` +
        `it falls inside the gap [${gapStartLocal}, ${gapEndLocal}).`,
    );
    this.name = "NonExistentLocalTimeError";
  }
}

export class AmbiguousLocalTimeError extends Error {
  constructor(
    public readonly zone: string,
    public readonly local: number,
    public readonly earlierUtc: number,
    public readonly laterUtc: number,
  ) {
    super(
      `Local time ${local} is ambiguous in zone "${zone}": ` +
        `it maps to UTC ${earlierUtc} and ${laterUtc}; a fold choice is required.`,
    );
    this.name = "AmbiguousLocalTimeError";
  }
}

export class NonPositiveIntervalError extends Error {
  constructor(
    public readonly startUtc: number,
    public readonly endUtc: number,
  ) {
    super(`Interval must have positive length, got [${startUtc}, ${endUtc}].`);
    this.name = "NonPositiveIntervalError";
  }
}

const MINUTE_MS = 60_000;

export function createZoneTable(input: ZoneTable): ZoneTable {
  for (let i = 0; i < input.transitions.length; i++) {
    const t = input.transitions[i]!;
    if (!Number.isInteger(t.offsetMinutes)) {
      throw new Error(`Transition ${i} has a non-integer-minute offset.`);
    }
    if (i > 0 && t.utc <= input.transitions[i - 1]!.utc) {
      throw new Error("Transitions must be strictly ascending by utc.");
    }
  }
  if (!Number.isInteger(input.initialOffsetMinutes)) {
    throw new Error("Initial offset must be an integer number of minutes.");
  }
  return input;
}

/** Offset (minutes) in effect at a UTC instant. */
export function offsetAtUtc(table: ZoneTable, utc: number): number {
  const transitions = table.transitions;
  let lo = 0;
  let hi = transitions.length; // search over "last transition with utc <= instant"
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (transitions[mid]!.utc <= utc) lo = mid + 1;
    else hi = mid;
  }
  return lo === 0 ? table.initialOffsetMinutes : transitions[lo - 1]!.offsetMinutes;
}

/** Convert a UTC instant to its local wall-clock reading. */
export function utcToLocal(
  table: ZoneTable,
  utc: number,
): { local: number; offsetMinutes: number } {
  const offsetMinutes = offsetAtUtc(table, utc);
  return { local: utc + offsetMinutes * MINUTE_MS, offsetMinutes };
}

/**
 * Map a local wall-clock time to its UTC candidate(s).
 *
 * A local time L is valid for offset o iff the instant L - o is actually
 * observed with offset o. Zero valid candidates means L never occurs (gap);
 * two means L occurs twice (fold).
 */
export function localToUtcCandidates(table: ZoneTable, local: number): number[] {
  const offsets = new Set<number>([table.initialOffsetMinutes]);
  for (const t of table.transitions) offsets.add(t.offsetMinutes);

  const candidates: number[] = [];
  for (const offset of offsets) {
    const utc = local - offset * MINUTE_MS;
    if (offsetAtUtc(table, utc) === offset) candidates.push(utc);
  }
  candidates.sort((a, b) => a - b);
  return candidates;
}

/** Classify a local time as nonexistent, unique, or ambiguous. */
export function classifyLocal(table: ZoneTable, local: number): LocalResolution {
  const candidates = localToUtcCandidates(table, local);
  if (candidates.length === 0) return { kind: "none" };
  if (candidates.length === 1) return { kind: "single", utc: candidates[0]! };
  return { kind: "ambiguous", earlierUtc: candidates[0]!, laterUtc: candidates[1]! };
}

/** Locate the gap (spring forward) containing a nonexistent local time. */
export function findGap(
  table: ZoneTable,
  local: number,
): { gapStartLocal: number; gapEndLocal: number; transition: OffsetTransition } {
  for (const t of table.transitions) {
    const before = offsetAtUtc(table, t.utc - 1);
    if (t.offsetMinutes > before) {
      const gapStartLocal = t.utc + before * MINUTE_MS;
      const gapEndLocal = t.utc + t.offsetMinutes * MINUTE_MS;
      if (local >= gapStartLocal && local < gapEndLocal) {
        return { gapStartLocal, gapEndLocal, transition: t };
      }
    }
  }
  throw new Error(`Local time ${local} is not inside any gap of zone "${table.name}".`);
}

/**
 * Resolve a local time to a single UTC instant.
 *
 *  - Gap: always throws `NonExistentLocalTimeError` (the time does not exist).
 *  - Fold: throws `AmbiguousLocalTimeError` unless `foldChoice` selects one of
 *    the two candidates.
 */
export function resolveLocal(
  table: ZoneTable,
  local: number,
  foldChoice?: FoldChoice,
): number {
  const resolution = classifyLocal(table, local);
  switch (resolution.kind) {
    case "single":
      return resolution.utc;
    case "none": {
      const gap = findGap(table, local);
      throw new NonExistentLocalTimeError(
        table.name,
        local,
        gap.gapStartLocal,
        gap.gapEndLocal,
      );
    }
    case "ambiguous":
      if (foldChoice === "earlier") return resolution.earlierUtc;
      if (foldChoice === "later") return resolution.laterUtc;
      throw new AmbiguousLocalTimeError(
        table.name,
        local,
        resolution.earlierUtc,
        resolution.laterUtc,
      );
  }
}

export interface LocalInterval {
  readonly startUtc: number;
  readonly endUtc: number;
  readonly durationMs: number;
}

/**
 * Convert a local-time interval to UTC. Each endpoint is resolved
 * independently (fold choices apply per endpoint); the resulting UTC
 * interval must have strictly positive length.
 */
export function localIntervalToUtc(
  table: ZoneTable,
  startLocal: number,
  endLocal: number,
  choices: { startFoldChoice?: FoldChoice; endFoldChoice?: FoldChoice } = {},
): LocalInterval {
  const startUtc = resolveLocal(table, startLocal, choices.startFoldChoice);
  const endUtc = resolveLocal(table, endLocal, choices.endFoldChoice);
  if (endUtc <= startUtc) throw new NonPositiveIntervalError(startUtc, endUtc);
  return { startUtc, endUtc, durationMs: endUtc - startUtc };
}
