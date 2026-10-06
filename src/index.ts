export type {
  Affinity,
  CaretPosition,
  Direction,
  Glyph,
  GlyphRun,
  Rect,
} from './types.ts';
export {
  layoutLine,
  type GlyphPosition,
  type LayoutLine,
  type PositionedRun,
} from './layout.ts';
export { caretStops, caretX, hitTest, type CaretStop } from './hitTest.ts';
export { offsetXInRun, selectionRects } from './selection.ts';
