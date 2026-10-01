import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import { Rational } from '../src/rational.ts';
import {
  checkPSD,
  determinant,
  matFromDecimals,
  quadraticForm,
  type RMat,
} from '../src/matrix.ts';

const D = Decimal.from;
const R = Rational.make;
const M = (rows: string[][]): RMat => matFromDecimals(rows.map((r) => r.map(D)));

/** 独立参照实现：Laplace 展开求行列式（与 matrix.ts 的高斯消元不同算法）。 */
function detLaplace(m: RMat): Rational {
  const n = m.length;
  if (n === 0) return Rational.one();
  if (n === 1) return m[0]![0]!;
  let acc = Rational.zero();
  for (let j = 0; j < n; j++) {
    const sub = m.slice(1).map((row) => row.filter((_, k) => k !== j));
    const term = m[0]![j]!.mul(detLaplace(sub));
    acc = j % 2 === 0 ? acc.add(term) : acc.sub(term);
  }
  return acc;
}

/** Sylvester 判据（对称矩阵）：全部主子式 ≥ 0 ⟺ 半正定。独立参照。 */
function allPrincipalMinorsNonneg(m: RMat): boolean {
  const n = m.length;
  for (let mask = 1; mask < 1 << n; mask++) {
    const idx: number[] = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) idx.push(i);
    const sub = idx.map((i) => idx.map((j) => m[i]![j]!));
    if (detLaplace(sub).sign < 0) return false;
  }
  return true;
}

test('二次型精确：a=(3,4)，独立方差 0.01/0.04 → 73/100', () => {
  const m = M([
    ['0.01', '0'],
    ['0', '0.04'],
  ]);
  const v = [R(3n, 1n), R(4n, 1n)];
  const q = quadraticForm(v, m);
  assert.equal(q.toString(), '73/100');
  assert.equal(q.toDecimalExact()!.toString(), '0.73');
});

test('行列式：与高斯消元无关的 Laplace 参照交叉验证', () => {
  const a = M([
    ['1', '2'],
    ['3', '4'],
  ]);
  assert.equal(determinant(a).toString(), '-2');
  assert.equal(detLaplace(a).toString(), '-2');

  // 3 阶 Hilbert 矩阵：det = 1/2160（已知参考值）
  const h3 = M([
    ['1', '0.5', '0.333333333333333333333333333333'],
    ['0.5', '0.333333333333333333333333333333', '0.25'],
    ['0.333333333333333333333333333333', '0.25', '0.2'],
  ]);
  // 用精确分数构造同一矩阵
  const h3exact: RMat = [
    [R(1n, 1n), R(1n, 2n), R(1n, 3n)],
    [R(1n, 2n), R(1n, 3n), R(1n, 4n)],
    [R(1n, 3n), R(1n, 4n), R(1n, 5n)],
  ];
  assert.equal(determinant(h3exact).toString(), '1/2160');
  assert.equal(detLaplace(h3exact).toString(), '1/2160');
  // 同一截断矩阵，两种算法必须给出完全相同的精确值
  assert.equal(determinant(h3).toString(), detLaplace(h3).toString());
  // 截断矩阵的行列式与 1/2160 之差非零但小于 1e-29（精确算术可分辨）
  const diff = determinant(h3).sub(R(1n, 2160n)).abs();
  assert.ok(diff.sign > 0);
  assert.ok(diff.cmp(Rational.from(D('1e-29'))) < 0);
});

test('半正定判定与 Sylvester 判据交叉验证', () => {
  const cases: Array<{ name: string; m: RMat }> = [
    {
      name: '正定',
      m: M([
        ['2', '1', '0'],
        ['1', '2', '1'],
        ['0', '1', '2'],
      ]),
    },
    {
      name: '不定（负主子式）',
      m: M([
        ['1', '2', '0'],
        ['2', '1', '0'],
        ['0', '0', '1'],
      ]),
    },
    {
      name: '秩 1 半正定（完全相关）',
      m: M([
        ['1', '1', '1'],
        ['1', '1', '1'],
        ['1', '1', '1'],
      ]),
    },
    {
      name: '不定（负对角元）',
      m: M([
        ['1', '0', '0'],
        ['0', '-1', '0'],
        ['0', '0', '1'],
      ]),
    },
    {
      name: '零主元+非零列 → 不定',
      m: M([
        ['0', '0', '0'],
        ['0', '1', '2'],
        ['0', '2', '1'],
      ]),
    },
    {
      name: '含零方差维度的合法半正定',
      m: M([
        ['1', '0', '0'],
        ['0', '0', '0'],
        ['0', '0', '2'],
      ]),
    },
  ];
  for (const { name, m } of cases) {
    const psd = checkPSD(m);
    const reference = allPrincipalMinorsNonneg(m);
    assert.equal(psd.ok, reference, `${name}: LDL 判定应与 Sylvester 判据一致`);
    if (!psd.ok) {
      // 负方向证据必须精确满足 vᵀMv < 0
      const check = quadraticForm(psd.witness, m);
      assert.equal(check.toString(), psd.value.toString());
      assert.ok(psd.value.sign < 0, `${name}: 证据值必须为负`);
    }
  }
});

test('矛盾协方差 [[1,2],[2,1]]：负方向证据精确值 -3', () => {
  const m = M([
    ['1', '2'],
    ['2', '1'],
  ]);
  const psd = checkPSD(m);
  assert.ok(!psd.ok);
  assert.equal(psd.value.toString(), '-3');
  assert.equal(quadraticForm(psd.witness, m).toString(), '-3');
});

test('零方差维度不贡献：二次型与降维结果一致', () => {
  const full = M([
    ['0.04', '0', '0'],
    ['0', '0', '0'],
    ['0', '0', '0.09'],
  ]);
  const reduced = M([
    ['0.04', '0'],
    ['0', '0.09'],
  ]);
  const vFull = [R(1n, 1n), R(7n, 1n), R(2n, 1n)];
  const vReduced = [R(1n, 1n), R(2n, 1n)];
  assert.equal(quadraticForm(vFull, full).toString(), quadraticForm(vReduced, reduced).toString());
  assert.ok(checkPSD(full).ok);
});
