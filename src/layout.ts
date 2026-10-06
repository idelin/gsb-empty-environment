import type { Direction, GlyphRun } from './types.ts';

/** One row of the per-glyph position table. */
export interface GlyphPosition {
  readonly runIndex: number;
  readonly glyphIndex: number;
  readonly direction: Direction;
  readonly start: number;
  readonly end: number;
  /** Visual x of the glyph box's left edge. */
  readonly x: number;
  readonly width: number;
}

export interface PositionedRun {
  readonly runIndex: number;
  readonly direction: Direction;
  readonly x: number;
  readonly width: number;
  readonly logicalStart: number;
  readonly logicalEnd: number;
  /** Glyphs in logical order, each with its visual box. */
  readonly glyphs: readonly GlyphPosition[];
}

export interface LayoutLine {
  readonly x: number;
  readonly width: number;
  /** Runs in visual (left-to-right screen) order. */
  readonly runs: readonly PositionedRun[];
  /** Flat per-glyph position table, in logical order per run. */
  readonly positions: readonly GlyphPosition[];
}

/**
 * Lay out a single line. Runs are placed side by side in the order given
 * (the caller has already resolved bidi run order). Inside an RTL run the
 * first logical glyph sits at the run's right edge.
 */
export function layoutLine(runs: readonly GlyphRun[], originX = 0): LayoutLine {
  const positionedRuns: PositionedRun[] = [];
  const positions: GlyphPosition[] = [];
  let penX = originX;
  let logicalCursor = 0;

  runs.forEach((run, runIndex) => {
    const width = run.glyphs.reduce((sum, glyph) => sum + glyph.width, 0);
    const glyphs: GlyphPosition[] = [];
    let advanced = 0;
    let logicalStart = Infinity;
    let logicalEnd = -Infinity;

    run.glyphs.forEach((glyph, glyphIndex) => {
      const x =
        run.direction === 'ltr'
          ? penX + advanced
          : penX + width - advanced - glyph.width;
      advanced += glyph.width;
      logicalStart = Math.min(logicalStart, glyph.start);
      logicalEnd = Math.max(logicalEnd, glyph.end);
      const position: GlyphPosition = {
        runIndex,
        glyphIndex,
        direction: run.direction,
        start: glyph.start,
        end: glyph.end,
        x,
        width: glyph.width,
      };
      glyphs.push(position);
      positions.push(position);
    });

    if (run.glyphs.length === 0) {
      logicalStart = logicalCursor;
      logicalEnd = logicalCursor;
    }
    logicalCursor = Math.max(logicalCursor, logicalEnd);

    positionedRuns.push({
      runIndex,
      direction: run.direction,
      x: penX,
      width,
      logicalStart,
      logicalEnd,
      glyphs,
    });
    penX += width;
  });

  return { x: originX, width: penX - originX, runs: positionedRuns, positions };
}
