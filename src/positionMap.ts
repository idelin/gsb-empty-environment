import { Bias, PositionMap, Range, TextEdit } from './types';

/**
 * A segment of the old text and where it landed in the new text.
 * `atomic` segments come from edits: offsets strictly inside them have no
 * meaningful linear correspondent, so they collapse to a boundary by bias.
 */
interface Segment {
  oldStart: number;
  oldEnd: number;
  newStart: number;
  newEnd: number;
  atomic: boolean;
}

/**
 * Build a PositionMap from a list of non-overlapping edits (any order)
 * applied simultaneously against a frozen old text of length `oldLength`.
 */
export function buildPositionMap(oldLength: number, edits: TextEdit[]): PositionMap {
  const sorted = [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
  const segments: Segment[] = [];
  let oldCursor = 0;
  let newCursor = 0;
  for (const edit of sorted) {
    if (edit.start < oldCursor) {
      throw new Error(`overlapping edits at offset ${edit.start}`);
    }
    if (edit.start > oldCursor) {
      const len = edit.start - oldCursor;
      segments.push({
        oldStart: oldCursor,
        oldEnd: edit.start,
        newStart: newCursor,
        newEnd: newCursor + len,
        atomic: false,
      });
      newCursor += len;
    }
    segments.push({
      oldStart: edit.start,
      oldEnd: edit.end,
      newStart: newCursor,
      newEnd: newCursor + edit.newText.length,
      atomic: true,
    });
    newCursor += edit.newText.length;
    oldCursor = edit.end;
  }
  if (oldCursor < oldLength) {
    segments.push({
      oldStart: oldCursor,
      oldEnd: oldLength,
      newStart: newCursor,
      newEnd: newCursor + (oldLength - oldCursor),
      atomic: false,
    });
    newCursor += oldLength - oldCursor;
  }
  const newLength = newCursor;

  function mapOffset(
    pos: number,
    bias: Bias,
    fromStart: 'oldStart' | 'newStart',
    fromEnd: 'oldEnd' | 'newEnd',
    toStart: 'oldStart' | 'newStart',
    toEnd: 'oldEnd' | 'newEnd',
    fromLength: number,
    toLength: number,
  ): number {
    if (pos < 0 || pos > fromLength) {
      throw new RangeError(`offset ${pos} out of range [0, ${fromLength}]`);
    }
    // Zero-width edits (pure insertions) claim their point first; the bias
    // decides whether the offset lands before or after the inserted text.
    for (const seg of segments) {
      if (seg.atomic && seg[fromStart] === seg[fromEnd] && pos === seg[fromStart]) {
        return bias === 'before' ? seg[toStart] : seg[toEnd];
      }
    }
    for (const seg of segments) {
      const s = seg[fromStart];
      const e = seg[fromEnd];
      if (pos < s) break;
      if (pos >= s && pos <= e) {
        if (!seg.atomic) return seg[toStart] + (pos - s);
        if (pos === s) return seg[toStart];
        if (pos === e) return seg[toEnd];
        return bias === 'before' ? seg[toStart] : seg[toEnd];
      }
    }
    return toLength;
  }

  function mapRange(start: number, end: number, forward: boolean): Range {
    if (start > end) throw new RangeError(`invalid range [${start}, ${end})`);
    const map = forward
      ? (p: number, b: Bias) => mapOffset(p, b, 'oldStart', 'oldEnd', 'newStart', 'newEnd', oldLength, newLength)
      : (p: number, b: Bias) => mapOffset(p, b, 'newStart', 'newEnd', 'oldStart', 'oldEnd', newLength, oldLength);
    return { start: map(start, 'before'), end: map(end, 'after') };
  }

  return {
    oldLength,
    newLength,
    oldToNew: (offset, bias = 'after') =>
      mapOffset(offset, bias, 'oldStart', 'oldEnd', 'newStart', 'newEnd', oldLength, newLength),
    newToOld: (offset, bias = 'after') =>
      mapOffset(offset, bias, 'newStart', 'newEnd', 'oldStart', 'oldEnd', newLength, oldLength),
    oldRangeToNew: (range) => mapRange(range.start, range.end, true),
    newRangeToOld: (range) => mapRange(range.start, range.end, false),
  };
}
