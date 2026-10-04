export interface Point {
  x: number;
  y: number;
}

export interface Seam {
  round: number;
  energy: number;
  path: Point[];
}

export interface CarveReport {
  seams: Seam[];
  removed: number;
  feasible: boolean;
  reason: string;
}

export interface RemainingPixel {
  x: number;
  y: number;
  energy: number;
}

export type RecomputeEnergy = (remaining: RemainingPixel[][]) => number[][];

export interface SeamCarverOptions {
  protectedPoints?: Point[];
  recomputeEnergy?: RecomputeEnergy;
}

interface Cell {
  origX: number;
  energy: number;
  isProtected: boolean;
}

interface FoundSeam {
  energy: number;
  cols: number[];
}

function makeCell(origX: number, energy: number, isProtected: boolean): Cell {
  return { origX, energy, isProtected };
}

function validateEnergyMap(energy: number[][]): void {
  if (!Array.isArray(energy) || energy.length === 0) {
    throw new RangeError("energy map must contain at least one row");
  }
  if (!Array.isArray(energy[0]) || energy[0].length === 0) {
    throw new RangeError("energy map rows must contain at least one column");
  }
  const width = energy[0].length;
  for (let y = 0; y < energy.length; y++) {
    const row = energy[y];
    if (!Array.isArray(row) || row.length !== width) {
      throw new RangeError("energy map must be rectangular, row " + y + " has a different length");
    }
    for (let x = 0; x < width; x++) {
      if (!Number.isInteger(row[x])) {
        throw new TypeError("energy must be an integer at (" + x + ", " + y + ")");
      }
    }
  }
}

export class SeamCarver {
  private grid: Cell[][];
  private recompute: RecomputeEnergy | undefined;
  private round: number;

  constructor(energy: number[][], options: SeamCarverOptions = {}) {
    validateEnergyMap(energy);
    this.grid = energy.map((row, y) => row.map((value, x) => makeCell(x, value, false)));
    const points = options.protectedPoints ?? [];
    for (const p of points) {
      if (!Number.isInteger(p.x) || !Number.isInteger(p.y) || p.y < 0 || p.y >= this.height || p.x < 0 || p.x >= this.width) {
        throw new RangeError("protected point out of bounds (" + p.x + ", " + p.y + ")");
      }
      this.grid[p.y][p.x].isProtected = true;
    }
    this.recompute = options.recomputeEnergy;
    this.round = 0;
  }

  get height(): number {
    return this.grid.length;
  }

  get width(): number {
    return this.grid[0].length;
  }

  findSeam(): FoundSeam | null {
    const h = this.height;
    if (h === 0) {
      return null;
    }
    const w = this.width;
    if (w === 0) {
      return null;
    }
    const dp: number[][] = [];
    const parent: number[][] = [];
    for (let y = 0; y < h; y++) {
      dp.push(new Array(w).fill(Infinity));
      parent.push(new Array(w).fill(-1));
    }
    for (let x = 0; x < w; x++) {
      const cell = this.grid[0][x];
      if (!cell.isProtected) {
        dp[0][x] = cell.energy;
      }
    }
    for (let y = 1; y < h; y++) {
      for (let x = 0; x < w; x++) {
        const cell = this.grid[y][x];
        if (cell.isProtected) {
          continue;
        }
        let best = Infinity;
        let bestX = -1;
        for (let dx = -1; dx <= 1; dx++) {
          const px = x + dx;
          if (px < 0 || px >= w) {
            continue;
          }
          if (dp[y - 1][px] < best) {
            best = dp[y - 1][px];
            bestX = px;
          }
        }
        if (bestX >= 0) {
          dp[y][x] = best + cell.energy;
          parent[y][x] = bestX;
        }
      }
    }
    let endX = -1;
    let total = Infinity;
    for (let x = 0; x < w; x++) {
      if (dp[h - 1][x] < total) {
        total = dp[h - 1][x];
        endX = x;
      }
    }
    if (endX < 0 || total === Infinity) {
      return null;
    }
    const cols = new Array(h);
    let x = endX;
    for (let y = h - 1; y >= 0; y--) {
      cols[y] = x;
      x = parent[y][x];
    }
    const energy = total;
    return { energy, cols };
  }

  carveOne(): Seam | null {
    const found = this.findSeam();
    if (found === null) {
      return null;
    }
    this.round += 1;
    const round = this.round;
    const energy = found.energy;
    const path: Point[] = [];
    for (let y = 0; y < this.height; y++) {
      const col = found.cols[y];
      path.push({ x: this.grid[y][col].origX, y });
    }
    for (let y = 0; y < this.height; y++) {
      this.grid[y].splice(found.cols[y], 1);
    }
    if (this.recompute !== undefined && this.width > 0) {
      const remaining: RemainingPixel[][] = this.grid.map((row, y) => row.map((cell) => ({ x: cell.origX, y, energy: cell.energy })));
      const next = this.recompute(remaining);
      this.applyRecomputed(next);
    }
    return { round, energy, path };
  }

  carve(count: number): CarveReport {
    if (!Number.isInteger(count) || count < 0) {
      throw new RangeError("count must be a non-negative integer");
    }
    const seams: Seam[] = [];
    let reason = "";
    for (let i = 0; i < count; i++) {
      const seam = this.carveOne();
      if (seam === null) {
        reason = "no feasible seam exists at round " + (i + 1) + ", stopped after " + i + " removal(s)";
        break;
      }
      seams.push(seam);
    }
    const removed = seams.length;
    const feasible = removed === count;
    return { seams, removed, feasible, reason };
  }

  private applyRecomputed(next: number[][]): void {
    validateEnergyMap(next);
    if (next.length !== this.height || next[0].length !== this.width) {
      throw new RangeError("recomputeEnergy must return a " + this.height + "x" + this.width + " grid");
    }
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        this.grid[y][x].energy = next[y][x];
      }
    }
  }
}

export function carveSeams(energy: number[][], count: number, options: SeamCarverOptions = {}): CarveReport {
  return new SeamCarver(energy, options).carve(count);
}

export function gradientEnergy(gray: number[][]): number[][] {
  validateEnergyMap(gray);
  const h = gray.length;
  const w = gray[0].length;
  const out: number[][] = [];
  for (let y = 0; y < h; y++) {
    const row: number[] = [];
    for (let x = 0; x < w; x++) {
      const left = gray[y][Math.max(0, x - 1)];
      const right = gray[y][Math.min(w - 1, x + 1)];
      const up = gray[Math.max(0, y - 1)][x];
      const down = gray[Math.min(h - 1, y + 1)][x];
      row.push(Math.abs(right - left) + Math.abs(down - up));
    }
    out.push(row);
  }
  return out;
}
