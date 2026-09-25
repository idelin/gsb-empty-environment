import type { MergeMap } from "./merge.ts";
import {
  rectOfRegion,
  rectsEqual,
  unionRects,
  type Pos,
  type Rect,
} from "./types.ts";

/** A range selection: the cell where the drag started and the current end. */
export interface Selection {
  anchor: Pos;
  focus: Pos;
}

/** Axis-aligned bounding rect of a selection, before merge expansion. */
export function selectionRect(selection: Selection): Rect {
  return {
    top: Math.min(selection.anchor.row, selection.focus.row),
    left: Math.min(selection.anchor.col, selection.focus.col),
    bottom: Math.max(selection.anchor.row, selection.focus.row),
    right: Math.max(selection.anchor.col, selection.focus.col),
  };
}

/**
 * Expand a rect so every merged region it touches is fully covered.
 *
 * Expansion is monotone (the rect only grows) inside a finite grid, so
 * iterating to a fixed point always terminates - including for chains
 * of merges where covering region A pulls in region B, which pulls in
 * region C, and so on. The iteration cap is a defensive bound only;
 * it can never be reached before the fixed point.
 */
export function expandToMerges(rect: Rect, merges: MergeMap): Rect {
  let current = rect;
  const maxIterations = merges.regions.length + 1;
  for (let i = 0; i < maxIterations; i++) {
    let next = current;
    for (const region of merges.regionsIntersecting(current)) {
      next = unionRects(next, rectOfRegion(region));
    }
    if (rectsEqual(next, current)) return current;
    current = next;
  }
  return current;
}

/** The effective selected rect: bounding box expanded over all merges. */
export function effectiveSelection(selection: Selection, merges: MergeMap): Rect {
  return expandToMerges(selectionRect(selection), merges);
}
