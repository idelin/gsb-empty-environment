import { fixtureGlyphs, measureText, type GlyphMetrics } from "./fixtures/glyphs.ts";
import type { GridNode } from "./fixtures/nodeTree.ts";
import type { Pos } from "./types.ts";

/**
 * Pure layout engine: derives grid geometry from the node-tree fixture
 * and the glyph-metrics fixture. No browser layout engine involved.
 */
export interface GridGeometry {
  rows: number;
  cols: number;
  /** x offset of each column's left edge, length cols + 1. */
  columnOffsets: number[];
  /** y offset of each row's top edge, length rows + 1. */
  rowOffsets: number[];
}

export function computeLayout(
  grid: GridNode,
  metrics: GlyphMetrics = fixtureGlyphs,
): GridGeometry {
  const rows = grid.children.length;
  const cols = Math.max(0, ...grid.children.map((r) => r.children.length));

  const colWidths = new Array<number>(cols).fill(0);
  for (const rowNode of grid.children) {
    rowNode.children.forEach((cellNode, col) => {
      const contentWidth = measureText(cellNode.text, metrics) + metrics.cellPaddingX * 2;
      colWidths[col] = Math.max(colWidths[col], contentWidth);
    });
  }

  const columnOffsets = [0];
  for (const w of colWidths) columnOffsets.push(columnOffsets[columnOffsets.length - 1] + w);

  const rowHeight = metrics.lineHeight + metrics.cellPaddingY * 2;
  const rowOffsets = [0];
  for (let r = 0; r < rows; r++) rowOffsets.push(rowOffsets[r] + rowHeight);

  return { rows, cols, columnOffsets, rowOffsets };
}

/** Hit-test a pixel point against the computed geometry. */
export function hitTest(geometry: GridGeometry, x: number, y: number): Pos | null {
  const col = findInterval(geometry.columnOffsets, x);
  const row = findInterval(geometry.rowOffsets, y);
  if (row < 0 || col < 0) return null;
  return { row, col };
}

function findInterval(offsets: number[], value: number): number {
  for (let i = 0; i < offsets.length - 1; i++) {
    if (value >= offsets[i] && value < offsets[i + 1]) return i;
  }
  return -1;
}
