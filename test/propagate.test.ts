import { test } from 'node:test';
import assert from 'node:assert/strict';
import { NonPSDError, propagateFirstOrder, propagateLinear } from '../src/propagate.ts';

// Reference values are hand-derived exact algebra; no empirical sampling
// is used anywhere in this suite.

test('independent linear propagation is exact', () => {
  // y = 2 x1 + 3 x2, u1 = 0.1, u2 = 0.2 (independent)
  // Var(y) = 4*0.01 + 9*0.04 = 0.40 = 2/5
  const r = propagateLinear({
    inputs: [
      { id: 'x1', value: '10' },
      { id: 'x2', value: '20' },
    ],
    outputs: [{ id: 'y', coefficients: [2, 3] }],
    covariance: [
      ['0.01', 0],
      [0, '0.04'],
    ],
  });
  const y = r.outputs[0];
  assert.equal(y.value.toString(), '80');
  assert.equal(y.variance.toString(), '2/5');
  assert.equal(y.std.decimal, '0.632456'); // sqrt(2/5), half-even, 6 places
  assert.equal(y.std.rounding, 'half-even');
  assert.equal(y.std.places, 6);
  assert.equal(
    y.varianceExpression,
    'Var(y) = (2)^2*Var(x1) + (3)^2*Var(x2) = 2/5',
  );
  // Exact coefficients are echoed back.
  assert.deepEqual(
    r.coefficients[0].terms.map((t) => [t.input, t.coefficient.toString()]),
    [
      ['x1', '2'],
      ['x2', '3'],
    ],
  );
  assert.equal(r.kind, 'linear-exact');
  assert.ok(r.assumptions.some((a) => a.includes('精确线性')));
});

test('offset is exact and does not affect variance', () => {
  const r = propagateLinear({
    inputs: [{ id: 'x', value: '1.5' }],
    outputs: [{ id: 'y', coefficients: ['2'], offset: '5' }],
    covariance: [['0.25']],
  });
  assert.equal(r.outputs[0].value.toString(), '8');
  assert.equal(r.outputs[0].variance.toString(), '1'); // 4 * 0.25
  assert.equal(r.outputs[0].std.decimal, '1.000000');
});

test('fully correlated errors add linearly, not in quadrature', () => {
  // x1 and x2 share one calibration source with u = 0.3.
  // y = x1 + x2 -> Var = (0.3 + 0.3)^2 = 0.36 = 9/25, std = 0.6 exactly.
  const r = propagateLinear({
    inputs: [
      { id: 'x1', value: 0, contributions: [{ source: 'cal' }] },
      { id: 'x2', value: 0, contributions: [{ source: 'cal' }] },
    ],
    outputs: [{ id: 'y', coefficients: [1, 1] }],
    sources: [{ id: 'cal', std: '0.3' }],
  });
  const y = r.outputs[0];
  assert.equal(y.variance.toString(), '9/25');
  assert.notEqual(y.variance.toString(), '9/50'); // independent RSS would be wrong
  assert.equal(y.std.decimal, '0.600000');
  assert.equal(
    y.varianceExpression,
    'Var(y) = (1)^2*Var(x1) + (1)^2*Var(x2) + 2*(1)(1)*Cov(x1,x2) = 9/25',
  );
});

test('zero-variance dimensions do not contribute', () => {
  const r = propagateLinear({
    inputs: [
      { id: 'exact', value: '7' },
      { id: 'noisy', value: '1' },
    ],
    outputs: [{ id: 'y', coefficients: [1, 1] }],
    covariance: [
      [0, 0],
      [0, '0.04'],
    ],
  });
  const y = r.outputs[0];
  assert.equal(y.variance.toString(), '1/25');
  assert.equal(y.varianceExpression, 'Var(y) = (1)^2*Var(noisy) = 1/25');
});

test('contradictory covariance is rejected with negative-direction evidence', () => {
  // |Cov| = 2 > sqrt(1*1): no valid random vector has this covariance.
  try {
    propagateLinear({
      inputs: [
        { id: 'x1', value: 0 },
        { id: 'x2', value: 0 },
      ],
      outputs: [{ id: 'y', coefficients: [1, 1] }],
      covariance: [
        [1, 2],
        [2, 1],
      ],
    });
    assert.fail('should have thrown');
  } catch (e) {
    assert.ok(e instanceof NonPSDError);
    assert.equal(e.failure.quadraticForm.sign(), -1);
    assert.match(e.message, /v\^T Sigma v = -?\d/);
  }
});

test('nonlinear model requires explicit first-order declaration', () => {
  const base = {
    inputs: [
      { id: 'V', value: '2' },
      { id: 'I', value: '5' },
    ],
    outputs: [{ id: 'P', valueAtPoint: '10', derivatives: ['5', '2'] }],
    covariance: [
      ['0.01', 0],
      [0, '0.04'],
    ],
  };
  assert.throws(
    () => propagateFirstOrder({ ...base, firstOrderDeclared: false }),
    /first-order Taylor approximation must be explicitly declared/,
  );
  // P = V*I at (2,5): dP = 5 dV + 2 dI
  // Var = 25*0.01 + 4*0.04 = 0.41 = 41/100
  const r = propagateFirstOrder({
    ...base,
    firstOrderDeclared: true,
    validityNote: '小信号范围内 (|dV| < 0.5)',
  });
  assert.equal(r.kind, 'nonlinear-first-order');
  assert.equal(r.outputs[0].value.toString(), '10');
  assert.equal(r.outputs[0].variance.toString(), '41/100');
  assert.ok(r.assumptions.some((a) => a.includes('一阶泰勒近似')));
  assert.ok(r.assumptions.some((a) => a.includes('小信号范围内')));
});

test('big values propagate exactly without Number intermediary', () => {
  const r = propagateLinear({
    inputs: [{ id: 'big', value: '1000000000000000000000000000001' }], // 10^30 + 1
    outputs: [{ id: 'y', coefficients: [3] }],
    covariance: [['1e-36']],
  });
  const y = r.outputs[0];
  assert.equal(y.value.toString(), '3000000000000000000000000000003');
  assert.equal(y.variance.toString(), '9/1000000000000000000000000000000000000');
});

test('spec validation', () => {
  const inputs = [{ id: 'x', value: 0 }];
  const outputs = [{ id: 'y', coefficients: [1] }];
  assert.throws(
    () => propagateLinear({ inputs, outputs }),
    /exactly one of `covariance` or `sources`/,
  );
  assert.throws(
    () => propagateLinear({ inputs, outputs, covariance: [[1]], sources: [] }),
    /exactly one of `covariance` or `sources`/,
  );
  assert.throws(
    () => propagateLinear({ inputs, outputs, covariance: [[1, 0]] }),
    /must be square|dimension/,
  );
  assert.throws(
    () =>
      propagateLinear({
        inputs: [
          { id: 'a', value: 0 },
          { id: 'b', value: 0 },
        ],
        outputs: [{ id: 'y', coefficients: [1, 1] }],
        covariance: [
          [1, '0.5'],
          ['0.6', 1],
        ],
      }),
    /symmetric/,
  );
  assert.throws(
    () =>
      propagateLinear({
        inputs,
        outputs: [{ id: 'y', coefficients: [1, 1] }],
        covariance: [[1]],
      }),
    /coefficients for 1 inputs/,
  );
});
