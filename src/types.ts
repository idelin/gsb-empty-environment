/** Node-tree fixture input (a DOM-like table description). */
export interface CellNode {
  text: string;
  colSpan?: number;
  rowSpan?: number;
}

export interface RowNode {
  cells: CellNode[];
}

export interface TableNode {
  rows: RowNode[];
}

/** Glyph-metric fixture: the only source of text measurement. */
export interface GlyphMetrics {
  defaultAdvance: number;
  advances: Record<string, number>;
  lineHeight: number;
  cellPaddingX: number;
  cellPaddingY: number;
}

/** A grid coordinate (slot index, 0-based). */
export interface Coord {
  row: number;
  col: number;
}

/** Inclusive rectangle over grid slots. */
export interface Rect {
  top: number;
  left: number;
  bottom: number;
  right: number;
}

/** A placed cell: anchor slot, span, and laid-out pixel rect. */
export interface GridCell {
  id: number;
  text: string;
  anchor: Coord;
  rowSpan: number;
  colSpan: number;
  x: number;
  y: number;
  width: number;
  height: number;
}
