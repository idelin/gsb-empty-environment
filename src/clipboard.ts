import type { MergeMap } from "./merge.ts";
import { rectSize, type Pos, type Rect } from "./types.ts";

/**
 * Copy a (merge-expanded) rect to a matrix of cell values.
 *
 * The output keeps the rectangular structure of the source: one row
 * per grid row, one slot per grid column. Region anchors contribute
 * their value once; covered cells contribute an empty placeholder so
 * pasting back into a grid lines up column-for-column.
 */
export function copyRect(
  rect: Rect,
  merges: MergeMap,
  getValue: (pos: Pos) => string,
): string[][] {
  const { rows, cols } = rectSize(rect);
  const matrix: string[][] = [];
  for (let r = 0; r < rows; r++) {
    const line: string[] = [];
    for (let c = 0; c < cols; c++) {
      const pos = { row: rect.top + r, col: rect.left + c };
      if (merges.isCovered(pos)) {
        line.push(""); // empty placeholder keeps the rectangle intact
      } else {
        line.push(getValue(pos));
      }
    }
    matrix.push(line);
  }
  return matrix;
}

/** Tab-separated serialization suitable for the system clipboard. */
export function toTSV(matrix: string[][]): string {
  return matrix.map((line) => line.join("\t")).join("\n");
}
