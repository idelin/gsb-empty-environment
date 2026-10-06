/** Text direction of a glyph run. */
export type Direction = 'ltr' | 'rtl';

/**
 * A single glyph: a logical character range plus its advance width.
 * `start`/`end` are logical character offsets into the line's text.
 * `width` may be 0 (zero-width glyphs such as combining marks).
 */
export interface Glyph {
  start: number;
  end: number;
  width: number;
}

/** A run of same-direction glyphs, stored in logical order. */
export interface GlyphRun {
  direction: Direction;
  glyphs: Glyph[];
}

/**
 * A simplified text line: glyph runs in *visual* order, laid out
 * left to right starting at x = 0.
 */
export interface TextLine {
  runs: GlyphRun[];
}

/** Which visual edge of a glyph/boundary a caret belongs to. */
export type Affinity = 'left' | 'right';

/**
 * A logical insertion point. `affinity` disambiguates positions that
 * share the same visual x (e.g. both sides of a direction boundary).
 */
export interface CaretPosition {
  offset: number;
  affinity: Affinity;
}

/** An axis-aligned rectangle in line coordinates. */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}
