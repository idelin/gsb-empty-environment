import type { Direction, GlyphRun, TextLine } from './types.ts';

/** One row of the per-glyph position table. */
export interface GlyphPosition {
  runIndex: number;
  glyphIndex: number;
  direction: Direction;
  /** Logical character range [start, end). */
  start: number;
  end: number;
  /** Visual x of the glyph's left edge. */
  x: number;
  width: number;
  /** Logical offset associated with the glyph's visual left edge. */
  leftOffset: number;
  /** Logical offset associated with the glyph's visual right edge. */
  rightOffset: number;
}

/** Result of laying out a line: the position table plus metrics. */
export interface LineLayout {
  /** Glyphs in visual order (sorted by x, ties broken by logical start). */
  glyphs: GlyphPosition[];
  /** Total advance width of the line. */
  width: number;
  /** Logical text length (maximum glyph end offset). */
  length: number;
}

function runWidth(run: GlyphRun): number {
  let width = 0;
  for (const glyph of run.glyphs) width += glyph.width;
  return width;
}

/**
 * Build the per-glyph position table for a line.
 *
 * Runs are placed left to right in the order given. Within an LTR run
 * glyphs advance left to right in logical order; within an RTL run the
 * first logical glyph is rightmost. For each glyph, `leftOffset` and
 * `rightOffset` record which logical offset sits on each visual edge,
 * which is what makes RTL mapping and boundary affinity explicit.
 */
export function layoutLine(line: TextLine): LineLayout {
  const glyphs: GlyphPosition[] = [];
  let runX = 0;
  let length = 0;

  line.runs.forEach((run, runIndex) => {
    const width = runWidth(run);
    let x = run.direction === 'ltr' ? runX : runX + width;
    run.glyphs.forEach((glyph, glyphIndex) => {
      if (run.direction === 'rtl') x -= glyph.width;
      const ltr = run.direction === 'ltr';
      glyphs.push({
        runIndex,
        glyphIndex,
        direction: run.direction,
        start: glyph.start,
        end: glyph.end,
        x,
        width: glyph.width,
        leftOffset: ltr ? glyph.start : glyph.end,
        rightOffset: ltr ? glyph.end : glyph.start,
      });
      if (ltr) x += glyph.width;
      if (glyph.end > length) length = glyph.end;
    });
    runX += width;
  });

  glyphs.sort((a, b) => a.x - b.x || a.start - b.start);
  return { glyphs, width: runX, length };
}
