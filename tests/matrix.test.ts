import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { RationalMatrix, SingularMatrixError, bareissDeterminant } from '../src/matrix.ts';

test('Bareiss：整数矩阵行列式', () => {
  assert.equal(
    bareissDeterminant([
      [2n, 0n],
      [0n, 3n],
    ]),
    6n,
  );
  assert.equal(
    bareissDeterminant([
      [1n, 2n, 3n],
      [0n, 1n, 4n],
      [5n, 6n, 0n],
    ]),
    1n,
  );
  // 需要换主元的矩阵
  assert.equal(
    bareissDeterminant([
      [0n, 1n],
      [1n, 0n],
    ]),
    -1n,
  );
  // 奇异矩阵精确得 0
  assert.equal(
    bareissDeterminant([
      [1n, 2n, 3n],
      [4n, 5n, 6n],
      [7n, 8n, 9n],
    ]),
    0n,
  );
});

test('有理矩阵行列式：分数元素精确处理', () => {
  const m = new RationalMatrix([
    ['1/2', '1/3'],
    ['1/4', '1/5'],
  ]);
  // 1/2*1/5 - 1/3*1/4 = 1/10 - 1/12 = 1/60
  assert.ok(m.determinant().equals(new Rational(1n, 60n)));
});

test('1x1 与 2x2 求逆', () => {
  const one = new RationalMatrix([[4]]);
  assert.ok(one.inverse().at(0, 0).equals(new Rational(1n, 4n)));

  const m = new RationalMatrix([
    [2, 1],
    [1, 3],
  ]);
  const inv = m.inverse();
  // det = 5, inv = 1/5 * [[3,-1],[-1,2]]
  assert.ok(inv.at(0, 0).equals(new Rational(3n, 5n)));
  assert.ok(inv.at(0, 1).equals(new Rational(-1n, 5n)));
  assert.ok(inv.at(1, 0).equals(new Rational(-1n, 5n)));
  assert.ok(inv.at(1, 1).equals(new Rational(2n, 5n)));
});

test('A * A^{-1} === I 且 A^{-1} * A === I（严格相等）', () => {
  const m = new RationalMatrix([
    ['2/3', '-1/5', '3/7'],
    ['4', '0.25', '-2'],
    ['1/8', '5/6', '7/9'],
  ]);
  const inv = m.inverse();
  const eye = RationalMatrix.identity(3);
  assert.ok(m.mul(inv).equals(eye));
  assert.ok(inv.mul(m).equals(eye));
});

test('奇异性精确判定：det=0，inverse 抛 SingularMatrixError', () => {
  const singular = new RationalMatrix([
    [1, 2, 3],
    [4, 5, 6],
    [7, 8, 9],
  ]);
  assert.ok(singular.determinant().isZero());
  assert.ok(singular.isSingular());
  assert.throws(() => singular.inverse(), SingularMatrixError);

  // 行重复 / 分数行成比例，同样精确判奇异
  const proportional = new RationalMatrix([
    ['1/2', '1/3'],
    ['3/2', '1'],
  ]);
  assert.ok(proportional.isSingular());
  assert.throws(() => proportional.inverse(), SingularMatrixError);
});

test('非方阵构造被拒绝', () => {
  assert.throws(() => new RationalMatrix([[1, 2, 3], [4, 5, 6]]), /square/);
});

test('行列式性质：det(A*B) = det(A)*det(B)，det(kA) = k^n det(A)', () => {
  const a = new RationalMatrix([
    [1, '1/2'],
    ['1/3', 2],
  ]);
  const b = new RationalMatrix([
    [3, '-1/4'],
    ['2/5', 1],
  ]);
  assert.ok(a.mul(b).determinant().equals(a.determinant().mul(b.determinant())));
  const scaled = new RationalMatrix([
    [3, '3/2'],
    [1, 6],
  ]);
  assert.ok(scaled.determinant().equals(a.determinant().mul(9n)));
});
