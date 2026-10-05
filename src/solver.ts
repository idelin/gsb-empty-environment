import {
  firstValue,
  intersect,
  isEmpty,
  lastValue,
  sizeOf,
  subtract,
  type ValSet,
} from "./valset.ts";

/** A Box is a conjunctive region: one ValSet per field. */
export type Box = ValSet[];

export function boxIsEmpty(box: Box): boolean {
  return box.some(isEmpty);
}

export function intersectBox(a: Box, b: Box): Box | null {
  const out = a.map((s, f) => intersect(s, b[f]));
  return boxIsEmpty(out) ? null : out;
}

const SIZE_CAP = Number.MAX_SAFE_INTEGER;

export function boxSize(box: Box): number {
  let acc = 1;
  for (const s of box) acc = Math.min(SIZE_CAP, acc * sizeOf(s));
  return acc;
}

/**
 * box \ blocker, as a disjoint union of boxes (split field by field).
 * Returns [] when box is fully contained in blocker.
 */
export function subtractBox(box: Box, blocker: Box): Box[] {
  const pieces: Box[] = [];
  const rest: Box = box.slice();
  for (let f = 0; f < box.length; f++) {
    const diff = subtract(rest[f], blocker[f]);
    if (!isEmpty(diff)) {
      const piece = rest.slice();
      piece[f] = diff;
      pieces.push(piece);
    }
    const keep = intersect(rest[f], blocker[f]);
    if (isEmpty(keep)) return pieces;
    rest[f] = keep;
  }
  return pieces;
}

/**
 * Find one point inside `allowed` that lies outside every blocker box,
 * or null if the blockers jointly cover `allowed`.
 *
 * Complete backtracking search: to escape a blocker we only need to dodge it
 * on one field, so we branch on (blocker, field) and shrink the allowed set.
 * Never enumerates the input space.
 */
export function findAvoiding(allowed: Box, blockers: Box[]): number[] | null {
  if (boxIsEmpty(allowed)) return null;
  const active: Box[] = [];
  for (const b of blockers) {
    const broken = b.some((bf, f) => isEmpty(intersect(allowed[f], bf)));
    if (!broken) active.push(b);
  }
  if (active.length === 0) return allowed.map(firstValue);

  let chosen: Box | null = null;
  let chosenFields: number[] = [];
  for (const b of active) {
    const fields: number[] = [];
    for (let f = 0; f < allowed.length; f++) {
      if (!isEmpty(subtract(allowed[f], b[f]))) fields.push(f);
    }
    if (fields.length === 0) return null; // allowed ⊆ b: this branch is dead
    if (chosen === null || fields.length < chosenFields.length) {
      chosen = b;
      chosenFields = fields;
    }
  }
  for (const f of chosenFields) {
    const next = allowed.slice();
    next[f] = subtract(allowed[f], (chosen as Box)[f]);
    const hit = findAvoiding(next, blockers);
    if (hit) return hit;
  }
  return null;
}

/**
 * The minimal witness: lexicographically smallest point (fields in declared
 * order, values in domain order) inside `allowed` that avoids all blockers.
 * Per field we binary-search the smallest feasible value, so cost is
 * O(log domain) feasibility checks per field, never a domain scan.
 */
export function minimalWitness(allowed: Box, blockers: Box[]): number[] | null {
  const cur: Box = allowed.slice();
  for (let f = 0; f < cur.length; f++) {
    if (isEmpty(cur[f])) return null;
    let lo = firstValue(cur[f]);
    let hi = lastValue(cur[f]);
    const feasible = (cap: number): boolean => {
      const trial = cur.slice();
      trial[f] = intersect(cur[f], [[0, cap]]);
      return findAvoiding(trial, blockers) !== null;
    };
    if (!feasible(hi)) return null;
    while (lo < hi) {
      const mid = lo + ((hi - lo) >> 1);
      if (feasible(mid)) hi = mid;
      else lo = mid + 1;
    }
    cur[f] = [[lo, lo]];
  }
  return cur.map((s) => s[0][0]);
}

export interface CoverStep {
  /** Index into the `blockers` array passed to explainCover. */
  index: number;
  /** Cells of the (remaining) region this blocker covers at its step. */
  cells: Box[];
}

/**
 * Greedy set-cover explanation: pick the blocker covering the most of the
 * still-uncovered region, subtract it, repeat. Returns null if the blockers
 * do not jointly cover `base` (should not happen when findAvoiding failed).
 */
export function explainCover(base: Box, blockers: Box[]): CoverStep[] | null {
  let uncovered: Box[] = [base];
  const remaining = blockers.map((b, i) => ({ b, i }));
  const steps: CoverStep[] = [];
  while (uncovered.length > 0) {
    let bestIdx = -1;
    let bestCells: Box[] = [];
    let bestSize = 0;
    for (const { b, i } of remaining) {
      const cells: Box[] = [];
      let size = 0;
      for (const u of uncovered) {
        const cell = intersectBox(u, b);
        if (cell) {
          cells.push(cell);
          size += boxSize(cell);
        }
      }
      if (size > bestSize) {
        bestIdx = i;
        bestCells = cells;
        bestSize = size;
      }
    }
    if (bestIdx < 0 || bestSize === 0) return null;
    steps.push({ index: bestIdx, cells: bestCells });
    const chosen = blockers[bestIdx];
    uncovered = uncovered.flatMap((u) => subtractBox(u, chosen));
    remaining.splice(
      remaining.findIndex((r) => r.i === bestIdx),
      1,
    );
  }
  return steps;
}
