import { FORBIDDEN_COST } from './hungarian.ts';

export interface EnumeratedAssignment {
  /** Original row index -> original column index. Unmatched rows hold -1. */
  rowToCol: number[];
  cost: number;
}

/**
 * Exhaustive enumeration of every one-to-one assignment, including
 * partial assignments when columns outnumber rows.
 *
 * Intended for small verification trees only: complexity is
 * P(columns, rows). Forbidden edges are skipped during enumeration.
 */
export function enumerateAssignments(costs: number[][]): EnumeratedAssignment[] {
  const rowCount = costs.length;
  const results: EnumeratedAssignment[] = [];
  if (rowCount === 0) {
    return [{ rowToCol: [], cost: 0 }];
  }
  const colCount = costs[0].length;
  const rowToCol = new Array<number>(rowCount).fill(-1);
  const usedCols = new Array<boolean>(colCount).fill(false);

  const visit = (row: number, accumulated: number): void => {
    if (row === rowCount) {
      results.push({ rowToCol: [...rowToCol], cost: accumulated });
      return;
    }
    // Also explore leaving this row unmatched, so partial assignments are
    // represented (only relevant when rows > columns).
    if (colCount < rowCount) {
      visit(row + 1, accumulated);
    }
    for (let col = 0; col < colCount; col += 1) {
      if (usedCols[col]) {
        continue;
      }
      const edgeCost = costs[row][col];
      if (edgeCost >= FORBIDDEN_COST / 2) {
        continue;
      }
      usedCols[col] = true;
      rowToCol[row] = col;
      visit(row + 1, accumulated + edgeCost);
      rowToCol[row] = -1;
      usedCols[col] = false;
    }
  };

  visit(0, 0);
  return results.sort((a, b) => a.cost - b.cost);
}

/** Best and distinct second-best assignments via full enumeration. */
export function bestAndSecondBest(costs: number[][]): {
  best: EnumeratedAssignment | null;
  second: EnumeratedAssignment | null;
} {
  const all = enumerateAssignments(costs);
  if (all.length === 0) {
    return { best: null, second: null };
  }
  const best = all[0];
  const second =
    all.find(
      (candidate) =>
        candidate.cost > best.cost ||
        candidate.rowToCol.some((col, row) => col !== best.rowToCol[row]),
    ) ?? null;
  return { best, second };
}
