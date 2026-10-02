import { footprintOf } from './types.ts';
import type {
  ItemSpec,
  PackOptions,
  PackResult,
  Placement,
  Footprint,
} from './types.ts';
import {
  validateItems,
  packHeuristic,
  layoutFrom,
  storedSize,
  footprintSize,
  orientations,
} from './packer.ts';
import type { ValidatedItem } from './packer.ts';
import { exactFeasible, areaLowerBound } from './exact.ts';
import type { ExactPlacement } from './exact.ts';
import { verifyLayout } from './verify.ts';

/**
 * Pack items into fixed-size pages.
 *
 * - Deterministic MaxRects heuristic produces the layout (upper bound).
 * - An area bound gives the lower bound; when the movable item count is at
 *   most `exactThreshold` (default 12) an exact branch-and-bound proves the
 *   minimal page count and the layout is replaced by an optimal one.
 * - Every returned layout is re-checked by the independent pixel-occupancy
 *   verifier; if verification fails the function throws instead of
 *   publishing a broken layout.
 */
export function pack(items: ItemSpec[], options: PackOptions): PackResult {
  const { pageW, pageH } = options;
  const exactThreshold = options.exactThreshold ?? 12;
  if (!Number.isInteger(pageW) || pageW <= 0 || !Number.isInteger(pageH) || pageH <= 0) {
    throw new Error(`page size must be positive integers, got ${pageW}x${pageH}`);
  }

  const { valid, invalid } = validateItems(items);
  const heuristic = packHeuristic(valid, pageW, pageH);
  const unpacked = [...invalid, ...heuristic.unpacked];

  const movable = valid.filter((it) => it.spec.fixed === undefined);
  // Items that can never fit an empty page are reported unpacked and are
  // irrelevant to the page-count proof, so the exact solver skips them.
  const placeableMovable = movable.filter((it) =>
    orientations(it).some((r) => {
      const fs = footprintSize(it, r);
      return fs.w <= pageW && fs.h <= pageH;
    }),
  );

  const lower = areaLowerBound(
    valid.filter((it) => !unpacked.some((u) => u.id === it.spec.id)),
    pageW,
    pageH,
  );
  let upper = layoutFrom(pageW, pageH, heuristic.placements, unpacked).pages;
  let optimal: number | null = null;
  let certificate: 'exact' | 'heuristic' = 'heuristic';
  let placements: Placement[] = heuristic.placements;

  const canAttemptExact = placeableMovable.length <= exactThreshold && placeableMovable.length > 0;

  if (canAttemptExact) {
    // Footprints committed by fixed items, per page.
    const fixedFp: Footprint[][] = [];
    for (const p of heuristic.placements) {
      const spec = valid.find((v) => v.spec.id === p.id)!;
      if (spec.spec.fixed === undefined) continue;
      while (fixedFp.length <= p.page) fixedFp.push([]);
      fixedFp[p.page]!.push(footprintOf(p));
    }
    const fixedPages = fixedFp.length;
    const startK = Math.max(lower, fixedPages > 0 ? fixedPages : 1);
    let budgetExhausted = false;
    for (let k = startK; k <= upper; k++) {
      const res = exactFeasible(placeableMovable, pageW, pageH, k, fixedFp);
      if (res.exhausted) {
        budgetExhausted = true;
        break; // Budget hit: stay with heuristic certificate.
      }
      if (res.feasible && res.placements !== null) {
        optimal = k;
        certificate = 'exact';
        if (k < upper) {
          // Rebuild the layout with the exact (optimal) page count.
          placements = rebuildWithExact(valid, heuristic.placements, res.placements, placeableMovable);
          upper = k;
        }
        break;
      }
      // Infeasible at k pages: the lower bound rises to k + 1.
    }
    if (optimal === null && !budgetExhausted && upper === startK) {
      // Heuristic already matches the best provable bound.
      optimal = upper;
      certificate = 'exact';
    }
  } else if (movable.length === 0) {
    // Only fixed items: page count is forced, hence optimal.
    optimal = upper;
    certificate = 'exact';
  }

  if (optimal === null && upper === lower && lower > 0) {
    // The layout meets the area lower bound: provably optimal.
    optimal = upper;
    certificate = 'exact';
  }

  const layout = layoutFrom(pageW, pageH, placements, unpacked);
  const report = verifyLayout(layout, items);
  if (!report.ok) {
    throw new Error(`internal error: produced layout failed verification: ${report.errors.join('; ')}`);
  }
  return { layout, bounds: { lower, upper, optimal, certificate } };
}

function rebuildWithExact(
  valid: ValidatedItem[],
  heuristicPlacements: Placement[],
  exactPlacements: ExactPlacement[],
  movable: ValidatedItem[],
): Placement[] {
  const fixed = heuristicPlacements.filter((p) =>
    valid.some((v) => v.spec.id === p.id && v.spec.fixed !== undefined),
  );
  const out: Placement[] = [...fixed];
  for (const ep of exactPlacements) {
    const item = movable[ep.itemIndex]!;
    const stored = storedSize(item, ep.rotated);
    out.push({
      id: item.spec.id,
      page: ep.page,
      x: ep.x + item.margin.left,
      y: ep.y + item.margin.top,
      w: stored.w,
      h: stored.h,
      rotated: ep.rotated,
      margin: item.margin,
    });
  }
  return out;
}
