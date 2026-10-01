import test from 'node:test';
import assert from 'node:assert/strict';
import {
  Stage,
  apply,
  invert,
  isSingular,
  multiply,
  rotation,
  scaling,
  translation,
} from '../src/index.ts';
import { approx } from './helpers.ts';

test('matrix multiply composes transforms', () => {
  const m = multiply(translation(3, 4), scaling(2, 2));
  const [x, y] = apply(m, 1, 1);
  approx(x, 5, 1e-12, 'x');
  approx(y, 6, 1e-12, 'y');
});

test('invert undoes apply', () => {
  const m = multiply(translation(2, -3), multiply(rotation(0.7), scaling(1.5, 0.5)));
  const inv = invert(m);
  const [x, y] = apply(inv, ...apply(m, 4, -2));
  approx(x, 4, 1e-9, 'x');
  approx(y, -2, 1e-9, 'y');
});

test('rotation by 90 degrees maps x axis to y axis', () => {
  const [x, y] = apply(rotation(Math.PI / 2), 1, 0);
  approx(x, 0, 1e-12, 'x');
  approx(y, 1, 1e-12, 'y');
});

test('singular matrices are detected and rejected', () => {
  assert.ok(isSingular({ a: 1, b: 2, c: 2, d: 4, e: 0, f: 0 }));
  assert.ok(isSingular(scaling(0, 1)));
  assert.ok(isSingular(scaling(1e-15, 1e-15)));
  assert.ok(!isSingular(scaling(1, 2)));
  assert.throws(
    () => Stage.affine(4, 4, 4, 4, scaling(0, 1), 'bilinear'),
    /singular/,
  );
  assert.throws(
    () => Stage.affine(4, 4, 4, 4, { a: 1, b: 2, c: 2, d: 4, e: 0, f: 0 }, 'area'),
    /singular/,
  );
});
