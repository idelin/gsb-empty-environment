export interface OperationCosts {
  readonly insert: number;
  readonly delete: number;
  readonly substitute: number;
}

export const DEFAULT_COSTS: OperationCosts = Object.freeze({
  insert: 1,
  delete: 1,
  substitute: 1,
});

export type MoveKind = 'diagonal' | 'delete' | 'insert';

export const DEFAULT_MOVE_ORDER: readonly MoveKind[] = Object.freeze([
  'diagonal',
  'delete',
  'insert',
]);

export type OperationKind = 'match' | 'substitute' | 'delete' | 'insert';

export interface EditOperation {
  readonly kind: OperationKind;
  readonly sourceIndex: number;
  readonly targetIndex: number;
  readonly sourceChar: string | undefined;
  readonly targetChar: string | undefined;
  readonly cost: number;
}

export interface AnalyzeOptions {
  readonly moveOrder?: readonly MoveKind[];
}

export interface EditDistanceAnalysis {
  readonly source: string;
  readonly target: string;
  readonly costs: OperationCosts;
  readonly cost: number;
  readonly count: bigint;
  unrank(k: number | bigint): EditOperation[];
}

const MOVE_KINDS: readonly MoveKind[] = ['diagonal', 'delete', 'insert'];

function validateCosts(costs: OperationCosts): void {
  const rules: ReadonlyArray<readonly [string, number, boolean]> = [
    ['insert', costs.insert, false],
    ['delete', costs.delete, false],
    ['substitute', costs.substitute, true],
  ];
  for (const [name, value, allowZero] of rules) {
    if (typeof value !== 'number') {
      throw new TypeError(`cost "${name}" must be a number, got ${typeof value}`);
    }
    if (!Number.isFinite(value)) {
      throw new RangeError(`cost "${name}" must be finite, got ${value}`);
    }
    if (allowZero ? value < 0 : value <= 0) {
      throw new RangeError(
        allowZero
          ? `cost "${name}" must be >= 0, got ${value}`
          : `cost "${name}" must be > 0 (zero-cost insert/delete is forbidden), got ${value}`,
      );
    }
  }
}

function normalizeMoveOrder(order: readonly MoveKind[] | undefined): MoveKind[] {
  if (order === undefined) {
    return [...DEFAULT_MOVE_ORDER];
  }
  const ok =
    Array.isArray(order) &&
    order.length === MOVE_KINDS.length &&
    new Set(order).size === MOVE_KINDS.length &&
    order.every((move) => MOVE_KINDS.includes(move));
  if (!ok) {
    throw new TypeError(
      `moveOrder must be a permutation of ${JSON.stringify(MOVE_KINDS)}, got ${JSON.stringify(order)}`,
    );
  }
  return [...order];
}

function normalizeIndex(k: number | bigint, count: bigint): bigint {
  let index: bigint;
  if (typeof k === 'bigint') {
    index = k;
  } else if (typeof k === 'number' && Number.isSafeInteger(k)) {
    index = BigInt(k);
  } else {
    throw new TypeError(`k must be a safe integer or a bigint, got ${String(k)}`);
  }
  if (index < 0n || index >= count) {
    throw new RangeError(`k=${index} out of range: there are ${count} optimal path(s), valid k is 0..${count - 1n}`);
  }
  return index;
}

export function analyzeEditDistance(
  source: string,
  target: string,
  costs: OperationCosts = DEFAULT_COSTS,
  options: AnalyzeOptions = {},
): EditDistanceAnalysis {
  validateCosts(costs);
  const moveOrder = normalizeMoveOrder(options.moveOrder);
  const sourceChars = Array.from(source);
  const targetChars = Array.from(target);
  const m = sourceChars.length;
  const n = targetChars.length;
  const width = n + 1;
  const at = (i: number, j: number): number => i * width + j;

  const costTable = new Array<number>((m + 1) * width);
  const countTable = new Array<bigint>((m + 1) * width);
  costTable[at(0, 0)] = 0;
  countTable[at(0, 0)] = 1n;

  for (let i = 0; i <= m; i++) {
    for (let j = 0; j <= n; j++) {
      if (i === 0 && j === 0) continue;
      let best = Infinity;
      let ways = 0n;
      if (i > 0 && j > 0) {
        const diagonalCost =
          costTable[at(i - 1, j - 1)] + (sourceChars[i - 1] === targetChars[j - 1] ? 0 : costs.substitute);
        if (diagonalCost < best) {
          best = diagonalCost;
          ways = 0n;
        }
        if (diagonalCost === best) {
          ways += countTable[at(i - 1, j - 1)];
        }
      }
      if (i > 0) {
        const deleteCost = costTable[at(i - 1, j)] + costs.delete;
        if (deleteCost < best) {
          best = deleteCost;
          ways = 0n;
        }
        if (deleteCost === best) {
          ways += countTable[at(i - 1, j)];
        }
      }
      if (j > 0) {
        const insertCost = costTable[at(i, j - 1)] + costs.insert;
        if (insertCost < best) {
          best = insertCost;
          ways = 0n;
        }
        if (insertCost === best) {
          ways += countTable[at(i, j - 1)];
        }
      }
      costTable[at(i, j)] = best;
      countTable[at(i, j)] = ways;
    }
  }

  const totalCost = costTable[at(m, n)];
  const totalCount = countTable[at(m, n)];

  const diagonalOp = (i: number, j: number): EditOperation => {
    const matched = sourceChars[i - 1] === targetChars[j - 1];
    return {
      kind: matched ? 'match' : 'substitute',
      sourceIndex: i - 1,
      targetIndex: j - 1,
      sourceChar: sourceChars[i - 1],
      targetChar: targetChars[j - 1],
      cost: matched ? 0 : costs.substitute,
    };
  };
  const deleteOp = (i: number, j: number): EditOperation => ({
    kind: 'delete',
    sourceIndex: i - 1,
    targetIndex: j,
    sourceChar: sourceChars[i - 1],
    targetChar: undefined,
    cost: costs.delete,
  });
  const insertOp = (i: number, j: number): EditOperation => ({
    kind: 'insert',
    sourceIndex: i,
    targetIndex: j - 1,
    sourceChar: undefined,
    targetChar: targetChars[j - 1],
    cost: costs.insert,
  });

  const unrank = (k: number | bigint): EditOperation[] => {
    let remaining = normalizeIndex(k, totalCount);
    const operations: EditOperation[] = [];
    let i = m;
    let j = n;
    while (i > 0 || j > 0) {
      const cellCost = costTable[at(i, j)];
      let advanced = false;
      for (const move of moveOrder) {
        if (move === 'diagonal') {
          if (i === 0 || j === 0) continue;
          const opCost = sourceChars[i - 1] === targetChars[j - 1] ? 0 : costs.substitute;
          if (costTable[at(i - 1, j - 1)] + opCost !== cellCost) continue;
          const ways = countTable[at(i - 1, j - 1)];
          if (remaining < ways) {
            operations.unshift(diagonalOp(i, j));
            i--;
            j--;
            advanced = true;
            break;
          }
          remaining -= ways;
        } else if (move === 'delete') {
          if (i === 0) continue;
          if (costTable[at(i - 1, j)] + costs.delete !== cellCost) continue;
          const ways = countTable[at(i - 1, j)];
          if (remaining < ways) {
            operations.unshift(deleteOp(i, j));
            i--;
            advanced = true;
            break;
          }
          remaining -= ways;
        } else {
          if (j === 0) continue;
          if (costTable[at(i, j - 1)] + costs.insert !== cellCost) continue;
          const ways = countTable[at(i, j - 1)];
          if (remaining < ways) {
            operations.unshift(insertOp(i, j));
            j--;
            advanced = true;
            break;
          }
          remaining -= ways;
        }
      }
      if (!advanced) {
        throw new Error(`internal error: no optimal predecessor at cell (${i}, ${j})`);
      }
    }
    return operations;
  };

  return {
    source,
    target,
    costs,
    cost: totalCost,
    count: totalCount,
    unrank,
  };
}

export function editDistance(
  source: string,
  target: string,
  costs: OperationCosts = DEFAULT_COSTS,
): { cost: number; count: bigint } {
  const { cost, count } = analyzeEditDistance(source, target, costs);
  return { cost, count };
}

export function kthOptimalPath(
  source: string,
  target: string,
  k: number | bigint,
  costs: OperationCosts = DEFAULT_COSTS,
  options: AnalyzeOptions = {},
): EditOperation[] {
  return analyzeEditDistance(source, target, costs, options).unrank(k);
}
