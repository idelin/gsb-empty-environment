/**
 * 精确有理数矩阵代数。
 *
 * - quadraticForm：vᵀMv，精确。
 * - determinant：高斯消元，精确（供参考验证交叉核对）。
 * - checkPSD：精确 LDLᵀ 分解判定半正定。
 *   · 负主元 → 不定，构造负方向证据 v 使 vᵀMv < 0；
 *   · 零主元但对应 Schur 余子式列非零 → 不定（含零方差维度的合法矩阵
 *     其零主元对应整行整列必为零，此时该维度不贡献，跳过即可）。
 *   返回的证据向量都会用精确二次型复核 vᵀMv < 0 后才返回。
 */

import { Rational } from './rational.ts';
import { Decimal } from './decimal.ts';

export type RVec = Rational[];
export type RMat = Rational[][];

export function vecFromDecimals(v: Decimal[]): RVec {
  return v.map((d) => Rational.from(d));
}

export function matFromDecimals(rows: Decimal[][]): RMat {
  return rows.map((row) => vecFromDecimals(row));
}

export function zeroMat(n: number): RMat {
  return Array.from({ length: n }, () => Array.from({ length: n }, () => Rational.zero()));
}

export function identityMat(n: number): RMat {
  const m = zeroMat(n);
  for (let i = 0; i < n; i++) m[i]![i] = Rational.one();
  return m;
}

/** vᵀMv（利用对称性：对角 + 2×非对角），精确。 */
export function quadraticForm(v: RVec, m: RMat): Rational {
  const n = v.length;
  let acc = Rational.zero();
  for (let i = 0; i < n; i++) {
    if (!v[i]!.isZero()) acc = acc.add(v[i]!.mul(v[i]!).mul(m[i]![i]!));
    for (let j = i + 1; j < n; j++) {
      if (!v[i]!.isZero() && !v[j]!.isZero() && !m[i]![j]!.isZero()) {
        acc = acc.add(v[i]!.mul(m[i]![j]!).mul(v[j]!).mul(Rational.make(2n, 1n)));
      }
    }
  }
  return acc;
}

export function matVec(m: RMat, v: RVec): RVec {
  return m.map((row) => {
    let acc = Rational.zero();
    for (let j = 0; j < v.length; j++) acc = acc.add(row[j]!.mul(v[j]!));
    return acc;
  });
}

/** 精确行列式（有理数高斯消元，部分主元）。 */
export function determinant(m: RMat): Rational {
  const n = m.length;
  const a = m.map((row) => row.slice());
  let det = Rational.one();
  for (let k = 0; k < n; k++) {
    let pivot = -1;
    for (let i = k; i < n; i++) {
      if (!a[i]![k]!.isZero()) {
        pivot = i;
        break;
      }
    }
    if (pivot < 0) return Rational.zero();
    if (pivot !== k) {
      const tmp = a[k]!;
      a[k] = a[pivot]!;
      a[pivot] = tmp;
      det = det.neg();
    }
    const d = a[k]![k]!;
    det = det.mul(d);
    for (let i = k + 1; i < n; i++) {
      if (a[i]![k]!.isZero()) continue;
      const factor = a[i]![k]!.div(d);
      for (let j = k; j < n; j++) {
        a[i]![j] = a[i]![j]!.sub(factor.mul(a[k]![j]!));
      }
    }
  }
  return det;
}

export function checkSymmetric(m: RMat): boolean {
  const n = m.length;
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (!m[i]![j]!.equals(m[j]![i]!)) return false;
    }
  }
  return true;
}

export type PsdResult =
  | { ok: true }
  | { ok: false; witness: RVec; value: Rational };

/**
 * 解 Lᵀv = w，其中 L 为单位下三角，列 0..k-1 已定型，列 ≥ k 视为单位块。
 * 用于把 Schur 余子式空间中的证据向量映射回原坐标。
 */
function solveLTransposePartial(l: RMat, k: number, w: RVec): RVec {
  const n = l.length;
  const v = w.slice();
  for (let i = k - 1; i >= 0; i--) {
    let acc = w[i]!;
    for (let j = i + 1; j < n; j++) {
      if (!l[j]![i]!.isZero()) acc = acc.sub(l[j]![i]!.mul(v[j]!));
    }
    v[i] = acc;
  }
  return v;
}

/**
 * 精确 LDLᵀ 半正定判定。矩阵必须对称（调用方负责检查）。
 * 失败时返回负方向证据 (witness, value=witnessᵀM·witness < 0)。
 */
export function checkPSD(m: RMat): PsdResult {
  const n = m.length;
  const a = m.map((row) => row.slice()); // 就地保存 Schur 余子式
  const l = identityMat(n);

  const fail = (w: RVec): PsdResult => {
    const value = quadraticForm(w, m);
    if (value.sign >= 0) {
      throw new Error('checkPSD 内部错误：证据向量复核失败');
    }
    return { ok: false, witness: w, value };
  };

  for (let k = 0; k < n; k++) {
    const d = a[k]![k]!;
    if (d.sign < 0) {
      // 负主元：取 Schur 空间中的 e_k，映射回原坐标
      const w: RVec = Array.from({ length: n }, () => Rational.zero());
      w[k] = Rational.one();
      return fail(solveLTransposePartial(l, k, w));
    }
    if (d.isZero()) {
      // 零主元：Schur 余子式中该列必须全零，否则不定
      let j = -1;
      for (let r = k + 1; r < n; r++) {
        if (!a[r]![k]!.isZero()) {
          j = r;
          break;
        }
      }
      if (j >= 0) {
        // 2×2 主子阵 [[0, s],[s, dj]] 行列式 = -s² < 0 → 不定
        const s = a[j]![k]!;
        const dj = a[j]![j]!;
        // w = t·e_k + e_j，取 t 使 wᵀAw = 2ts + dj = -(|dj|+1) + dj ≤ -1 < 0
        const mag = dj.abs().add(Rational.one()).div(s.abs().mul(Rational.make(2n, 1n)));
        const t = s.sign > 0 ? mag.neg() : mag;
        const w: RVec = Array.from({ length: n }, () => Rational.zero());
        w[k] = t;
        w[j] = Rational.one();
        return fail(solveLTransposePartial(l, k, w));
      }
      continue; // 零方差维度：整行整列为零，不贡献，跳过
    }
    for (let i = k + 1; i < n; i++) {
      l[i]![k] = a[i]![k]!.div(d);
    }
    for (let i = k + 1; i < n; i++) {
      if (a[i]![k]!.isZero()) continue;
      for (let j = k + 1; j < n; j++) {
        a[i]![j] = a[i]![j]!.sub(a[i]![k]!.mul(a[j]![k]!).div(d));
      }
    }
  }
  return { ok: true };
}
