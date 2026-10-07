import type { BoundaryAffinity, Range, RangeAffinity, TextEdit } from "./types.ts";

export type RangeMapping = { readonly valid: true; readonly range: Range } | { readonly valid: false };

/**
 * Map a single offset through one edit.
 *
 * Precondition: the offset is not strictly inside the edit's deleted span
 * (such a position no longer exists; use mapRangeThroughEdit for ranges,
 * which reports that situation as invalid).
 */
export function mapOffsetThroughEdit(
  offset: number,
  edit: TextEdit,
  affinity: BoundaryAffinity,
): number {
  const removed = edit.end - edit.start;
  const delta = edit.text.length - removed;

  if (offset < edit.start) return offset;
  if (offset > edit.end) return offset + delta;

  // offset lies within [edit.start, edit.end].
  if (removed === 0) {
    // Pure insertion exactly at the offset: affinity decides the side.
    return affinity === "after" ? offset + edit.text.length : offset;
  }
  // Boundary of a deleted/replaced span: clamp to the corresponding edge
  // of the replacement text.
  if (offset === edit.start) {
    return affinity === "after" ? edit.start + edit.text.length : edit.start;
  }
  // offset === edit.end
  return affinity === "before" ? edit.start : edit.start + edit.text.length;
}

/**
 * Map a range through one edit.
 *
 * Invalidation rules (an invalidated range means the completion computed
 * against it is stale and must be discarded):
 * - a deletion/replacement whose removed span intersects [range.start, range.end)
 *   destroys content the range referred to;
 * - an insertion strictly inside the range touches its interior.
 *
 * Insertions exactly at a boundary never invalidate; they are mapped
 * according to that boundary's affinity.
 */
export function mapRangeThroughEdit(
  target: Range,
  edit: TextEdit,
  affinity: RangeAffinity,
): RangeMapping {
  const isInsertion = edit.start === edit.end;

  if (isInsertion) {
    if (target.start < edit.start && edit.start < target.end) {
      return { valid: false };
    }
  } else if (edit.start < target.end && edit.end > target.start) {
    // Removed span intersects the range content. For an empty target range
    // this also catches deletions strictly spanning the insertion point.
    return { valid: false };
  }

  const start = mapOffsetThroughEdit(target.start, edit, affinity.start);
  const end = mapOffsetThroughEdit(target.end, edit, affinity.end);
  return { valid: true, range: { start, end } };
}
