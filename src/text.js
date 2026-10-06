/**
 * Text utilities: line index and edit application.
 * Positions are 1-based { line, column }; offsets are 0-based.
 */

export class LineIndex {
  constructor(text) {
    this.starts = [0];
    for (let i = 0; i < text.length; i++) {
      if (text.charCodeAt(i) === 10) this.starts.push(i + 1);
    }
  }

  offsetToPosition(offset) {
    const clamped = Math.max(0, Math.min(offset, Number.MAX_SAFE_INTEGER));
    let lo = 0;
    let hi = this.starts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.starts[mid] <= clamped) lo = mid;
      else hi = mid - 1;
    }
    return { line: lo + 1, column: clamped - this.starts[lo] + 1 };
  }

  positionToOffset(line, column) {
    const lineIdx = Math.max(0, Math.min(line - 1, this.starts.length - 1));
    const start = this.starts[lineIdx];
    const end = lineIdx + 1 < this.starts.length ? this.starts[lineIdx + 1] - 1 : Number.MAX_SAFE_INTEGER;
    return Math.max(start, Math.min(start + column - 1, end));
  }
}

/** A text edit: replace [start, end) with `text`. Offsets are 0-based, half-open. */
export function applyEditToText(text, edit) {
  return text.slice(0, edit.start) + edit.text + text.slice(edit.end);
}

/**
 * Apply a batch of edits expressed in the coordinate system of `text`.
 * Edits are applied from highest start offset to lowest, so they must not overlap.
 */
export function applyEditsToText(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start);
  let out = text;
  for (const edit of sorted) out = applyEditToText(out, edit);
  return out;
}

/** Map an offset in the pre-edit text to the post-edit text for a single edit. */
export function mapOffsetThroughEdit(offset, edit) {
  if (offset < edit.start) return offset;
  if (offset > edit.end) return offset + (edit.text.length - (edit.end - edit.start));
  // Inside the replaced range: clamp to the end of the replacement.
  return edit.start + edit.text.length;
}

/** Map an offset through a batch of edits (pre-edit coordinates, non-overlapping). */
export function mapOffsetThroughEdits(offset, edits) {
  const sorted = [...edits].sort((a, b) => a.start - b.start);
  let delta = 0;
  for (const edit of sorted) {
    if (offset < edit.start) break;
    if (offset > edit.end) {
      delta += edit.text.length - (edit.end - edit.start);
    } else {
      return edit.start + delta + edit.text.length;
    }
  }
  return offset + delta;
}

/** Half-open range intersection test. Insertions (start === end) only count when strictly inside. */
export function rangesIntersect(aStart, aEnd, bStart, bEnd) {
  if (aStart === aEnd) return bStart < aStart && aStart < bEnd;
  if (bStart === bEnd) return aStart < bStart && bStart < aEnd;
  return aStart < bEnd && bStart < aEnd;
}
