import { Rational } from "./rational.ts";

/** A weighted directed graph with non-negative rational edge costs. */
export interface Graph<N> {
  neighbors(node: N): Iterable<{ to: N; cost: Rational }>;
  key(node: N): string;
}

export interface Cell {
  x: number;
  y: number;
}

export interface GridOptions {
  /** Allow 8-directional movement (default: 4-directional). */
  diagonal?: boolean;
  /** Blocked cells, as "x,y" strings or [x, y] pairs. */
  blocked?: Iterable<string | readonly [number, number]>;
  /**
   * Cost of moving from one cell to an adjacent cell.
   * Must be a non-negative rational. Defaults to 1 for every move.
   */
  moveCost?: (from: Cell, to: Cell) => Rational | number | string;
}

const ORTHO: ReadonlyArray<readonly [number, number]> = [
  [1, 0], [-1, 0], [0, 1], [0, -1],
];
const DIAG: ReadonlyArray<readonly [number, number]> = [
  [1, 1], [1, -1], [-1, 1], [-1, -1],
];

export class GridMap implements Graph<Cell> {
  readonly width: number;
  readonly height: number;
  private readonly diagonal: boolean;
  private readonly blockedCells: Set<string>;
  private readonly moveCost: (from: Cell, to: Cell) => Rational;

  constructor(width: number, height: number, options: GridOptions = {}) {
    if (width <= 0 || height <= 0) throw new Error("grid dimensions must be positive");
    this.width = width;
    this.height = height;
    this.diagonal = options.diagonal ?? false;
    this.blockedCells = new Set<string>();
    for (const b of options.blocked ?? []) {
      this.blockedCells.add(typeof b === "string" ? b : `${b[0]},${b[1]}`);
    }
    const raw = options.moveCost;
    this.moveCost = (from, to) => {
      const c = raw ? Rational.of(raw(from, to)) : Rational.ONE;
      if (c.isNegative()) {
        throw new Error(`negative move cost ${c} at (${from.x},${from.y})->(${to.x},${to.y})`);
      }
      return c;
    };
  }

  key(cell: Cell): string {
    return `${cell.x},${cell.y}`;
  }

  inBounds(cell: Cell): boolean {
    return cell.x >= 0 && cell.y >= 0 && cell.x < this.width && cell.y < this.height;
  }

  isBlocked(cell: Cell): boolean {
    return this.blockedCells.has(this.key(cell));
  }

  *neighbors(cell: Cell): Iterable<{ to: Cell; cost: Rational }> {
    const dirs = this.diagonal ? [...ORTHO, ...DIAG] : ORTHO;
    for (const [dx, dy] of dirs) {
      const to = { x: cell.x + dx, y: cell.y + dy };
      if (!this.inBounds(to) || this.isBlocked(to)) continue;
      yield { to, cost: this.moveCost(cell, to) };
    }
  }
}
