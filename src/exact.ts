import { footprintsOverlap } from './types.ts';
import type { Footprint } from './types.ts';
import { footprintSize, orientations } from './packer.ts';
import type { ValidatedItem } from './packer.ts';

export interface ExactPlacement {
  itemIndex: number;
  page: number;
  x: number;
  y: number;
  rotated: boolean;
}

/**
 * Exact feasibility check: can `items` (already validated, movable only)
 * be packed into `pageCount` pages of pageW x pageH, given pre-committed
 * fixed footprints? Uses backtracking over "normal pattern" candidate
 * positions (x in {0} u {right edges}, y in {0} u {bottom edges}), which is
 * complete: any feasible packing can be shifted into such a position set.
 */
export function exactFeasible(
  items: ValidatedItem[],
  pageW: number,
  pageH: number,
  pageCount: number,
  fixedFootprints: Footprint[][],
  nodeBudget = 2_000_000,
): { feasible: boolean; placements: ExactPlacement[] | null; exhausted: boolean } {
  // Sort largest-area-first for pruning; keep original indices.
  const order = items
    .map((it, i) => {
      const fs = footprintSize(it, false);
      return { i, area: fs.w * fs.h };
    })
    .sort((a, b) => b.area - a.area || a.i - b.i)
    .map((e) => e.i);

  const occupied: Footprint[][] = [];
  const freeArea: number[] = [];
  for (let p = 0; p < pageCount; p++) {
    const fixed = fixedFootprints[p] ?? [];
    occupied.push(fixed.map((f) => ({ ...f })));
    freeArea.push(pageW * pageH - fixed.reduce((s, f) => s + f.w * f.h, 0));
  }

  const chosen: (ExactPlacement | null)[] = items.map(() => null);
  let nodes = 0;
  let exhausted = false;

  function fitsOnPage(fp: Footprint, page: number): boolean {
    if (fp.x < 0 || fp.y < 0 || fp.x + fp.w > pageW || fp.y + fp.h > pageH) return false;
    for (const o of occupied[page]!) {
      if (footprintsOverlap(fp, o)) return false;
    }
    return true;
  }

  function search(depth: number): boolean {
    if (depth === order.length) return true;
    if (++nodes > nodeBudget) {
      exhausted = true;
      return false;
    }
    const idx = order[depth]!;
    const item = items[idx]!;
    const fsList = orientations(item).map((r) => ({ rotated: r, ...footprintSize(item, r) }));

    // Remaining items cannot fit in remaining free area anywhere -> prune.
    let totalFree = 0;
    for (let p = 0; p < pageCount; p++) totalFree += freeArea[p]!;
    let needed = 0;
    for (let d = depth; d < order.length; d++) {
      const it2 = items[order[d]!]!;
      const fs = footprintSize(it2, false);
      needed += fs.w * fs.h;
    }
    if (needed > totalFree) return false;

    for (let page = 0; page < pageCount; page++) {
      // Candidate coordinates: 0 and edges of occupied footprints on this page.
      const xs = new Set<number>([0]);
      const ys = new Set<number>([0]);
      for (const o of occupied[page]!) {
        xs.add(o.x + o.w);
        ys.add(o.y + o.h);
      }
      const xList = [...xs].sort((a, b) => a - b);
      const yList = [...ys].sort((a, b) => a - b);
      for (const { rotated, w, h } of fsList) {
        if (w > pageW || h > pageH) continue;
        if (w * h > freeArea[page]!) continue;
        for (const y of yList) {
          if (y + h > pageH) continue;
          for (const x of xList) {
            if (x + w > pageW) continue;
            const fp: Footprint = { x, y, w, h };
            if (!fitsOnPage(fp, page)) continue;
            occupied[page]!.push(fp);
            freeArea[page]! -= w * h;
            chosen[idx] = { itemIndex: idx, page, x, y, rotated };
            if (search(depth + 1)) return true;
            chosen[idx] = null;
            freeArea[page]! += w * h;
            occupied[page]!.pop();
          }
        }
      }
    }
    return false;
  }

  const feasible = search(0);
  return {
    feasible,
    placements: feasible ? (chosen.filter((c) => c !== null) as ExactPlacement[]) : null,
    exhausted,
  };
}

/** Area-based lower bound on page count (margins included via footprints). */
export function areaLowerBound(
  items: ValidatedItem[],
  pageW: number,
  pageH: number,
): number {
  let area = 0;
  for (const it of items) {
    // Use the smaller footprint area across allowed orientations (area is
    // rotation-invariant, but margins make this explicit and safe).
    let best = Infinity;
    for (const r of orientations(it)) {
      const fs = footprintSize(it, r);
      best = Math.min(best, fs.w * fs.h);
    }
    area += best;
  }
  if (area === 0) return 0;
  return Math.max(1, Math.ceil(area / (pageW * pageH)));
}
