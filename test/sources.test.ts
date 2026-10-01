import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildSourceModel } from '../src/sources.ts';

test('shared source makes inputs fully correlated (no RSS for common error)', () => {
  // x1 = s, x2 = s with Var(s) = 0.09 (u = 0.3):
  // Sigma = [[0.09, 0.09], [0.09, 0.09]], so Var(x1 + x2) = 0.36 = (2u)^2,
  // NOT the independent root-sum-of-squares 0.18.
  const m = buildSourceModel(
    [
      { id: 'x1', value: '10', contributions: [{ source: 'cal' }] },
      { id: 'x2', value: '20', contributions: [{ source: 'cal' }] },
    ],
    [{ id: 'cal', std: '0.3' }],
  );
  assert.deepEqual(m.covariance.map((r) => r.map(String)), [
    ['9/100', '9/100'],
    ['9/100', '9/100'],
  ]);
  assert.deepEqual(m.edges, [{ between: ['x1', 'x2'], sharedSources: ['cal'] }]);
});

test('independent sources give diagonal covariance, no graph edges', () => {
  const m = buildSourceModel(
    [
      { id: 'x1', value: 0, contributions: [{ source: 's1' }] },
      { id: 'x2', value: 0, contributions: [{ source: 's2' }] },
    ],
    [
      { id: 's1', std: '0.1' },
      { id: 's2', std: '0.2' },
    ],
  );
  assert.deepEqual(m.covariance.map((r) => r.map(String)), [
    ['1/100', '0'],
    ['0', '1/25'],
  ]);
  assert.equal(m.edges.length, 0);
});

test('duplicate contribution of the same source is merged, not double counted', () => {
  // gains 1 and 2 for the same source merge to gain 3 -> Var = 9 * 0.01.
  const m = buildSourceModel(
    [{ id: 'x', value: 0, contributions: [{ source: 's' }, { source: 's', gain: 2 }] }],
    [{ id: 's', std: '0.1' }],
  );
  assert.equal(m.covariance[0][0].toString(), '9/100');
  assert.equal(m.warnings.length, 1);
  assert.match(m.warnings[0], /duplicate contribution/);
});

test('sources with identical signatures are flagged as suspected duplicates', () => {
  const m = buildSourceModel(
    [{ id: 'x', value: 0, contributions: [{ source: 'a' }, { source: 'b' }] }],
    [
      { id: 'a', std: '0.1' },
      { id: 'b', std: '0.1' },
    ],
  );
  assert.equal(m.warnings.length, 1);
  assert.match(m.warnings[0], /'a' and 'b'/);
  // As declared (independent) they still sum in quadrature: Var = 2 * 0.01.
  assert.equal(m.covariance[0][0].toString(), '1/50');
});

test('zero-variance source contributes nothing', () => {
  const m = buildSourceModel(
    [
      { id: 'x1', value: 0, contributions: [{ source: 'dead' }, { source: 'live' }] },
      { id: 'x2', value: 0, contributions: [{ source: 'dead' }] },
    ],
    [
      { id: 'dead', variance: 0 },
      { id: 'live', std: '0.5' },
    ],
  );
  assert.deepEqual(m.covariance.map((r) => r.map(String)), [
    ['1/4', '0'],
    ['0', '0'],
  ]);
  assert.equal(m.edges.length, 0); // no live shared source
});

test('mixed shared and independent sources compose exactly', () => {
  // x1 = s1 + s2, x2 = s1; Var(s1) = Var(s2) = 1.
  // Sigma = [[2, 1], [1, 1]]; Var(x1 - x2) = 2 + 1 - 2*1 = 1 = Var(s2).
  const m = buildSourceModel(
    [
      { id: 'x1', value: 0, contributions: [{ source: 's1' }, { source: 's2' }] },
      { id: 'x2', value: 0, contributions: [{ source: 's1' }] },
    ],
    [
      { id: 's1', variance: 1 },
      { id: 's2', variance: 1 },
    ],
  );
  assert.deepEqual(m.covariance.map((r) => r.map(String)), [
    ['2', '1'],
    ['1', '1'],
  ]);
});

test('invalid declarations are rejected', () => {
  assert.throws(
    () => buildSourceModel([{ id: 'x', value: 0, contributions: [{ source: 'ghost' }] }], []),
    /unknown source/,
  );
  assert.throws(
    () =>
      buildSourceModel(
        [{ id: 'x', value: 0 }],
        [
          { id: 's', std: 1 },
          { id: 's', std: 2 },
        ],
      ),
    /Duplicate error source/,
  );
  assert.throws(
    () => buildSourceModel([{ id: 'x', value: 0 }], [{ id: 's', std: 1, variance: 1 }]),
    /exactly one of std \/ variance/,
  );
  assert.throws(
    () => buildSourceModel([{ id: 'x', value: 0 }], [{ id: 's', variance: -1 }]),
    /negative variance/,
  );
});
