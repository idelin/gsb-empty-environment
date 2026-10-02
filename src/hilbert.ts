/**
 * N 阶希尔伯特矩阵：H[i][j] = 1 / (i + j + 1)（0-based）。
 * 经典病态矩阵：5 阶时条件数约 4.8e5，浮点求逆会严重失真，
 * 纯有理数运算则可得到精确结果。
 */

import { Rational } from './rational.ts';
import { RationalMatrix } from './matrix.ts';

export function hilbert(n: number): RationalMatrix {
  if (!Number.isInteger(n) || n < 1) throw new Error('hilbert: n must be a positive integer');
  const rows: Rational[][] = [];
  for (let i = 0; i < n; i++) {
    const row: Rational[] = [];
    for (let j = 0; j < n; j++) {
      row.push(new Rational(1n, BigInt(i + j + 1)));
    }
    rows.push(row);
  }
  return new RationalMatrix(rows);
}
