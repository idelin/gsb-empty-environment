import { Fraction } from './fraction.ts';
import type { FractionLike } from './fraction.ts';

export interface Cell {
  x: number;
  y: number;
}

export interface Step {
  cell: Cell;
  /** Exact non-negative cost of entering `cell`. */
  cost: Fraction;
}

/**
 * Uniform 4-connected grid. Each cell has a non-negative rational cost
 * that is paid when entering the cell; `null` means blocked.
 * Zero-cost cells are allowed, so zero-cost cycles can occur.
 */
export class Grid {
  readonly width: number;
  readonly height: number;
  private readonly costs: (Fraction | null)[];

  constructor(rows: (FractionLike | null)[][]) {
    if (rows.length === 0 || rows[0].length === 0) {
      throw new Error('grid must be non-empty');
    }
    this.height = rows.length;
    this.width = rows[0].length;
    this.costs = new Array(this.width * this.height);
    for (let y = 0; y < this.height; y++) {
      if (rows[y].length !== this.width) {
        throw new Error('ragged grid row ' + y);
      }
      for (let x = 0; x < this.width; x++) {
        const raw = rows[y][x];
        if (raw === null) {
          this.costs[y * this.width + x] = null;
          continue;
        }
        const c = Fraction.of(raw);
        if (c.isNegative()) {
          throw new Error('negative cell cost at (' + x + ',' + y + '): ' + c.toString());
        }
        this.costs[y * this.width + x] = c;
      }
    }
  }

  inBounds(x: number, y: number): boolean {
    return x >= 0 && y >= 0 && x < this.width && y < this.height;
  }

  /** Cost of entering the cell, or null when blocked / out of bounds. */
  costAt(x: number, y: number): Fraction | null {
    if (!this.inBounds(x, y)) return null;
    return this.costs[y * this.width + x];
  }

  isWalkable(cell: Cell): boolean {
    return this.costAt(cell.x, cell.y) !== null;
  }

  /** Smallest cell cost in the grid (useful for building admissible heuristics). */
  minCost(): Fraction {
    let best: Fraction | null = null;
    for (const c of this.costs) {
      if (c === null) continue;
      if (best === null || c.lt(best)) best = c;
    }
    return best ?? Fraction.ZERO;
  }

  neighbors(cell: Cell): Step[] {
    const out: Step[] = [];
    const dx = [1, -1, 0, 0];
    const dy = [0, 0, 1, -1];
    for (let i = 0; i < 4; i++) {
      const nx = cell.x + dx[i];
      const ny = cell.y + dy[i];
      const cost = this.costAt(nx, ny);
      if (cost !== null) out.push({ cell: { x: nx, y: ny }, cost });
    }
    return out;
  }
}
