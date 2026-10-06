import type { ItemLocation } from "./layout.ts";

/** Structural subset of the layout needed by the anchor machinery. */
export interface AnchorSource {
  readonly count: number;
  locate(offset: number): ItemLocation | null;
  keyAt(index: number): string;
  indexOfKey(key: string): number;
  topOf(index: number): number;
  bottomOf(index: number): number;
}

/**
 * A captured scroll anchor: the first visible item plus the scroll offset
 * inside it, and its neighbours at capture time for deletion fallback.
 */
export interface Anchor {
  key: string;
  offsetInItem: number;
  /** Item right after the anchor when captured (its original successor). */
  nextKey: string | null;
  /** Item right before the anchor when captured. */
  prevKey: string | null;
}

/** Captures the first visible item at `scrollOffset` and its neighbours. */
export function captureAnchor(layout: AnchorSource, scrollOffset: number): Anchor | null {
  const location = layout.locate(scrollOffset);
  if (location === null) {
    return null;
  }
  return {
    key: location.key,
    offsetInItem: location.offsetInItem,
    nextKey: location.index + 1 < layout.count ? layout.keyAt(location.index + 1) : null,
    prevKey: location.index > 0 ? layout.keyAt(location.index - 1) : null,
  };
}

/**
 * Computes the scroll offset that keeps the captured anchor visually stable
 * after a mutation:
 * - anchor still present: restore its top plus the captured inner offset;
 * - anchor deleted: align the top of its original successor;
 * - no successor: align the bottom of its original predecessor;
 * - nothing left: 0.
 */
export function resolveAnchor(layout: AnchorSource, anchor: Anchor | null): number {
  if (anchor === null) {
    return 0;
  }
  const index = layout.indexOfKey(anchor.key);
  if (index >= 0) {
    return layout.topOf(index) + anchor.offsetInItem;
  }
  if (anchor.nextKey !== null) {
    const nextIndex = layout.indexOfKey(anchor.nextKey);
    if (nextIndex >= 0) {
      return layout.topOf(nextIndex);
    }
  }
  if (anchor.prevKey !== null) {
    const prevIndex = layout.indexOfKey(anchor.prevKey);
    if (prevIndex >= 0) {
      return layout.bottomOf(prevIndex);
    }
  }
  return 0;
}
