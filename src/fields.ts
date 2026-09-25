import { deepEqual, type JsonValue } from "./json.ts";

/** One source's contribution to a field, with lineage back to the source path. */
export interface Contribution {
  value: JsonValue;
  source: string;
  version: number;
  /** JSON Pointer into the source batch where this value came from. */
  path: string;
}

/**
 * Field-level lineage: every source's latest contribution is kept, so removing
 * a source lets the field fall back to an older contribution from another
 * source ("old snapshots can still restore old sources").
 */
export interface FieldState {
  contributions: Map<string, Contribution>;
  /** Winning contribution, or null when empty or undecided. */
  effective: Contribution | null;
  /** Undecided top-priority candidates with differing values (never picked arbitrarily). */
  conflict: Contribution[] | null;
}

export function emptyFieldState(): FieldState {
  return { contributions: new Map(), effective: null, conflict: null };
}

export function cloneFieldState(state: FieldState): FieldState {
  return {
    contributions: new Map(state.contributions),
    effective: state.effective,
    conflict: state.conflict ? [...state.conflict] : null,
  };
}

/**
 * Deterministic adjudication:
 *  - highest source priority wins;
 *  - a single top candidate wins;
 *  - tied top candidates with equal values are not a conflict;
 *  - tied top candidates with differing values stay UNDECIDED (conflict),
 *    effective becomes null — the value is never chosen arbitrarily.
 */
export function adjudicate(
  state: FieldState,
  priorityOf: (source: string) => number,
): void {
  const all = [...state.contributions.values()];
  if (all.length === 0) {
    state.effective = null;
    state.conflict = null;
    return;
  }
  let topPriority = -Infinity;
  for (const c of all) topPriority = Math.max(topPriority, priorityOf(c.source));
  const top = all.filter((c) => priorityOf(c.source) === topPriority);
  const first = top[0]!;
  const allEqual = top.every((c) => deepEqual(c.value, first.value));
  if (allEqual) {
    state.effective = first;
    state.conflict = null;
  } else {
    state.effective = null;
    state.conflict = top;
  }
}

/** Apply a contribution to a copied state and re-adjudicate (preview). */
export function previewWith(
  base: FieldState | undefined,
  source: string,
  contribution: Contribution | null,
  priorityOf: (source: string) => number,
): FieldState {
  const next = base ? cloneFieldState(base) : emptyFieldState();
  if (contribution === null) next.contributions.delete(source);
  else next.contributions.set(source, contribution);
  adjudicate(next, priorityOf);
  return next;
}
