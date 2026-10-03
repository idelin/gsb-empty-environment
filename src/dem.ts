import type { DEM, Grid } from './types.ts';

export function createGrid(rows: number, cols: number, fill = 0): Grid {
  if (!Number.isInteger(rows) || !Number.isInteger(cols) || rows <= 0 || cols <= 0) {
    throw new Error(`非法栅格尺寸: ${rows}x${cols}`);
  }
  const data = new Float64Array(rows * cols);
  if (fill !== 0) data.fill(fill);
  return { rows, cols, data };
}

/** 由二维数组构建 DEM。 */
export function demFromArray(elevations: number[][], cellSize = 1): DEM {
  if (elevations.length === 0 || elevations[0].length === 0) {
    throw new Error('DEM 高程数组不能为空');
  }
  const rows = elevations.length;
  const cols = elevations[0].length;
  const grid = createGrid(rows, cols);
  for (let r = 0; r < rows; r++) {
    if (elevations[r].length !== cols) throw new Error(`DEM 第 ${r} 行长度不一致`);
    for (let c = 0; c < cols; c++) {
      const v = elevations[r][c];
      if (!Number.isFinite(v)) throw new Error(`DEM (${r},${c}) 高程非法: ${v}`);
      grid.data[r * cols + c] = v;
    }
  }
  return { ...grid, cellSize };
}

export function gridIndex(grid: Grid, row: number, col: number): number {
  return row * grid.cols + col;
}

export function inBounds(grid: Grid, row: number, col: number): boolean {
  return row >= 0 && row < grid.rows && col >= 0 && col < grid.cols;
}

/** 8 邻域偏移（含对角），对角距离为 cellSize*sqrt(2)。 */
export const NEIGHBORS8: ReadonlyArray<readonly [number, number]> = [
  [-1, -1], [-1, 0], [-1, 1],
  [0, -1],           [0, 1],
  [1, -1],  [1, 0],  [1, 1],
];

/** 4 邻域偏移。 */
export const NEIGHBORS4: ReadonlyArray<readonly [number, number]> = [
  [-1, 0], [0, -1], [0, 1], [1, 0],
];
