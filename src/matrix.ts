/**
 * 纯有理数方阵代数：
 *  - 行列式：Bareiss 无除法（fraction-free）高斯消元，整数域上精确进行；
 *  - 逆矩阵：伴随矩阵法 A^{-1} = adj(A) / det(A)，各余子式同样由 Bareiss 求得；
 *  - 奇异性：det(A) === 0 时精确判定，inverse() 抛出 SingularMatrixError。
 */

import { Rational, type RationalLike } from './rational.ts';

export class SingularMatrixError extends Error {
  constructor(message = 'matrix is singular (determinant is zero)') {
    super(message);
    this.name = 'SingularMatrixError';
  }
}

function lcm(a: bigint, b: bigint): bigint {
  if (a === 0n || b === 0n) return 0n;
  const aa = a < 0n ? -a : a;
  const bb = b < 0n ? -b : b;
  let x = aa;
  let y = bb;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return (aa / x) * bb;
}

/**
 * Bareiss 无除法消元求整数方阵行列式。
 * 每一步 m[i][j] = (m[i][j]*pivot - m[i][k]*m[k][j]) / prev 均为整除，
 * 全程 BigInt，无任何精度损失。
 */
export function bareissDeterminant(matrix: bigint[][]): bigint {
  const n = matrix.length;
  const m = matrix.map((row) => row.slice());
  let sign = 1n;
  let prev = 1n;
  for (let k = 0; k < n - 1; k++) {
    if (m[k][k] === 0n) {
      let swap = -1;
      for (let i = k + 1; i < n; i++) {
        if (m[i][k] !== 0n) {
          swap = i;
          break;
        }
      }
      if (swap === -1) return 0n;
      const tmp = m[k];
      m[k] = m[swap];
      m[swap] = tmp;
      sign = -sign;
    }
    const pivot = m[k][k];
    for (let i = k + 1; i < n; i++) {
      for (let j = k + 1; j < n; j++) {
        m[i][j] = (m[i][j] * pivot - m[i][k] * m[k][j]) / prev;
      }
    }
    prev = pivot;
    for (let i = k + 1; i < n; i++) {
      m[i][k] = 0n;
    }
  }
  return sign * m[n - 1][n - 1];
}

export class RationalMatrix {
  readonly size: number;
  readonly data: Rational[][];

  constructor(entries: RationalLike[][]) {
    if (entries.length === 0) throw new Error('matrix must be non-empty');
    const n = entries.length;
    for (const row of entries) {
      if (row.length !== n) throw new Error('matrix must be square');
    }
    this.size = n;
    this.data = entries.map((row) => row.map((v) => Rational.from(v)));
  }

  static identity(n: number): RationalMatrix {
    const rows: RationalLike[][] = [];
    for (let i = 0; i < n; i++) {
      const row: RationalLike[] = [];
      for (let j = 0; j < n; j++) row.push(i === j ? 1n : 0n);
      rows.push(row);
    }
    return new RationalMatrix(rows);
  }

  at(i: number, j: number): Rational {
    return this.data[i][j];
  }

  equals(other: RationalMatrix): boolean {
    if (this.size !== other.size) return false;
    for (let i = 0; i < this.size; i++) {
      for (let j = 0; j < this.size; j++) {
        if (!this.data[i][j].equals(other.data[i][j])) return false;
      }
    }
    return true;
  }

  mul(other: RationalMatrix): RationalMatrix {
    const n = this.size;
    if (other.size !== n) throw new Error('matrix size mismatch');
    const out: RationalLike[][] = [];
    for (let i = 0; i < n; i++) {
      const row: RationalLike[] = [];
      for (let j = 0; j < n; j++) {
        let acc = Rational.ZERO;
        for (let k = 0; k < n; k++) {
          acc = acc.add(this.data[i][k].mul(other.data[k][j]));
        }
        row.push(acc);
      }
      out.push(row);
    }
    return new RationalMatrix(out);
  }

  /**
   * 提取整数码矩阵：intM = M * L，L 为所有元素分母的最小公倍数。
   * 于是 det(M) = det(intM) / L^n。
   */
  private toIntegerMatrix(): { intM: bigint[][]; scale: bigint } {
    let scale = 1n;
    for (const row of this.data) {
      for (const v of row) scale = lcm(scale, v.den);
    }
    const intM = this.data.map((row) => row.map((v) => v.num * (scale / v.den)));
    return { intM, scale };
  }

  /** 精确行列式（Bareiss 无除法算法） */
  determinant(): Rational {
    const n = this.size;
    const { intM, scale } = this.toIntegerMatrix();
    const intDet = bareissDeterminant(intM);
    return new Rational(intDet, scale ** BigInt(n));
  }

  /** 去掉第 r 行第 c 列的余子矩阵 */
  private minor(r: number, c: number): RationalMatrix {
    const rows: RationalLike[][] = [];
    for (let i = 0; i < this.size; i++) {
      if (i === r) continue;
      const row: RationalLike[] = [];
      for (let j = 0; j < this.size; j++) {
        if (j === c) continue;
        row.push(this.data[i][j]);
      }
      rows.push(row);
    }
    return new RationalMatrix(rows);
  }

  /** 伴随矩阵 adj(A)：adj[i][j] = (-1)^(i+j) * det(minor(j, i)) */
  adjugate(): RationalMatrix {
    const n = this.size;
    if (n === 1) return new RationalMatrix([[1n]]);
    const cofactors: RationalLike[][] = [];
    for (let i = 0; i < n; i++) {
      const row: RationalLike[] = [];
      for (let j = 0; j < n; j++) {
        const minorDet = this.minor(j, i).determinant();
        row.push((i + j) % 2 === 0 ? minorDet : minorDet.neg());
      }
      cofactors.push(row);
    }
    return new RationalMatrix(cofactors);
  }

  /**
   * 精确逆矩阵 A^{-1} = adj(A) / det(A)。
   * det(A) = 0 时抛出 SingularMatrixError。
   */
  inverse(): RationalMatrix {
    const det = this.determinant();
    if (det.isZero()) throw new SingularMatrixError();
    const n = this.size;
    if (n === 1) return new RationalMatrix([[Rational.ONE.div(det)]]);
    const adj = this.adjugate();
    const out: RationalLike[][] = [];
    for (let i = 0; i < n; i++) {
      const row: RationalLike[] = [];
      for (let j = 0; j < n; j++) row.push(adj.data[i][j].div(det));
      out.push(row);
    }
    return new RationalMatrix(out);
  }

  isSingular(): boolean {
    return this.determinant().isZero();
  }

  toString(): string {
    return this.data.map((row) => `[ ${row.map((v) => v.toString()).join(', ')} ]`).join('\n');
  }
}
