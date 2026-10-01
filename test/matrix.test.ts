import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational } from '../src/rational.ts';
import { checkPSD, matMul, quadForm, toMatrix, transpose } from '../src/matrix.ts';

// All expected values below are hand-derived reference algebra (exact
// rationals), not Monte-Carlo estimates: empirical sample fluctuation is
// not accepted as proof of propagation correctness.
test('reference 2x2: c=(3,-2), Sigma=[[4,1],[1,9]] -> Var = 60', () => {
  // 9*4 + 2*3*(-2)*1 + 4*9 = 36 - 12 + 36 = 60
  const S = toMatrix([[4, 1], [1, 9]]);
  const c = [Rational.of(3), Rational.of(-2)];
  assert.equal(quadForm(c, S).toString(), '60');
});

test('reference 3x3: c=(1,1,1) sums all entries; c=(1,-1,2) gives 15', () => {
  const S = toMatrix([
    [2, 1, 0],
    [1, 3, 1],
    [0, 1, 4],
  ]);
  assert.equal(quadForm([1, 1, 1].map(Rational.of), S).toString(), '13');
  // Sigma*c = [1, 0, 7]; c.(Sigma c) = 1 + 0 + 14 = 15
  assert.equal(quadForm([1, -1, 2].map(Rational.of), S).toString(), '15');
});

test('A Sigma A^T matches hand computation', () => {
  // A = [[1,1],[1,-1]], Sigma = diag(2,3):
  // A Sigma A^T = [[5, -1], [-1, 5]]
  const A = toMatrix([
    [1, 1],
    [1, -1],
  ]);
  const S = toMatrix([
    [2, 0],
    [0, 3],
  ]);
  const out = matMul(matMul(A, S), transpose(A));
  assert.deepEqual(out.map((r) => r.map(String)), [
    ['5', '-1'],
    ['-1', '5'],
  ]);
});

test('PSD acceptance, including zero-variance dimensions', () => {
  assert.deepEqual(checkPSD(toMatrix([[2, 1], [1, 2]])).psd, true);
  // Rank-1 with a zero-variance dimension: PSD, rank 1.
  const r1 = checkPSD(toMatrix([[0, 0], [0, 4]]));
  assert.equal(r1.psd, true);
  assert.equal(r1.rank, 1);
  // Fully correlated rank-1 matrix [[1,1],[1,1]] is PSD.
  const fc = checkPSD(toMatrix([[1, 1], [1, 1]]));
  assert.equal(fc.psd, true);
  assert.equal(fc.rank, 1);
  // Empty matrix is trivially PSD.
  assert.equal(checkPSD([]).psd, true);
});

test('non-PSD returns exact negative-direction evidence', () => {
  // Contradictory covariance: |Cov| = 2 > sqrt(1*1) (|rho| > 1).
  const S = toMatrix([
    [1, 2],
    [2, 1],
  ]);
  const res = checkPSD(S);
  assert.equal(res.psd, false);
  if (res.psd) return;
  // The witness must satisfy v^T S v < 0 exactly.
  assert.equal(res.failure.quadraticForm.sign(), -1);
  assert.equal(quadForm(res.failure.vector, S).cmp(res.failure.quadraticForm), 0);
});

test('zero diagonal with nonzero coupling is indefinite, with witness', () => {
  const S = toMatrix([
    [0, 1],
    [1, 0],
  ]);
  const res = checkPSD(S);
  assert.equal(res.psd, false);
  if (res.psd) return;
  assert.equal(res.failure.reason, 'indefinite-zero-diagonal');
  // w^T S w = -2 exactly for w = (1, -1).
  assert.equal(res.failure.quadraticForm.toString(), '-2');
});

test('non-PSD detected after nontrivial pivoting (3x3)', () => {
  // Leading 2x2 block is PSD; the Schur complement goes negative.
  const S = toMatrix([
    [1, 0, 2],
    [0, 1, 0],
    [2, 0, 1],
  ]);
  const res = checkPSD(S);
  assert.equal(res.psd, false);
  if (res.psd) return;
  assert.equal(res.failure.quadraticForm.sign(), -1);
  assert.equal(quadForm(res.failure.vector, S).cmp(res.failure.quadraticForm), 0);
});
