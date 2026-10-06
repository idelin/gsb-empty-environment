/**
 * Full-matrix reference model: materializes the complete series x time
 * matrix and stacks every column from scratch. Used as the ground truth
 * to verify the incremental revision engine.
 */

import { StackCell, StackMode, stackColumn } from './stack';

/** rows = series, cols = time columns; null = missing. */
export type Matrix = Array<Array<number | null>>;

export function stackMatrix(matrix: Matrix, mode: StackMode): StackCell[][] {
  const rows = matrix.length;
  const cols = rows === 0 ? 0 : matrix[0].length;
  const out: StackCell[][] = [];
  for (let c = 0; c < cols; c++) {
    const values: Array<number | null> = [];
    for (let r = 0; r < rows; r++) values.push(matrix[r][c]);
    out.push(stackColumn(values, mode));
  }
  // Return as [series][column] to match the matrix layout.
  const bySeries: StackCell[][] = [];
  for (let r = 0; r < rows; r++) {
    const row: StackCell[] = [];
    for (let c = 0; c < cols; c++) row.push(out[c][r]);
    bySeries.push(row);
  }
  return bySeries;
}

/**
 * Builds a dense matrix from per-series time->value maps over the sorted
 * union of all timestamps.
 */
export function buildMatrix(
  seriesData: ReadonlyArray<ReadonlyMap<number, number>>
): { times: number[]; matrix: Matrix } {
  const timeSet = new Set<number>();
  for (const m of seriesData) for (const t of m.keys()) timeSet.add(t);
  const times = Array.from(timeSet).sort((a, b) => a - b);
  const matrix: Matrix = seriesData.map((m) => times.map((t) => (m.has(t) ? (m.get(t) as number) : null)));
  return { times, matrix };
}
