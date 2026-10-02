import { footprintOf, footprintsOverlap, normalizeMargin } from './types.ts';
import type {
  ItemSpec,
  Layout,
  Placement,
  PackBounds,
  MarginSpec,
} from './types.ts';
import { validateItems, packHeuristic, layoutFrom, footprintSize, orientations } from './packer.ts';
import { exactFeasible, areaLowerBound } from './exact.ts';
import { verifyLayout } from './verify.ts';

export interface ResizeRequest {
  id: string;
  w: number;
  h: number;
}

export interface LayoutChanges {
  add?: ItemSpec[];
  remove?: string[];
  resize?: ResizeRequest[];
}

export interface OpPosition {
  page: number;
  x: number;
  y: number;
  rotated: boolean;
}

export type MoveOp =
  | { type: 'keep'; id: string; at: OpPosition }
  | { type: 'remove'; id: string; from: OpPosition }
  | { type: 'place'; id: string; to: OpPosition }
  | { type: 'move'; id: string; from: OpPosition; to: OpPosition };

export interface UpdateOk {
  ok: true;
  layout: Layout;
  plan: MoveOp[];
  bounds: PackBounds;
}

export interface UpdateFail {
  ok: false;
  error: string;
  /** Nothing is published on failure: no layout, no partial plan. */
}

export type UpdateResult = UpdateOk | UpdateFail;

function posOf(p: Placement): OpPosition {
  return { page: p.page, x: p.x, y: p.y, rotated: p.rotated };
}

function samePos(a: OpPosition, b: OpPosition): boolean {
  return a.page === b.page && a.x === b.x && a.y === b.y && a.rotated === b.rotated;
}

/**
 * Apply add/remove/resize changes to an existing layout.
 *
 * Objectives, in order:
 *   1. Move as few already-placed textures as possible (fixed items never
 *      move; unchanged items keep their exact positions; resized items stay
 *      put when their new footprint still fits where they are).
 *   2. Then minimize page count for the displaced/new items, filling free
 *      space on existing pages before opening new ones.
 *
 * The result is atomic: the returned plan and layout are only published when
 * the final layout passes the independent pixel-occupancy verifier. Any
 * failure (invalid change, oversized texture, unplaceable fixed item,
 * verification failure) returns `ok: false` and no coordinates.
 */
export function applyChanges(
  layout: Layout,
  items: ItemSpec[],
  changes: LayoutChanges,
  exactThreshold = 12,
): UpdateResult {
  const oldSpecs = new Map(items.map((it) => [it.id, it]));
  const oldPlacements = new Map(layout.placements.map((p) => [p.id, p]));

  // --- 1. Build the new spec list. ---
  const removeIds = new Set(changes.remove ?? []);
  for (const id of removeIds) {
    if (!oldSpecs.has(id)) return { ok: false, error: `cannot remove unknown id '${id}'` };
  }
  const resizeById = new Map<string, ResizeRequest>();
  for (const r of changes.resize ?? []) {
    if (!oldSpecs.has(r.id)) return { ok: false, error: `cannot resize unknown id '${r.id}'` };
    if (removeIds.has(r.id)) return { ok: false, error: `id '${r.id}' is both removed and resized` };
    resizeById.set(r.id, r);
  }
  const nextSpecs: ItemSpec[] = [];
  for (const spec of items) {
    if (removeIds.has(spec.id)) continue;
    const r = resizeById.get(spec.id);
    nextSpecs.push(r === undefined ? spec : { ...spec, w: r.w, h: r.h });
  }
  for (const add of changes.add ?? []) {
    if (oldSpecs.has(add.id) && !removeIds.has(add.id)) {
      return { ok: false, error: `cannot add duplicate id '${add.id}'` };
    }
    nextSpecs.push(add);
  }

  const { valid, invalid } = validateItems(nextSpecs);
  if (invalid.length > 0) {
    return { ok: false, error: `invalid items: ${invalid.map((u) => `${u.id} (${u.detail})`).join('; ')}` };
  }

  // --- 2. Decide which existing placements can stay (minimal moves). ---
  const keptFpByPage: Map<number, ReturnType<typeof footprintOf>[]> = new Map();
  const keepable = new Set<string>();
  const displaced: string[] = [];

  // Fixed items keep their placement by definition; validate they still fit.
  for (const v of valid) {
    if (v.spec.fixed === undefined) continue;
    const f = v.spec.fixed;
    const rotated = f.rotated === true && v.spec.w !== v.spec.h;
    const sizes = footprintSize(v, rotated);
    const fx = f.x - v.margin.left;
    const fy = f.y - v.margin.top;
    if (fx < 0 || fy < 0 || fx + sizes.w > layout.pageW || fy + sizes.h > layout.pageH) {
      return {
        ok: false,
        error: `fixed item '${v.spec.id}' no longer fits on page ${f.page} after change`,
      };
    }
  }

  // Process in deterministic id order so overlap checks are stable.
  const candidates = valid
    .filter((v) => v.spec.fixed === undefined && oldPlacements.has(v.spec.id))
    .sort((a, b) => (a.spec.id < b.spec.id ? -1 : 1));
  for (const v of candidates) {
    const old = oldPlacements.get(v.spec.id)!;
    const oldSpec = oldSpecs.get(v.spec.id)!;
    const resized = resizeById.has(v.spec.id);
    const marginChanged =
      JSON.stringify(normalizeMargin(oldSpec.margin)) !== JSON.stringify(v.margin);
    const rotationStillAllowed = !old.rotated || v.rotatable;
    if (!resized && !marginChanged && rotationStillAllowed) {
      keepable.add(v.spec.id);
      continue;
    }
    // Changed item: may stay only if its new footprint fits at the old spot.
    const stored = { w: old.rotated ? v.spec.h : v.spec.w, h: old.rotated ? v.spec.w : v.spec.h };
    const fp = {
      x: old.x - v.margin.left,
      y: old.y - v.margin.top,
      w: stored.w + v.margin.left + v.margin.right,
      h: stored.h + v.margin.top + v.margin.bottom,
    };
    const fitsPage =
      fp.x >= 0 && fp.y >= 0 && fp.x + fp.w <= layout.pageW && fp.y + fp.h <= layout.pageH;
    if (fitsPage && rotationStillAllowed) {
      keepable.add(v.spec.id);
    } else {
      displaced.push(v.spec.id);
    }
  }

  // Kept items must not collide with each other (they cannot: the old layout
  // was valid and only shrunk-or-equal... resized items may have grown, so
  // check grown footprints against other kept footprints and evict on clash).
  let changed = true;
  while (changed) {
    changed = false;
    const keptList = [...keepable].sort();
    for (let i = 0; i < keptList.length; i++) {
      const a = keptList[i]!;
      const pa = keptPlacement(a, oldPlacements, valid);
      const fa = footprintOf(pa);
      for (let j = i + 1; j < keptList.length; j++) {
        const b = keptList[j]!;
        const pb = keptPlacement(b, oldPlacements, valid);
        if (pa.page !== pb.page) continue;
        if (footprintsOverlap(fa, footprintOf(pb))) {
          // Evict the resized one; if both unchanged, evict the later id.
          const evict = resizeById.has(a) && !resizeById.has(b) ? a : b;
          keepable.delete(evict);
          displaced.push(evict);
          changed = true;
          break;
        }
      }
      if (changed) break;
    }
  }

  // --- 3. Repack: kept items become fixed, displaced/added are movable. ---
  const repackSpecs: ItemSpec[] = nextSpecs.map((spec) => {
    if (removeIds.has(spec.id)) return spec; // filtered below
    if (spec.fixed !== undefined) return spec;
    if (keepable.has(spec.id)) {
      const old = oldPlacements.get(spec.id)!;
      return {
        ...spec,
        fixed: { page: old.page, x: old.x, y: old.y, rotated: old.rotated },
      };
    }
    return spec;
  }).filter((spec) => !removeIds.has(spec.id));

  const { valid: repackValid, invalid: repackInvalid } = validateItems(repackSpecs);
  if (repackInvalid.length > 0) {
    return { ok: false, error: `invalid after repack: ${repackInvalid.map((u) => u.detail).join('; ')}` };
  }
  const packed = packHeuristic(repackValid, layout.pageW, layout.pageH);
  if (packed.unpacked.length > 0) {
    return {
      ok: false,
      error: `cannot place: ${packed.unpacked.map((u) => `${u.id} (${u.reason}: ${u.detail})`).join('; ')}`,
    };
  }
  const newLayout = layoutFrom(layout.pageW, layout.pageH, packed.placements, []);

  // --- 4. Verify before publishing (atomicity). ---
  const report = verifyLayout(newLayout, repackSpecs);
  if (!report.ok) {
    return { ok: false, error: `verification failed, nothing published: ${report.errors.join('; ')}` };
  }

  // --- 5. Emit the atomic move plan. ---
  const plan: MoveOp[] = [];
  const newPlacements = new Map(newLayout.placements.map((p) => [p.id, p]));
  for (const id of [...oldPlacements.keys()].sort()) {
    const old = oldPlacements.get(id)!;
    if (removeIds.has(id)) {
      plan.push({ type: 'remove', id, from: posOf(old) });
      continue;
    }
    const np = newPlacements.get(id);
    if (np === undefined) {
      return { ok: false, error: `internal error: '${id}' lost during update` };
    }
    const from = posOf(old);
    const to = posOf(np);
    if (samePos(from, to)) {
      plan.push({ type: 'keep', id, at: to });
    } else {
      plan.push({ type: 'move', id, from, to });
    }
  }
  for (const p of [...newLayout.placements].sort((a, b) => (a.id < b.id ? -1 : 1))) {
    if (!oldPlacements.has(p.id)) {
      plan.push({ type: 'place', id: p.id, to: posOf(p) });
    }
  }

  // --- 6. Bounds for the updated instance. ---
  const bounds = computeBounds(repackValid, newLayout, layout.pageW, layout.pageH, exactThreshold);
  return { ok: true, layout: newLayout, plan, bounds };
}

function keptPlacement(
  id: string,
  oldPlacements: Map<string, Placement>,
  valid: { spec: ItemSpec; margin: ReturnType<typeof normalizeMargin> }[],
): Placement {
  const old = oldPlacements.get(id)!;
  const v = valid.find((e) => e.spec.id === id)!;
  return { ...old, w: old.rotated ? v.spec.h : v.spec.w, h: old.rotated ? v.spec.w : v.spec.h, margin: v.margin };
}

function computeBounds(
  valid: { spec: ItemSpec; margin: ReturnType<typeof normalizeMargin>; rotatable: boolean }[],
  layout: Layout,
  pageW: number,
  pageH: number,
  exactThreshold: number,
): PackBounds {
  const lower = areaLowerBound(valid, pageW, pageH);
  const upper = layout.pages;
  const movable = valid.filter((v) => v.spec.fixed === undefined);
  if (upper === lower && lower > 0) {
    return { lower, upper, optimal: upper, certificate: 'exact' };
  }
  if (movable.length === 0 || movable.length > exactThreshold) {
    return {
      lower,
      upper,
      optimal: movable.length === 0 ? upper : null,
      certificate: movable.length === 0 ? 'exact' : 'heuristic',
    };
  }
  const fixedFp = new Map<number, ReturnType<typeof footprintOf>[]>();
  for (const p of layout.placements) {
    const v = valid.find((e) => e.spec.id === p.id)!;
    if (v.spec.fixed === undefined) continue;
    const arr = fixedFp.get(p.page) ?? [];
    arr.push(footprintOf(p));
    fixedFp.set(p.page, arr);
  }
  const fixedArr: ReturnType<typeof footprintOf>[][] = [];
  for (const [page, fps] of fixedFp) {
    while (fixedArr.length <= page) fixedArr.push([]);
    fixedArr[page] = fps;
  }
  for (let k = Math.max(lower, 1); k <= upper; k++) {
    const res = exactFeasible(movable, pageW, pageH, k, fixedArr);
    if (res.exhausted) return { lower, upper, optimal: null, certificate: 'heuristic' };
    if (res.feasible) return { lower, upper, optimal: k, certificate: 'exact' };
  }
  return { lower, upper, optimal: upper, certificate: 'exact' };
}

/** Re-export for callers that want margin math without the packer. */
export { normalizeMargin };
export type { MarginSpec };
