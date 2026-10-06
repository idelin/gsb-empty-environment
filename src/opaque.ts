import { Rect, subtract, sortCanonical, isEmpty } from "./rect";

/**
 * Opaque visibility decomposition.
 *
 * Input: axis-aligned rectangles in layer order, index 0 = bottom,
 * index n-1 = top. All rectangles are treated as fully opaque.
 *
 * Output: for each input rectangle, the list of rectangular fragments of it
 * that remain visible (not covered by any rectangle above it). Fragments use
 * half-open bounds, are pairwise non-overlapping within and across layers,
 * and each list is sorted in canonical order (y0, x0, y1, x1).
 */
export function visibleFragments(layers: readonly Rect[]): Rect[][] {
  const result: Rect[][] = [];
  for (let i = 0; i < layers.length; i++) {
    const r = layers[i]!;
    let free: Rect[] = isEmpty(r) ? [] : [r];
    for (let j = i + 1; j < layers.length && free.length > 0; j++) {
      const occ = layers[j]!;
      if (isEmpty(occ)) continue;
      const next: Rect[] = [];
      for (const f of free) next.push(...subtract(f, occ));
      free = next;
    }
    result.push(sortCanonical(free));
  }
  return result;
}

/**
 * Flattened view: every visible fragment tagged with its source layer index,
 * sorted canonically. The union of these fragments is exactly the visible
 * region of the whole stack.
 */
export interface LayeredFragment {
  readonly layer: number;
  readonly rect: Rect;
}

export function visibleFragmentsFlat(layers: readonly Rect[]): LayeredFragment[] {
  const perLayer = visibleFragments(layers);
  const all: LayeredFragment[] = [];
  for (let i = 0; i < perLayer.length; i++) {
    for (const r of perLayer[i]!) all.push({ layer: i, rect: r });
  }
  return all.sort((a, b) => {
    const c = cmpRect(a.rect, b.rect);
    return c !== 0 ? c : a.layer - b.layer;
  });
}

function cmpRect(a: Rect, b: Rect): number {
  if (a.y0 !== b.y0) return a.y0 - b.y0;
  if (a.x0 !== b.x0) return a.x0 - b.x0;
  if (a.y1 !== b.y1) return a.y1 - b.y1;
  return a.x1 - b.x1;
}
