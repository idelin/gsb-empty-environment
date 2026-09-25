import { test } from 'node:test';
import assert from 'node:assert/strict';
import { identity, TransformStack } from '../src/matrix.ts';
import { PathBuilder } from '../src/path.ts';
import { fillPath } from '../src/raster.ts';
import type { Mask } from '../src/raster.ts';
import { referenceMask } from '../src/reference.ts';

function assertMaskClose(actual: Mask, expected: Mask, tolerance: number): void {
  assert.equal(actual.data.length, expected.data.length);
  let maxError = 0;
  for (let i = 0; i < actual.data.length; i++) {
    maxError = Math.max(maxError, Math.abs(actual.data[i] - expected.data[i]));
  }
  assert.ok(maxError <= tolerance, `max per-pixel error ${maxError} exceeds ${tolerance}`);
}

test('fractional rect coverage is exact', () => {
  const path = new PathBuilder().rect(2.5, 1.25, 3, 2);
  const mask = fillPath(path.flatten(identity()), 8, 6);
  assert.ok(Math.abs(mask.data[1 * 8 + 2] - 0.375) < 1e-6);
  assert.ok(Math.abs(mask.data[2 * 8 + 3] - 1) < 1e-6);
  assert.ok(Math.abs(mask.data[3 * 8 + 5] - 0.125) < 1e-6);
  assert.ok(Math.abs(mask.data[3 * 8 + 3] - 0.25) < 1e-6);
  assert.equal(mask.data[0], 0);
  assert.equal(mask.data[4 * 8 + 6], 0);
});

test('edge coverage never leaves [0,1], even with overlapping contours', () => {
  const path = new PathBuilder().rect(1, 1, 4, 4).rect(2, 2, 4, 4);
  const mask = fillPath(path.flatten(identity()), 8, 8);
  for (const v of mask.data) {
    assert.ok(v >= 0 && v <= 1, `coverage ${v} out of range`);
  }
  assert.equal(mask.data[3 * 8 + 3], 1);
  assert.equal(mask.data[1 * 8 + 1], 1);
  assert.equal(mask.data[5 * 8 + 5], 1);
});

test('rotated rect matches supersampled reference per pixel', () => {
  const stack = new TransformStack();
  stack.translate(10, 8);
  stack.rotate(Math.PI / 6);
  const contours = new PathBuilder().rect(-4, -3, 8, 6).flatten(stack.current);
  const actual = fillPath(contours, 20, 16);
  const expected = referenceMask(contours, 20, 16, 32);
  assertMaskClose(actual, expected, 0.08);
});

test('bezier blob matches supersampled reference per pixel', () => {
  const stack = new TransformStack();
  stack.translate(10, 8);
  stack.scale(1.3, 0.8);
  stack.rotate(-Math.PI / 9);
  const contours = new PathBuilder()
    .moveTo(-5, 0)
    .cubicTo(-5, -4, 5, -4, 5, 0)
    .cubicTo(5, 4, -5, 4, -5, 0)
    .closePath()
    .flatten(stack.current);
  const actual = fillPath(contours, 20, 16);
  const expected = referenceMask(contours, 20, 16, 32);
  assertMaskClose(actual, expected, 0.08);
});

test('quadratic curve path matches supersampled reference per pixel', () => {
  const contours = new PathBuilder()
    .moveTo(2, 12)
    .quadraticTo(10, -4, 18, 12)
    .quadraticTo(10, 8, 2, 12)
    .closePath()
    .flatten(identity());
  const actual = fillPath(contours, 20, 16);
  const expected = referenceMask(contours, 20, 16, 32);
  assertMaskClose(actual, expected, 0.08);
});

test('path with hole (reverse-wound contour) matches reference', () => {
  const path = new PathBuilder().rect(1, 1, 6, 5);
  path.moveTo(3, 2).lineTo(3, 4).lineTo(5, 4).lineTo(5, 2).closePath();
  const contours = path.flatten(identity());
  const actual = fillPath(contours, 8, 8);
  const expected = referenceMask(contours, 8, 8, 32);
  assertMaskClose(actual, expected, 0.08);
  assert.equal(actual.data[3 * 8 + 4], 0);
  assert.equal(actual.data[1 * 8 + 1], 1);
});
