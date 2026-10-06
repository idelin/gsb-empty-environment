import { Affinity, Edit, Selection, SelectionAffinities } from './types';

/**
 * Map a boundary position through a single edit.
 *
 * Rules:
 *  - Position before the edit: unchanged.
 *  - Position after the edit: shifted by (inserted length - removed length).
 *  - Pure insertion exactly at the position: affinity decides the side.
 *  - Position inside or at the edges of a deleted/replaced range
 *    (i.e. covered by the deletion): snaps to the deletion start.
 */
export function transformPosition(pos: number, edit: Edit, affinity: Affinity = 'right'): number {
  const { start, end, text } = edit;
  const removed = end - start;
  const delta = text.length - removed;

  if (pos < start) return pos;
  if (pos > end) return pos + delta;

  // start <= pos <= end
  if (removed === 0) {
    // Pure insertion at `start`; pos === start === end.
    return affinity === 'left' ? pos : pos + text.length;
  }
  // Covered by a deletion/replacement: snap to the deletion start.
  return start;
}

function isInsert(edit: Edit): boolean {
  return edit.start === edit.end;
}

/**
 * Stable sort into application order: ascending by start; at the same
 * boundary, insertions come before deletions/replacements. Applying the
 * reversed (descending) sequence then (a) deletes original text before
 * inserting at the same boundary, so deletion ranges stay valid, and
 * (b) applies stacked same-point insertions in reverse given order, so the
 * final text keeps the given order.
 */
export function sortEdits(edits: readonly Edit[]): Edit[] {
  return edits
    .map((edit, index) => ({ edit, index }))
    .sort((a, b) => {
      if (a.edit.start !== b.edit.start) return a.edit.start - b.edit.start;
      const aIns = isInsert(a.edit) ? 0 : 1;
      const bIns = isInsert(b.edit) ? 0 : 1;
      return aIns - bIns || a.index - b.index;
    })
    .map(({ edit }) => edit);
}

/**
 * Validate that a batch of edits is well formed: every range is
 * non-negative with start <= end, and no two edits overlap. Edits may
 * touch (adjacent deletions), multiple insertions may share the same
 * point, and an insertion may sit exactly at a deletion's boundary; an
 * insertion strictly inside another edit's range is rejected.
 */
export function validateEdits(edits: readonly Edit[]): void {
  const sorted = sortEdits(edits);
  for (const e of sorted) {
    if (!Number.isInteger(e.start) || !Number.isInteger(e.end) || e.start < 0 || e.end < e.start) {
      throw new RangeError(`Invalid edit range [${e.start}, ${e.end})`);
    }
  }
  for (let i = 1; i < sorted.length; i++) {
    const prev = sorted[i - 1];
    const curr = sorted[i];
    let overlap: boolean;
    if (isInsert(curr)) {
      // An insertion only overlaps a non-insertion strictly containing it.
      overlap = !isInsert(prev) && prev.start < curr.start && curr.start < prev.end;
    } else if (isInsert(prev)) {
      // Insertion at this edit's start boundary: adjacent, not overlapping.
      overlap = false;
    } else {
      overlap = curr.start < prev.end;
    }
    if (overlap) {
      throw new RangeError(
        `Overlapping edits: [${prev.start}, ${prev.end}) and [${curr.start}, ${curr.end})`,
      );
    }
  }
}

/**
 * Apply a batch of edits (given in ORIGINAL coordinates, non-overlapping)
 * to a text, returning the new text.
 */
export function applyEdits(text: string, edits: readonly Edit[]): string {
  validateEdits(edits);
  for (const e of edits) {
    if (e.end > text.length) {
      throw new RangeError(`Edit [${e.start}, ${e.end}) exceeds text length ${text.length}`);
    }
  }
  let result = text;
  const descending = sortEdits(edits).reverse();
  for (const e of descending) {
    result = result.slice(0, e.start) + e.text + result.slice(e.end);
  }
  return result;
}

/**
 * Map a boundary position through a batch of edits (original coordinates).
 * Edits are applied right-to-left, which keeps each not-yet-applied edit's
 * original coordinates valid in the intermediate text.
 */
export function transformPositionThroughEdits(
  pos: number,
  edits: readonly Edit[],
  affinity: Affinity = 'right',
): number {
  validateEdits(edits);
  let result = pos;
  const descending = sortEdits(edits).reverse();
  for (const e of descending) {
    result = transformPosition(result, e, affinity);
  }
  return result;
}

/**
 * Transform both ends of a selection through a batch of edits.
 *
 * Defaults: the start uses 'right' affinity and the end uses 'left', so
 * text inserted exactly at a selection boundary is NOT pulled into the
 * selection. An empty selection (cursor) is transformed as a single
 * position with 'right' affinity so it stays empty and lands after text
 * inserted at the cursor.
 */
export function transformSelection(
  selection: Selection,
  edits: readonly Edit[],
  affinities: SelectionAffinities = {},
): Selection {
  const start = Math.min(selection.start, selection.end);
  const end = Math.max(selection.start, selection.end);
  if (start === end) {
    const pos = transformPositionThroughEdits(start, edits, 'right');
    return { start: pos, end: pos };
  }
  const startAffinity = affinities.start ?? 'right';
  const endAffinity = affinities.end ?? 'left';
  return {
    start: transformPositionThroughEdits(start, edits, startAffinity),
    end: transformPositionThroughEdits(end, edits, endAffinity),
  };
}
