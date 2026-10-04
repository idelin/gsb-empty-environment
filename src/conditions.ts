import type { Condition, FileMeta, Tri } from './types.ts';
import { triAnd, triNot, triOr } from './truth.ts';
import { globDisjoint, globMatches, globSubsumes } from './glob.ts';

/** Evaluate a condition against one file. Missing metadata yields 'unknown'. */
export function evalCondition(cond: Condition, file: FileMeta): Tri {
  switch (cond.kind) {
    case 'path':
      return globMatches(cond.glob, file.path) ? 'true' : 'false';
    case 'size':
      return evalRange(cond.min, cond.max, file.size);
    case 'version':
      return evalRange(cond.min, cond.max, file.version);
    case 'tag': {
      if (file.tags === undefined) return 'unknown';
      const has = file.tags.includes(cond.tag);
      const result = cond.negate ? !has : has;
      return result ? 'true' : 'false';
    }
    case 'not':
      return triNot(evalCondition(cond.cond, file));
    case 'all':
      return triAnd(cond.conds.map((c) => evalCondition(c, file)));
    case 'any':
      return triOr(cond.conds.map((c) => evalCondition(c, file)));
  }
}

function evalRange(min: number | undefined, max: number | undefined, value: number | undefined): Tri {
  if (value === undefined) return 'unknown';
  if (min !== undefined && value < min) return 'false';
  if (max !== undefined && value > max) return 'false';
  return 'true';
}

/**
 * A flat, conservative approximation of a condition, used for static
 * analysis and for cheaply pre-filtering files during incremental updates.
 * `unsatisfiable` marks a condition that can never hold (e.g. min > max).
 * `residual` marks conditions with structure we could not flatten (any-of,
 * negation of composites); the remaining fields then describe only the
 * parts we are sure about.
 */
export interface ConstraintSummary {
  pathGlob: string | null; // null = any path
  sizeMin: number | null; // null = unbounded
  sizeMax: number | null;
  versionMin: number | null;
  versionMax: number | null;
  requiredTags: ReadonlySet<string>;
  forbiddenTags: ReadonlySet<string>;
  residual: boolean;
  unsatisfiable: boolean;
}

export function summarizeCondition(cond: Condition): ConstraintSummary {
  const acc: {
    pathGlob: string | null;
    sizeMin: number | null;
    sizeMax: number | null;
    versionMin: number | null;
    versionMax: number | null;
    requiredTags: Set<string>;
    forbiddenTags: Set<string>;
    residual: boolean;
    unsatisfiable: boolean;
  } = {
    pathGlob: null,
    sizeMin: null,
    sizeMax: null,
    versionMin: null,
    versionMax: null,
    requiredTags: new Set(),
    forbiddenTags: new Set(),
    residual: false,
    unsatisfiable: false,
  };
  flatten(cond, true, acc);
  return acc;
}

function meetBound(current: number | null, next: number, pick: 'min' | 'max'): number {
  if (current === null) return next;
  return pick === 'min' ? Math.max(current, next) : Math.min(current, next);
}

function flatten(
  cond: Condition,
  positive: boolean,
  acc: ReturnType<typeof freshAcc>,
): void {
  switch (cond.kind) {
    case 'path':
      if (!positive) {
        acc.residual = true;
      } else if (acc.pathGlob === null || acc.pathGlob === cond.glob) {
        acc.pathGlob = cond.glob;
      } else if (globSubsumes(acc.pathGlob, cond.glob)) {
        acc.pathGlob = cond.glob; // narrower
      } else if (globSubsumes(cond.glob, acc.pathGlob)) {
        // keep the narrower existing one
      } else if (globDisjoint(acc.pathGlob, cond.glob)) {
        acc.unsatisfiable = true;
      } else {
        acc.residual = true; // overlapping but unrelatable globs
      }
      return;
    case 'size':
    case 'version': {
      const minKey = cond.kind === 'size' ? 'sizeMin' : 'versionMin';
      const maxKey = cond.kind === 'size' ? 'sizeMax' : 'versionMax';
      if (positive) {
        if (cond.min !== undefined) acc[minKey] = meetBound(acc[minKey], cond.min, 'min');
        if (cond.max !== undefined) acc[maxKey] = meetBound(acc[maxKey], cond.max, 'max');
      } else if (cond.min !== undefined && cond.max === undefined) {
        // not(x >= min)  ==>  x < min
        acc[maxKey] = meetBound(acc[maxKey], cond.min - 1, 'max');
      } else if (cond.max !== undefined && cond.min === undefined) {
        // not(x <= max)  ==>  x > max
        acc[minKey] = meetBound(acc[minKey], cond.max + 1, 'min');
      } else {
        acc.residual = true; // negation of a bounded range is a union
      }
      if (
        acc[minKey] !== null &&
        acc[maxKey] !== null &&
        (acc[minKey] as number) > (acc[maxKey] as number)
      ) {
        acc.unsatisfiable = true;
      }
      return;
    }
    case 'tag': {
      const required = positive !== Boolean(cond.negate);
      const target = required ? acc.requiredTags : acc.forbiddenTags;
      const other = required ? acc.forbiddenTags : acc.requiredTags;
      if (other.has(cond.tag)) acc.unsatisfiable = true;
      target.add(cond.tag);
      return;
    }
    case 'not':
      flatten(cond.cond, !positive, acc);
      return;
    case 'all':
      if (positive) {
        for (const c of cond.conds) flatten(c, true, acc);
      } else {
        acc.residual = true; // not(all(...)) is a union
      }
      return;
    case 'any':
      if (positive) {
        acc.residual = true; // a union cannot be flattened conjunctively
      } else {
        // not(any(...)) == all(not(...))
        for (const c of cond.conds) flatten(c, false, acc);
      }
      return;
  }
}

function freshAcc() {
  return {
    pathGlob: null as string | null,
    sizeMin: null as number | null,
    sizeMax: null as number | null,
    versionMin: null as number | null,
    versionMax: null as number | null,
    requiredTags: new Set<string>(),
    forbiddenTags: new Set<string>(),
    residual: false,
    unsatisfiable: false,
  };
}

function rangesOverlap(
  aMin: number | null,
  aMax: number | null,
  bMin: number | null,
  bMax: number | null,
): boolean {
  if (aMax !== null && bMin !== null && aMax < bMin) return false;
  if (bMax !== null && aMin !== null && bMax < aMin) return false;
  return true;
}

/** Conservative: true when some file could satisfy both summaries. */
export function summariesOverlap(a: ConstraintSummary, b: ConstraintSummary): boolean {
  if (a.unsatisfiable || b.unsatisfiable) return false;
  if (!rangesOverlap(a.sizeMin, a.sizeMax, b.sizeMin, b.sizeMax)) return false;
  if (!rangesOverlap(a.versionMin, a.versionMax, b.versionMin, b.versionMax)) return false;
  for (const t of a.requiredTags) if (b.forbiddenTags.has(t)) return false;
  for (const t of b.requiredTags) if (a.forbiddenTags.has(t)) return false;
  if (a.pathGlob !== null && b.pathGlob !== null && globDisjoint(a.pathGlob, b.pathGlob)) {
    return false;
  }
  return true;
}

/**
 * Conservative implication: true when every file satisfying `inner` also
 * satisfies `outer`. Requires `outer` to be fully flattened (no residual).
 */
export function summaryImplies(inner: ConstraintSummary, outer: ConstraintSummary): boolean {
  if (inner.unsatisfiable) return true; // empty set is subset of anything
  if (outer.unsatisfiable) return false;
  if (outer.residual) return false;
  const within = (iMin: number | null, iMax: number | null, oMin: number | null, oMax: number | null) =>
    (oMin === null || (iMin !== null && iMin >= oMin)) &&
    (oMax === null || (iMax !== null && iMax <= oMax));
  if (!within(inner.sizeMin, inner.sizeMax, outer.sizeMin, outer.sizeMax)) return false;
  if (!within(inner.versionMin, inner.versionMax, outer.versionMin, outer.versionMax)) return false;
  for (const t of outer.requiredTags) if (!inner.requiredTags.has(t)) return false;
  for (const t of outer.forbiddenTags) if (!inner.forbiddenTags.has(t)) return false;
  if (outer.pathGlob !== null) {
    if (inner.pathGlob === null) return false;
    if (!globSubsumes(outer.pathGlob, inner.pathGlob)) return false;
  }
  return true;
}

/**
 * Cheap conservative pre-filter: true when `file` could satisfy a condition
 * with this summary. Unknown metadata never excludes a file here.
 */
export function summaryMayMatchFile(summary: ConstraintSummary, file: FileMeta): boolean {
  if (summary.unsatisfiable) return false;
  if (summary.pathGlob !== null && !globMatches(summary.pathGlob, file.path)) return false;
  if (file.size !== undefined) {
    if (summary.sizeMin !== null && file.size < summary.sizeMin) return false;
    if (summary.sizeMax !== null && file.size > summary.sizeMax) return false;
  }
  if (file.version !== undefined) {
    if (summary.versionMin !== null && file.version < summary.versionMin) return false;
    if (summary.versionMax !== null && file.version > summary.versionMax) return false;
  }
  if (file.tags !== undefined) {
    for (const t of summary.requiredTags) if (!file.tags.includes(t)) return false;
    for (const t of summary.forbiddenTags) if (file.tags.includes(t)) return false;
  }
  return true;
}
