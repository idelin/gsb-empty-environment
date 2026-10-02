import { PagePacker } from './maxrects.ts';
import {
  footprintSize,
  itemRotatable,
  packingOrder,
  spriteOrigin,
} from './geometry.ts';
import type { ItemSpec, LayoutInput, Placement } from './types.ts';

export class BudgetExceeded extends Error {}

export interface ExactOptions {
  nodeLimit?: number;
  lowerBound: number;
  /** Pages -> obstacle footprints (fixed items), indexed by page. */
  fixedObstacles: Map<number, { x: number; y: number; w: number; h: number }[]>;
}

export interface ExactResult {
  placements: Placement[];
  pages: number;
  /** True when minimality is proven (exhaustive), false if budget ran out. */
  proven: boolean;
}

/**
 * Branch-and-bound over page assignments and candidate positions.
 * Certifies optimality when it finds a solution at the lower bound, or when
 * every smaller page count is exhaustively proven infeasible.
 */
export function exactPack(
  input: LayoutInput,
  items: ItemSpec[],
  opts: ExactOptions,
): ExactResult | null {
  const nodeLimit = opts.nodeLimit ?? 200_000;
  const ordered = packingOrder(items, input.spacing);
  let nodes = 0;

  const count = () => {
    nodes++;
    if (nodes > nodeLimit) throw new BudgetExceeded();
  };

  const tryFeasible = (
    k: number,
  ): { pages: PagePacker[]; placed: { item: ItemSpec; x: number; y: number; w: number; h: number; rotated: boolean; page: number }[] } | null => {
    const pages: PagePacker[] = [];
    for (let p = 0; p < k; p++) {
      const packer = new PagePacker(input.pageWidth, input.pageHeight);
      for (const ob of opts.fixedObstacles.get(p) ?? []) packer.addObstacle(ob);
      pages.push(packer);
    }
    const placed: {
      item: ItemSpec; x: number; y: number; w: number; h: number; rotated: boolean; page: number;
    }[] = [];

    const footprints = ordered.map((item) => {
      const { fw, fh } = footprintSize(item, input.spacing);
      return { item, fw, fh };
    });

    const dfs = (i: number): boolean => {
      if (i === footprints.length) return true;
      count();
      const { item, fw, fh } = footprints[i]!;
      const rotatable = itemRotatable(item) && fw !== fh;
      for (let p = 0; p < k; p++) {
        const page = pages[p]!;
        // Candidate positions: origins of the current free rectangles.
        const seen = new Set<string>();
        const candidates: { x: number; y: number; w: number; h: number; rotated: boolean }[] = [];
        for (const fr of page.freeRects) {
          const orientations = [
            { w: fw, h: fh, rotated: false },
            ...(rotatable ? [{ w: fh, h: fw, rotated: true }] : []),
          ];
          for (const o of orientations) {
            if (o.w > fr.w || o.h > fr.h) continue;
            const key = `${fr.x},${fr.y},${o.rotated}`;
            if (seen.has(key)) continue;
            seen.add(key);
            candidates.push({ x: fr.x, y: fr.y, w: o.w, h: o.h, rotated: o.rotated });
          }
        }
        // Deterministic order: top-to-bottom, left-to-right, unrotated first.
        candidates.sort((a, b) =>
          a.y - b.y || a.x - b.x || Number(a.rotated) - Number(b.rotated),
        );
        for (const c of candidates) {
          const snapshot = { free: page.freeRects, used: page.usedRects.slice() };
          page.placeExact(c, c.rotated);
          placed.push({ item, x: c.x, y: c.y, w: c.w, h: c.h, rotated: c.rotated, page: p });
          if (dfs(i + 1)) return true;
          placed.pop();
          page.freeRects = snapshot.free;
          page.usedRects = snapshot.used;
        }
      }
      return false;
    };

    return dfs(0) ? { pages, placed } : null;
  };

  try {
    for (let k = Math.max(1, opts.lowerBound); k <= ordered.length + 1; k++) {
      const result = tryFeasible(k);
      if (!result) continue;
      const placements: Placement[] = result.placed.map((pl) => {
        const origin = spriteOrigin(pl.x, pl.y, pl.item, input.spacing);
        return {
          id: pl.item.id,
          page: pl.page,
          x: origin.x,
          y: origin.y,
          width: pl.rotated ? pl.item.height : pl.item.width,
          height: pl.rotated ? pl.item.width : pl.item.height,
          rotated: pl.rotated,
        };
      });
      return { placements, pages: k, proven: true };
    }
    return null;
  } catch (err) {
    if (err instanceof BudgetExceeded) return null;
    throw err;
  }
}

/** Area-based page lower bound, also accounting for fixed-item pages. */
export function lowerBoundPages(
  input: LayoutInput,
  items: ItemSpec[],
  fixedMaxPage: number,
): number {
  const pageArea = input.pageWidth * input.pageHeight;
  let total = 0;
  for (const item of items) {
    const { fw, fh } = footprintSize(item, input.spacing);
    total += fw * fh;
  }
  let bound = Math.max(Math.ceil(total / pageArea), items.length > 0 ? 1 : 0);
  if (fixedMaxPage >= 0) bound = Math.max(bound, fixedMaxPage + 1);
  return bound;
}
