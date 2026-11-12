/**
 * Rectangular min-cost one-to-one assignment (Hungarian / Kuhn-Munkres).
 *
 * Rows are labels and columns are controls. `forbidden` edges use a large
 * finite sentinel instead of Infinity so the reduction arithmetic stays
 * finite; a solution containing a sentinel edge is reported as infeasible.
 */

export const FORBIDDEN_COST = 1e12;

export interface Assignment {
  /** Row (label) id index -> assigned column (control) index, or -1. */
  rowToCol: number[];
  cost: number;
  feasible: boolean;
}

/**
 * Min-cost assignment matching every row to a distinct column.
 * Requires at least as many columns as rows.
 */
export function minCostAssignment(costs: number[][]): Assignment {
  const rowCount = costs.length;
  if (rowCount === 0) {
    return { rowToCol: [], cost: 0, feasible: true };
  }
  const colCount = costs[0].length;
  if (colCount < rowCount) {
    throw new Error('minCostAssignment requires columns >= rows');
  }

  // 1-indexed potentials.
  const rowPotential = new Array<number>(rowCount + 1).fill(0);
  const colPotential = new Array<number>(colCount + 1).fill(0);
  const pairedCol = new Array<number>(colCount + 1).fill(0);
  const pairedRow = new Array<number>(rowCount + 1).fill(0);

  for (let row = 1; row <= rowCount; row += 1) {
    pairedCol[0] = row;
    let currentCol = 0;
    const minValue = new Array<number>(colCount + 1).fill(Number.POSITIVE_INFINITY);
    const used = new Array<boolean>(colCount + 1).fill(false);
    const previousRow = new Array<number>(colCount + 1).fill(0);

    do {
      used[currentCol] = true;
      const currentRow = pairedCol[currentCol];
      let delta = Number.POSITIVE_INFINITY;
      let nextCol = 0;

      for (let col = 1; col <= colCount; col += 1) {
        if (used[col]) {
          continue;
        }
        const slack =
          costs[currentRow - 1][col - 1] -
          rowPotential[currentRow] -
          colPotential[col];
        if (slack < minValue[col]) {
          minValue[col] = slack;
          previousRow[col] = currentCol;
        }
        if (minValue[col] < delta) {
          delta = minValue[col];
          nextCol = col;
        }
      }

      for (let col = 0; col <= colCount; col += 1) {
        if (used[col]) {
          rowPotential[pairedCol[col]] += delta;
          colPotential[col] -= delta;
        } else {
          minValue[col] -= delta;
        }
      }
      currentCol = nextCol;
    } while (pairedCol[currentCol] !== 0);

    // Augment along the alternating path.
    let col = currentCol;
    while (col !== 0) {
      const previousCol = previousRow[col];
      pairedCol[col] = pairedCol[previousCol];
      pairedRow[pairedCol[col]] = col;
      col = previousCol;
    }
  }

  const rowToCol = new Array<number>(rowCount).fill(-1);
  let total = 0;
  let feasible = true;
  for (let row = 1; row <= rowCount; row += 1) {
    const col = pairedRow[row];
    rowToCol[row - 1] = col - 1;
    const edgeCost = costs[row - 1][col - 1];
    total += edgeCost;
    if (edgeCost >= FORBIDDEN_COST / 2) {
      feasible = false;
    }
  }
  if (!feasible) {
    total = Number.POSITIVE_INFINITY;
  }
  return { rowToCol, cost: total, feasible };
}

/**
 * Transpose a matrix so the smaller dimension becomes the rows; the
 * returned mapping is expressed in terms of the original row indices.
 */
export function minCostAssignmentAnyShape(costs: number[][]): Assignment {
  const rowCount = costs.length;
  const colCount = rowCount === 0 ? 0 : costs[0].length;
  if (rowCount <= colCount) {
    return minCostAssignment(costs);
  }

  const transposed: number[][] = Array.from({ length: colCount }, () =>
    new Array<number>(rowCount).fill(0),
  );
  for (let row = 0; row < rowCount; row += 1) {
    for (let col = 0; col < colCount; col += 1) {
      transposed[col][row] = costs[row][col];
    }
  }

  const inner = minCostAssignment(transposed);
  const rowToCol = new Array<number>(rowCount).fill(-1);
  for (let col = 0; col < colCount; col += 1) {
    const originalRow = inner.rowToCol[col];
    if (originalRow >= 0) {
      rowToCol[originalRow] = col;
    }
  }
  return { rowToCol, cost: inner.cost, feasible: inner.feasible };
}
