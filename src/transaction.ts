/**
 * Core edit algebra for atomic multi-cursor transactions.
 *
 * Every edit staged in one transaction is interpreted in the SAME baseline
 * coordinate system: the document as it was when the transaction started.
 * Edits are only materialized at commit time, applied from the end of the
 * document backwards, so a length-changing replacement never shifts the
 * baseline ranges of the other edits.
 */

import { assertRange } from './offsets.ts';

export interface Range {
  start: number;
  end: number;
}

export interface Edit {
  range: Range;
  /** Replacement text (empty string for a pure deletion). */
  text: string;
}

export class EditConflictError extends Error {
  readonly edits: readonly [Edit, Edit];

  constructor(message: string, edits: readonly [Edit, Edit]) {
    super(message);
    this.name = 'EditConflictError';
    this.edits = edits;
  }
}

/** Validate every edit's range against the baseline text. */
export function validateEdits(baseline: string, edits: readonly Edit[]): void {
  for (const edit of edits) {
    assertRange(baseline, edit.range.start, edit.range.end, 'edit.range');
  }
}

function rangesOverlap(a: Range, b: Range): boolean {
  return a.start < b.end && b.start < a.end;
}

/**
 * Two staged edits conflict when:
 *  - their non-empty ranges intersect, or
 *  - an insertion point lies strictly inside another edit's range, or
 *  - two insertions target the same offset (ambiguous ordering).
 *
 * An insertion exactly at the boundary of another edit's range is legal and
 * has a deterministic outcome (see applyEdits).
 */
export function findConflict(
  edits: readonly Edit[],
): [Edit, Edit] | undefined {
  const sorted = [...edits].sort(
    (a, b) => a.range.start - b.range.start || a.range.end - b.range.end,
  );
  for (let i = 0; i < sorted.length; i++) {
    for (let j = i + 1; j < sorted.length; j++) {
      const a = sorted[i];
      const b = sorted[j];
      if (b.range.start > a.range.end) break;
      const aEmpty = a.range.start === a.range.end;
      const bEmpty = b.range.start === b.range.end;
      if (aEmpty && bEmpty) {
        if (a.range.start === b.range.start) return [a, b];
        continue;
      }
      if (!aEmpty && !bEmpty) {
        if (rangesOverlap(a.range, b.range)) return [a, b];
        continue;
      }
      const point = aEmpty ? a.range : b.range;
      const span = aEmpty ? b.range : a.range;
      if (span.start < point.start && point.start < span.end) return [a, b];
    }
  }
  return undefined;
}

/** Throw EditConflictError if any two staged edits conflict. */
export function assertNoConflicts(edits: readonly Edit[]): void {
  const conflict = findConflict(edits);
  if (conflict) {
    const [a, b] = conflict;
    throw new EditConflictError(
      `conflicting edits: [${a.range.start}, ${a.range.end}) vs ` +
        `[${b.range.start}, ${b.range.end})`,
      [a, b],
    );
  }
}

/**
 * Apply non-conflicting baseline edits to `baseline`, returning the new text
 * plus the inverse edits (in baseline-of-the-new-text coordinates) needed to
 * undo the change.
 *
 * Edits are applied in descending baseline-offset order, so replacements
 * later in the document cannot disturb the ranges of earlier ones.
 */
export function applyEdits(
  baseline: string,
  edits: readonly Edit[],
): { text: string; inverse: Edit[] } {
  assertNoConflicts(edits);
  // Apply in descending baseline-offset order so that a length-changing
  // replacement later in the document cannot disturb the baseline ranges
  // of the earlier ones.
  const descending = [...edits].sort(
    (a, b) => b.range.start - a.range.start || b.range.end - a.range.end,
  );
  let text = baseline;
  for (const edit of descending) {
    text =
      text.slice(0, edit.range.start) + edit.text + text.slice(edit.range.end);
  }
  // Inverse edits must be expressed in the coordinate system of the NEW
  // text: walk the edits in ascending baseline order, accumulating the
  // length delta of everything before each edit.
  const ascending = [...edits].sort(
    (a, b) => a.range.start - b.range.start || a.range.end - b.range.end,
  );
  let shift = 0;
  const inverse: Edit[] = [];
  for (const edit of ascending) {
    inverse.push({
      range: {
        start: edit.range.start + shift,
        end: edit.range.start + shift + edit.text.length,
      },
      text: baseline.slice(edit.range.start, edit.range.end),
    });
    shift += edit.text.length - (edit.range.end - edit.range.start);
  }
  return { text, inverse };
}

export type Association = 'start' | 'end';

/**
 * Map a baseline offset through a set of committed edits into the coordinate
 * system of the resulting text.
 *
 *  - Offsets before all edits are unchanged; offsets after an edit shift by
 *    that edit's length delta.
 *  - `association: 'start'` (default) keeps an offset at a replaced region's
 *    boundary attached to the text before it; `'end'` attaches it to the
 *    text after the replacement.
 *  - Offsets strictly inside a replaced region clamp to the region's start
 *    (or to the end of the replacement text with `'end'`).
 */
export function mapOffset(
  offset: number,
  edits: readonly Edit[],
  association: Association = 'start',
): number {
  const sorted = [...edits].sort(
    (a, b) => a.range.start - b.range.start || a.range.end - b.range.end,
  );
  let shift = 0;
  for (const edit of sorted) {
    const { start, end } = edit.range;
    const delta = edit.text.length - (end - start);
    if (offset < start) break;
    if (offset === start) {
      if (association === 'end') return start + shift + edit.text.length;
      return start + shift;
    }
    if (offset < end) {
      if (association === 'end') return start + shift + edit.text.length;
      return start + shift;
    }
    if (offset === end) {
      // Boundary just after a non-empty replaced region follows the text.
      return start + shift + edit.text.length;
    }
    shift += delta;
  }
  return offset + shift;
}

/** Map a baseline range through committed edits. */
export function mapRange(range: Range, edits: readonly Edit[]): Range {
  return {
    start: mapOffset(range.start, edits, 'start'),
    end: mapOffset(range.end, edits, 'end'),
  };
}
