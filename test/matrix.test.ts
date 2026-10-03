import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, r } from '../src/rational.ts';
import { Matrix, hilbert } from '../src/matrix.ts';

test('Rational keeps canonical form and exact arithmetic', () => {
  assert.equal(r('6/8').toString(), '3/4');
  assert.equal(r('2/-3').toString(), '-2/3');
  assert.equal(r('0/5').toString(), '0');
  assert.equal(r('1/2').add(r('1/3')).toString(), '5/6');
  assert.equal(r('1/2').sub(r('2/3')).toString(), '-1/6');
  assert.equal(r('2/3').mul(r('9/4')).toString(), '3/2');
  assert.equal(r('2/3').div(r('4/5')).toString(), '5/6');
  assert.equal(r('1/3').mul(r(3)).toString(), '1');
  assert.throws(() => r('1/0'), RangeError);
  assert.throws(() => r(0.1), TypeError);
});

test('determinant of small matrices is exact', () => {
  assert.equal(new Matrix([[5]]).determinant().toString(), '5');
  assert.equal(
    new Matrix([
      [1, 2],
      [3, 4],
    ]).determinant().toString(),
    '-2',
  );
  assert.equal(
    new Matrix([
      ['1/2', '1/3'],
      ['1/4', '1/5'],
    ]).determinant().toString(),
    '1/60',
  );
  // 3x3 with a zero pivot on the diagonal forces a row swap.
  assert.equal(
    new Matrix([
      [0, 1, 2],
      [3, 4, 5],
      [6, 7, 9],
    ]).determinant().toString(),
    '-3',
  );
});

test('singularity is decided exactly, not approximately', () => {
  // Rows are rationally dependent: row3 = row1 + row2 (with fractions).
  const singular = new Matrix([
    ['1/2', '1/3', '1/5'],
    ['1/7', '1/11', '1/13'],
    ['9/14', '14/33', '18/65'],
  ]);
  assert.equal(singular.determinant().toString(), '0');
  assert.ok(singular.isSingular());
  assert.throws(() => singular.inverse(), /singular/);
});

test('inverse satisfies A * A^-1 = A^-1 * A = I exactly', () => {
  const a = new Matrix([
    ['2/3', '-1/5', '4/7'],
    ['1/2', '3/11', '-2/13'],
    ['-5/17', '7/19', '1/23'],
  ]);
  const inv = a.inverse();
  const id = Matrix.identity(3);
  assert.ok(a.multiply(inv).equals(id));
  assert.ok(inv.multiply(a).equals(id));
  // inv(inv(A)) === A
  assert.ok(inv.inverse().equals(a));
});

test('5x5 Hilbert matrix: exact determinant and exact inverse', () => {
  const h5 = hilbert(5);

  // Exact determinant: 1 / 266716800000 (floating point loses this entirely).
  const det = h5.determinant();
  assert.equal(det.num, 1n);
  assert.equal(det.den, 266716800000n);
  assert.equal(det.toString(), '1/266716800000');

  // Exact inverse of H5 is a known integer matrix.
  const expectedInv = new Matrix([
    [25, -300, 1050, -1400, 630],
    [-300, 4800, -18900, 26880, -12600],
    [1050, -18900, 79380, -117600, 56700],
    [-1400, 26880, -117600, 179200, -88200],
    [630, -12600, 56700, -88200, 44100],
  ]);
  const inv = h5.inverse();
  assert.ok(inv.equals(expectedInv));

  // The product is strictly the identity matrix, entry by entry.
  const id = Matrix.identity(5);
  assert.ok(h5.multiply(inv).equals(id));
  assert.ok(inv.multiply(h5).equals(id));
});

test('Hilbert determinants of orders 1..5 match known exact values', () => {
  const expected = ['1', '1/12', '1/2160', '1/6048000', '1/266716800000'];
  for (let n = 1; n <= 5; n++) {
    assert.equal(hilbert(n).determinant().toString(), expected[n - 1], `H${n}`);
  }
});

test('larger rational matrix: inverse round-trips to identity', () => {
  const a = new Matrix([
    ['3/2', '0', '-1/3', '2'],
    ['1/5', '-2/7', '1', '0'],
    ['4', '1/9', '-5/2', '3/4'],
    ['-1/2', '2/3', '0', '7/5'],
  ]);
  const inv = a.inverse();
  assert.ok(a.multiply(inv).equals(Matrix.identity(4)));
  assert.ok(inv.multiply(a).equals(Matrix.identity(4)));
  // det(A) * det(A^-1) === 1 exactly
  assert.ok(a.determinant().mul(inv.determinant()).isOne());
});

test('non-square and empty matrices are rejected', () => {
  assert.throws(() => new Matrix([]), RangeError);
  assert.throws(
    () =>
      new Matrix([
        [1, 2, 3],
        [4, 5, 6],
      ]),
    RangeError,
  );
});

test('Rational type is used consistently', () => {
  const h = hilbert(3);
  assert.ok(h.get(0, 0) instanceof Rational);
  assert.equal(h.get(1, 2).toString(), '1/4');
});
