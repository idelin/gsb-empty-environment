import type { MergeMap } from "./merge.ts";
import type { Pos } from "./types.ts";

export type Direction = "up" | "down" | "left" | "right";

/**
 * Arrow-key navigation over a grid with merged cells.
 *
 * Invariants:
 *  - the active cell is always a plain cell or a region anchor;
 *  - a jump never lands on a covered cell;
 *  - leaving a merged region skips its whole span in one step;
 *  - moving into a region lands on its anchor;
 *  - moves that would leave the grid keep the current cell.
 */
export class Navigator {
  private readonly merges: MergeMap;
  private readonly rows: number;
  private readonly cols: number;

  constructor(merges: MergeMap, rows: number, cols: number) {
    this.merges = merges;
    this.rows = rows;
    this.cols = cols;
  }

  move(from: Pos, direction: Direction): Pos {
    const start = this.merges.resolveAnchor(from);
    const bounds = this.merges.boundsAt(start);

    // Stepping out of a region skips its entire span at once.
    let candidate: Pos;
    switch (direction) {
      case "up":
        candidate = { row: bounds.top - 1, col: start.col };
        break;
      case "down":
        candidate = { row: bounds.bottom + 1, col: start.col };
        break;
      case "left":
        candidate = { row: start.row, col: bounds.left - 1 };
        break;
      case "right":
        candidate = { row: start.row, col: bounds.right + 1 };
        break;
    }

    if (!this.inBounds(candidate)) return start;

    // Landing inside a region resolves to its anchor, so the active
    // cell is never a covered cell.
    return this.merges.resolveAnchor(candidate);
  }

  private inBounds(pos: Pos): boolean {
    return pos.row >= 0 && pos.row < this.rows && pos.col >= 0 && pos.col < this.cols;
  }
}
