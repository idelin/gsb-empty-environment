import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { RationalMatrix } from '../src/matrix.ts';
import { hilbert } from '../src/hilbert.ts';

test('5 阶希尔伯特矩阵构造正确', () => {
  const H = hilbert(5);
  assert.equal(H.size, 5);
  assert.ok(H.at(0, 0).equals(1n));
  assert.ok(H.at(0, 4).equals(new Rational(1n, 5n)));
  assert.ok(H.at(4, 4).equals(new Rational(1n, 9n)));
  assert.ok(H.at(2, 3).equals(new Rational(1n, 6n)));
});

test('5 阶希尔伯特矩阵精确行列式 = 1/266716800000', () => {
  const det = hilbert(5).determinant();
  assert.equal(det.num, 1n);
  assert.equal(det.den, 266716800000n);
  // 对照：浮点直接计算该行列式会有明显相对误差
  assert.notEqual(1 / 266716800000, 0);
});

test('5 阶希尔伯特矩阵精确逆矩阵（已知整数解）', () => {
  const expected = [
    [25n, -300n, 1050n, -1400n, 630n],
    [-300n, 4800n, -18900n, 26880n, -12600n],
    [1050n, -18900n, 79380n, -117600n, 56700n],
    [-1400n, 26880n, -117600n, 179200n, -88200n],
    [630n, -12600n, 56700n, -88200n, 44100n],
  ];
  const inv = hilbert(5).inverse();
  for (let i = 0; i < 5; i++) {
    for (let j = 0; j < 5; j++) {
      assert.ok(
        inv.at(i, j).equals(expected[i][j]),
        `inv[${i}][${j}] expected ${expected[i][j]}, got ${inv.at(i, j).toString()}`,
      );
    }
  }
});

test('H * H^{-1} === I 且 H^{-1} * H === I（严格为单位阵）', () => {
  const H = hilbert(5);
  const inv = H.inverse();
  const eye = RationalMatrix.identity(5);
  assert.ok(H.mul(inv).equals(eye));
  assert.ok(inv.mul(H).equals(eye));
});

test('det(H) * det(H^{-1}) === 1', () => {
  const H = hilbert(5);
  const detInv = H.inverse().determinant();
  assert.ok(H.determinant().mul(detInv).equals(1n));
});

test('各阶希尔伯特行列式与已知精确值一致', () => {
  // OEIS A005249: det(H_n) 的倒数
  const expectedDenominators = [1n, 12n, 2160n, 6048000n, 266716800000n];
  for (let n = 1; n <= 5; n++) {
    const det = hilbert(n).determinant();
    assert.equal(det.num, 1n, `n=${n} numerator`);
    assert.equal(det.den, expectedDenominators[n - 1], `n=${n} denominator`);
  }
});

test('更高阶（8 阶）希尔伯特矩阵求逆仍精确复原单位阵', () => {
  const H = hilbert(8);
  const inv = H.inverse();
  assert.ok(H.mul(inv).equals(RationalMatrix.identity(8)));
});
