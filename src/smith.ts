import { ModRing, type Matrix } from './ring.ts';

export interface SmithNormalForm {
  rank: number;
  exponents: number[];
  diagonal: number[];
  kernelSize: bigint;
  kernelCardinality: bigint;
  imageSize: bigint;
  kernelGenerators: number[][];
  kernelBasis: number[][];
  generatorOrders: bigint[];
  rowTransform: Matrix;
  columnTransform: Matrix;
  diagonalMatrix: Matrix;
}

function identity(size: number): Matrix {
  return Array.from({ length: size }, (_, row) =>
    Array.from({ length: size }, (_, column) => (row === column ? 1 : 0)),
  );
}

function cloneMatrix(matrix: Matrix): Matrix {
  return matrix.map((row) => row.slice());
}

function swapRows(matrix: Matrix, left: number, right: number): void {
  if (left === right) return;
  const row = matrix[left];
  matrix[left] = matrix[right];
  matrix[right] = row;
}

function swapColumns(matrix: Matrix, left: number, right: number): void {
  if (left === right) return;
  for (const row of matrix) {
    const value = row[left];
    row[left] = row[right];
    row[right] = value;
  }
}

function subtractRowMultiple(
  matrix: Matrix,
  targetRow: number,
  pivotRow: number,
  multiplier: number,
  ring: ModRing,
): void {
  if (multiplier === 0) return;
  for (let column = 0; column < matrix[targetRow].length; column += 1) {
    matrix[targetRow][column] = ring.sub(
      matrix[targetRow][column],
      ring.mul(multiplier, matrix[pivotRow][column]),
    );
  }
}

function subtractColumnMultiple(
  matrix: Matrix,
  targetColumn: number,
  pivotColumn: number,
  multiplier: number,
  ring: ModRing,
): void {
  if (multiplier === 0) return;
  for (let row = 0; row < matrix.length; row += 1) {
    matrix[row][targetColumn] = ring.sub(
      matrix[row][targetColumn],
      ring.mul(multiplier, matrix[row][pivotColumn]),
    );
  }
}

export function normalizeMatrix(matrix: readonly number[][] | undefined, ring: ModRing): Matrix {
  if (!matrix || matrix.length === 0) return [];
  const width = matrix[0]?.length ?? 0;
  return matrix.map((row, rowIndex) => {
    if (row.length !== width) {
      throw new RangeError(`matrix row ${rowIndex} has width ${row.length}, expected ${width}`);
    }
    return ring.normalize(row);
  });
}

export function matrixWidth(matrix: Matrix, fallback = 0): number {
  return matrix[0]?.length ?? fallback;
}

export function smithNormalForm(
  source: readonly number[][],
  ringOrDescriptor: ModRing | ConstructorParameters<typeof ModRing>[0] = {},
  expectedColumns = 0,
): SmithNormalForm {
  const ring = ringOrDescriptor instanceof ModRing ? ringOrDescriptor : new ModRing(ringOrDescriptor);
  const input = normalizeMatrix(source, ring);
  const rows = input.length;
  const columns = matrixWidth(input, expectedColumns);
  const a = cloneMatrix(input);
  const rowTransform = identity(rows);
  const columnTransform = identity(columns);
  const exponents: number[] = [];
  const diagonal: number[] = [];

  const pivotLimit = Math.min(rows, columns);
  let rank = 0;

  for (let pivot = 0; pivot < pivotLimit; pivot += 1) {
    let selectedRow = -1;
    let selectedColumn = -1;
    let bestValuation = ring.e;

    for (let row = pivot; row < rows; row += 1) {
      for (let column = pivot; column < columns; column += 1) {
        const valuation = ring.valuation(a[row][column]);
        if (valuation < bestValuation) {
          bestValuation = valuation;
          selectedRow = row;
          selectedColumn = column;
        }
      }
    }

    if (selectedRow === -1 || bestValuation >= ring.e) break;

    swapRows(a, pivot, selectedRow);
    swapRows(rowTransform, pivot, selectedRow);
    swapColumns(a, pivot, selectedColumn);
    swapColumns(columnTransform, pivot, selectedColumn);

    const pivotValue = a[pivot][pivot];
    const exponent = ring.valuation(pivotValue);

    for (let row = pivot + 1; row < rows; row += 1) {
      const multiplier = ring.solveMultiplier(pivotValue, a[row][pivot]);
      if (multiplier === null) {
        throw new Error(`Smith elimination failed: pivot cannot divide entry at row ${row}`);
      }
      subtractRowMultiple(a, row, pivot, multiplier, ring);
      subtractRowMultiple(rowTransform, row, pivot, multiplier, ring);
    }

    for (let column = pivot + 1; column < columns; column += 1) {
      const multiplier = ring.solveMultiplier(pivotValue, a[pivot][column]);
      if (multiplier === null) {
        throw new Error(`Smith elimination failed: pivot cannot divide column ${column}`);
      }
      subtractColumnMultiple(a, column, pivot, multiplier, ring);
      subtractColumnMultiple(columnTransform, column, pivot, multiplier, ring);
    }

    exponents.push(exponent);
    diagonal.push(pivotValue);
    rank += 1;
  }

  let kernelExponent = ring.e * (columns - rank);
  let imageExponent = 0;
  for (const exponent of exponents) {
    kernelExponent += exponent;
    imageExponent += ring.e - exponent;
  }

  const kernelSize = ring.p === 1 ? 1n : BigInt(ring.p) ** BigInt(kernelExponent);
  const imageSize = BigInt(ring.p) ** BigInt(imageExponent);

  const kernelGenerators: number[][] = [];
  const generatorOrders: bigint[] = [];

  for (let index = 0; index < rank; index += 1) {
    const exponent = exponents[index];
    if (exponent <= 0) continue;
    const scale = ring.p ** (ring.e - exponent);
    const generator = Array.from({ length: columns }, (_, row) =>
      ring.mul(scale, columnTransform[row][index] ?? 0),
    );
    kernelGenerators.push(generator);
    generatorOrders.push(BigInt(ring.p) ** BigInt(exponent));
  }

  for (let index = rank; index < columns; index += 1) {
    const generator = Array.from({ length: columns }, (_, row) =>
      ring.norm(columnTransform[row][index] ?? 0),
    );
    kernelGenerators.push(generator);
    generatorOrders.push(BigInt(ring.q));
  }

  return {
    rank,
    exponents,
    diagonal,
    kernelSize,
    kernelCardinality: kernelSize,
    imageSize,
    kernelGenerators,
    kernelBasis: kernelGenerators,
    generatorOrders,
    rowTransform,
    columnTransform,
    diagonalMatrix: a,
  };
}

export function modularKernelSize(
  matrix: readonly number[][],
  ring: ModRing,
): bigint {
  return smithNormalForm(matrix, ring).kernelSize;
}

export function fieldRankModP(source: readonly number[][], ring: ModRing): number {
  const matrix = normalizeMatrix(source, ring).map((row) => row.map((value) => value % ring.p));
  const rows = matrix.length;
  const columns = matrix[0]?.length ?? 0;
  let rank = 0;

  for (let column = 0; column < columns && rank < rows; column += 1) {
    let pivot = rank;
    while (pivot < rows && matrix[pivot]![column] === 0) pivot += 1;
    if (pivot === rows) continue;
    [matrix[rank], matrix[pivot]] = [matrix[pivot]!, matrix[rank]!];

    const pivotValue = matrix[rank]![column]!;
    let inverse = 1;
    for (let candidate = 1; candidate < ring.p; candidate += 1) {
      if ((pivotValue * candidate) % ring.p === 1) {
        inverse = candidate;
        break;
      }
    }
    for (let j = column; j < columns; j += 1) {
      matrix[rank]![j] = (matrix[rank]![j]! * inverse) % ring.p;
    }
    for (let i = 0; i < rows; i += 1) {
      if (i === rank || matrix[i]![column] === 0) continue;
      const factor = matrix[i]![column]!;
      for (let j = column; j < columns; j += 1) {
        matrix[i]![j] = (matrix[i]![j]! - factor * matrix[rank]![j]!) % ring.p;
        if (matrix[i]![j]! < 0) matrix[i]![j] += ring.p;
      }
    }
    rank += 1;
  }

  return rank;
}

export function isConsistentSystem(
  source: readonly number[][],
  vector: readonly number[],
  ring: ModRing,
): boolean {
  const width = matrixWidth(normalizeMatrix(source, ring));
  if (vector.length !== source.length) {
    throw new RangeError(`b has length ${vector.length}, expected ${source.length}`);
  }
  if (source.length === 0) return vector.every((value) => ring.norm(value) === 0);
  if (width === 0) return vector.every((value) => ring.norm(value) === 0);

  const snf = smithNormalForm(source, ring);
  const transformed = snf.rowTransform.map((row) => ring.dot(row, vector));
  for (let row = 0; row < source.length; row += 1) {
    const value = transformed[row];
    if (row < snf.rank) {
      if (ring.valuation(value) < snf.exponents[row]) return false;
    } else if (ring.norm(value) !== 0) {
      return false;
    }
  }
  return true;
}
