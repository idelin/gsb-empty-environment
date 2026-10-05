import { test } from 'node:test';
import assert from 'node:assert/strict';
import { analyzeQuadraticSign } from '../src/index.ts';

test('tangential quadratic (theta - 1)^2: double root at endpoint', () => {
  const a = analyzeQuadraticSign(1, -2, 1, 1);
  assert.equal(a.classification, 'convex-vertex-outside');
  assert.equal(a.discriminant, 0);
  assert.deepEqual(a.rootsInUnitInterval, [1]);
  assert.equal(a.certifiedMin, 0);
  assert.ok(a.holds);
});

test('interior tangency 4(theta - 1/2)^2: exact double root at 1/2', () => {
  const a = analyzeQuadraticSign(1, -4, 4, 1);
  assert.equal(a.classification, 'convex-vertex-inside');
  assert.equal(a.discriminant, 0);
  assert.equal(a.rootsInUnitInterval.length, 1);
  assert.ok(Math.abs(a.rootsInUnitInterval[0] - 0.5) < 1e-15);
  assert.equal(a.certifiedMin, 0);
  assert.ok(a.holds);
});

test('quadratic with exact roots 1/3 and 2/3 is certified non-monotone', () => {
  // W(theta) = 9 (theta - 1/3)(theta - 2/3) = 2 - 9 theta + 9 theta^2
  const a = analyzeQuadraticSign(2, -9, 9, 1);
  assert.equal(a.classification, 'convex-vertex-inside');
  assert.equal(a.discriminant, 81 - 72);
  assert.equal(a.rootsInUnitInterval.length, 2);
  assert.ok(Math.abs(a.rootsInUnitInterval[0] - 1 / 3) < 1e-12);
  assert.ok(Math.abs(a.rootsInUnitInterval[1] - 2 / 3) < 1e-12);
  // Exact vertex value: 2 - 81/36 = -1/4.
  assert.ok(Math.abs(a.certifiedMin - -0.25) < 1e-12);
  assert.ok(!a.holds);
});

test('concave quadratic 2 theta (1 - theta): endpoint roots only', () => {
  const a = analyzeQuadraticSign(0, 2, -2, 1);
  assert.equal(a.classification, 'concave-endpoint-min');
  assert.deepEqual(a.rootsInUnitInterval, [0, 1]);
  assert.equal(a.certifiedMin, 0);
  assert.ok(a.holds);
});

test('sigma = -1 flips the certified direction', () => {
  const a = analyzeQuadraticSign(0, -2, 2, -1);
  assert.equal(a.classification, 'concave-endpoint-min');
  assert.equal(a.certifiedMin, 0);
  assert.ok(a.holds);
});

test('linear W: endpoint minimum, root outside interval', () => {
  const a = analyzeQuadraticSign(3, -1, 0, 1);
  assert.equal(a.classification, 'linear');
  assert.equal(a.certifiedMin, 2);
  assert.deepEqual(a.rootsInUnitInterval, []);
  assert.ok(a.holds);
});

test('identically zero W (plateau derivative)', () => {
  const a = analyzeQuadraticSign(0, 0, 0, 1);
  assert.equal(a.classification, 'identically-zero');
  assert.equal(a.certifiedMin, 0);
  assert.ok(a.holds);
});

test('convex vertex inside: certifiedMin equals exact vertex value', () => {
  // W(theta) = 2 - 4 theta + 3 theta^2, vertex at theta = 2/3, value 2/3.
  const a = analyzeQuadraticSign(2, -4, 3, 1);
  assert.equal(a.classification, 'convex-vertex-inside');
  assert.ok(Math.abs(a.vertexTheta! - 2 / 3) < 1e-15);
  assert.ok(Math.abs(a.certifiedMin - 2 / 3) < 1e-12);
  assert.ok(a.discriminant! < 0);
  assert.deepEqual(a.rootsInUnitInterval, []);
  assert.ok(a.holds);
});
