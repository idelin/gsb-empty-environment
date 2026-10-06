/** Direction of a glyph run. */
export type Direction = 'ltr' | 'rtl';

/**
 * Visual caret affinity. When one logical offset owns two visual caret
 * positions (typical at bidi run boundaries), `left` picks the smaller
 * visual x and `right` picks the larger one.
 */
export type Affinity = 'left' | 'right';

/**
 * A shaped glyph (or cluster). `start`/`end` are logical character offsets
 * into the line's text, `width` is the advance in pixels. Zero-width glyphs
 * (e.g. combining marks) have `width === 0`.
 */
export interface Glyph {
  readonly start: number;
  readonly end: number;
  readonly width: number;
}

/**
 * A run of glyphs sharing one direction. Glyphs are listed in logical
 * (reading) order; the run itself is positioned in visual order relative
 * to its sibling runs.
 */
export interface GlyphRun {
  readonly direction: Direction;
  readonly glyphs: readonly Glyph[];
}

/** A logical insertion point plus the visual side it leans toward. */
export interface CaretPosition {
  readonly offset: number;
  readonly affinity: Affinity;
}

/** A horizontal selection fragment. */
export interface Rect {
  readonly x: number;
  readonly width: number;
}
