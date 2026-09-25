import { measureCellContent } from './glyphs.ts';
import type { Coord, GlyphMetrics, GridCell, Rect, TableNode } from './types.ts';

/**
 * Grid model built purely from the node-tree fixture and glyph metrics.
 * Placement follows the HTML table auto-placement algorithm; column widths
 * and row heights are computed here, not by a browser layout engine.
 */
export class Grid {
  readonly rowCount: number;
  readonly colCount: number;
  readonly cells: GridCell[];
  readonly columnWidths: number[];
  readonly rowHeights: number[];
  /** owner[row][col] -> the cell covering that slot (null for holes). */
  private readonly owner: (GridCell | null)[][];

  constructor(
    rowCount: number,
    colCount: number,
    cells: GridCell[],
    columnWidths: number[],
    rowHeights: number[],
    owner: (GridCell | null)[][],
  ) {
    this.rowCount = rowCount;
    this.colCount = colCount;
    this.cells = cells;
    this.columnWidths = columnWidths;
    this.rowHeights = rowHeights;
    this.owner = owner;
  }

  contains(row: number, col: number): boolean {
    return row >= 0 && row < this.rowCount && col >= 0 && col < this.colCount;
  }

  /** The cell covering a slot, or null for a hole. */
  ownerAt(row: number, col: number): GridCell | null {
    if (!this.contains(row, col)) return null;
    return this.owner[row][col];
  }

  /** Anchor coordinate of the region covering a slot, or null for holes. */
  anchorAt(row: number, col: number): Coord | null {
    const cell = this.ownerAt(row, col);
    return cell ? cell.anchor : null;
  }

  /** The cell whose anchor is exactly (row, col), else null (covered slot). */
  cellAt(row: number, col: number): GridCell | null {
    const cell = this.ownerAt(row, col);
    if (cell && cell.anchor.row === row && cell.anchor.col === col) return cell;
    return null;
  }

  /** True when the slot is inside a merged region but is not its anchor. */
  isCovered(row: number, col: number): boolean {
    const cell = this.ownerAt(row, col);
    return cell !== null && (cell.anchor.row !== row || cell.anchor.col !== col);
  }

  regionOf(cell: GridCell): Rect {
    return {
      top: cell.anchor.row,
      left: cell.anchor.col,
      bottom: cell.anchor.row + cell.rowSpan - 1,
      right: cell.anchor.col + cell.colSpan - 1,
    };
  }

  /** All merged (span > 1) regions, in placement order. */
  mergedRegions(): Rect[] {
    return this.cells
      .filter((cell) => cell.rowSpan > 1 || cell.colSpan > 1)
      .map((cell) => this.regionOf(cell));
  }

  bounds(): Rect {
    return { top: 0, left: 0, bottom: this.rowCount - 1, right: this.colCount - 1 };
  }
}

/** Build a Grid from the node-tree fixture and glyph-metric fixture. */
export function buildGrid(table: TableNode, metrics: GlyphMetrics): Grid {
  const occupied: boolean[][] = [];
  const placed: Array<{ node: TableNode['rows'][number]['cells'][number]; anchor: Coord; rowSpan: number; colSpan: number }> = [];

  const ensureSlot = (row: number, col: number): void => {
    while (occupied.length <= row) occupied.push([]);
    while (occupied[row].length <= col) occupied[row].push(false);
  };

  let colCount = 0;
  table.rows.forEach((rowNode, row) => {
    let col = 0;
    for (const cellNode of rowNode.cells) {
      ensureSlot(row, col);
      while (occupied[row][col]) {
        col += 1;
        ensureSlot(row, col);
      }
      const rowSpan = Math.max(1, cellNode.rowSpan ?? 1);
      const colSpan = Math.max(1, cellNode.colSpan ?? 1);
      for (let r = row; r < row + rowSpan; r += 1) {
        for (let c = col; c < col + colSpan; c += 1) {
          ensureSlot(r, c);
          occupied[r][c] = true;
        }
      }
      placed.push({ node: cellNode, anchor: { row, col }, rowSpan, colSpan });
      colCount = Math.max(colCount, col + colSpan);
      col += colSpan;
    }
  });
  const rowCount = occupied.length;

  // Column widths: single-column cells first, then distribute any deficit of
  // a spanned cell onto the last column it spans (deterministic rule).
  const columnWidths = new Array<number>(colCount).fill(0);
  for (const p of placed) {
    if (p.colSpan === 1) {
      const need = measureCellContent(p.node.text, metrics);
      columnWidths[p.anchor.col] = Math.max(columnWidths[p.anchor.col], need);
    }
  }
  for (const p of placed) {
    if (p.colSpan > 1) {
      const need = measureCellContent(p.node.text, metrics);
      const first = p.anchor.col;
      const last = p.anchor.col + p.colSpan - 1;
      let have = 0;
      for (let c = first; c <= last; c += 1) have += columnWidths[c];
      if (have < need) columnWidths[last] += need - have;
    }
  }

  // Row heights: uniform from metrics (line height + vertical padding).
  const rowHeight = metrics.lineHeight + 2 * metrics.cellPaddingY;
  const rowHeights = new Array<number>(rowCount).fill(rowHeight);

  const colX: number[] = [];
  const rowY: number[] = [];
  {
    let acc = 0;
    for (const w of columnWidths) {
      colX.push(acc);
      acc += w;
    }
    acc = 0;
    for (const h of rowHeights) {
      rowY.push(acc);
      acc += h;
    }
  }

  const cells: GridCell[] = placed.map((p, id) => {
    const lastCol = p.anchor.col + p.colSpan - 1;
    const lastRow = p.anchor.row + p.rowSpan - 1;
    const width = columnWidths.slice(p.anchor.col, lastCol + 1).reduce((a, b) => a + b, 0);
    const height = rowHeights.slice(p.anchor.row, lastRow + 1).reduce((a, b) => a + b, 0);
    return {
      id,
      text: p.node.text,
      anchor: p.anchor,
      rowSpan: p.rowSpan,
      colSpan: p.colSpan,
      x: colX[p.anchor.col],
      y: rowY[p.anchor.row],
      width,
      height,
    };
  });

  const owner: (GridCell | null)[][] = Array.from({ length: rowCount }, () =>
    new Array<GridCell | null>(colCount).fill(null),
  );
  for (const cell of cells) {
    for (let r = cell.anchor.row; r < cell.anchor.row + cell.rowSpan; r += 1) {
      for (let c = cell.anchor.col; c < cell.anchor.col + cell.colSpan; c += 1) {
        owner[r][c] = cell;
      }
    }
  }

  return new Grid(rowCount, colCount, cells, columnWidths, rowHeights, owner);
}
