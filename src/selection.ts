import type { CaretPosition, Rect } from './types.ts';
import type { LayoutLine, PositionedRun } from './layout.ts';

/**
 * Visual x of a logical offset inside one run. Offsets inside a glyph
 * cluster (e.g. half of a ligature) are interpolated linearly across the
 * glyph box; RTL runs mirror the interpolation.
 */
export function offsetXInRun(run: PositionedRun, offset: number): number {
  const clamped = Math.min(Math.max(offset, run.logicalStart), run.logicalEnd);
  if (run.glyphs.length === 0) {
    return run.x;
  }
  if (clamped <= run.logicalStart) {
    return run.direction === 'ltr' ? run.x : run.x + run.width;
  }
  if (clamped >= run.logicalEnd) {
    return run.direction === 'ltr' ? run.x + run.width : run.x;
  }
  const glyph =
    run.glyphs.find((g) => clamped >= g.start && clamped < g.end) ??
    run.glyphs[run.glyphs.length - 1]!;
  const span = glyph.end - glyph.start;
  const ratio = span > 0 ? (clamped - glyph.start) / span : 0;
  return run.direction === 'ltr'
    ? glyph.x + ratio * glyph.width
    : glyph.x + glyph.width - ratio * glyph.width;
}

/**
 * Visual rectangles covering the selection between `anchor` and `focus`.
 * Because a logically contiguous selection can be visually non-contiguous
 * across bidi runs, the result is one rect per covered run, in visual
 * (left-to-right) order; the rects may be disjoint.
 */
export function selectionRects(
  line: LayoutLine,
  anchor: CaretPosition,
  focus: CaretPosition,
): Rect[] {
  const start = Math.min(anchor.offset, focus.offset);
  const end = Math.max(anchor.offset, focus.offset);
  if (start === end) {
    return [];
  }
  const rects: Rect[] = [];
  for (const run of line.runs) {
    const s = Math.max(start, run.logicalStart);
    const e = Math.min(end, run.logicalEnd);
    if (s >= e) {
      continue;
    }
    const x1 = offsetXInRun(run, s);
    const x2 = offsetXInRun(run, e);
    if (x1 === x2) {
      continue;
    }
    rects.push({ x: Math.min(x1, x2), width: Math.abs(x2 - x1) });
  }
  return rects;
}
