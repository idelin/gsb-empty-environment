import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  TransformStack,
  applyToPoint,
  multiply,
  translation,
  rotation,
  scaling,
} from '../src/matrix.ts';
import { PathBuilder } from '../src/path.ts';
import { fillPath } from '../src/raster.ts';

test('nested transforms compose like matrix multiplication', () => {
  const stack = new TransformStack();
  stack.translate(10, 5);
  stack.save();
  stack.rotate(Math.PI / 2);
  stack.translate(3, 0);
  const p = applyToPoint(stack.current, 1, 0);
  assert.ok(Math.abs(p.x - 10) < 1e-9, `x=${p.x}`);
  assert.ok(Math.abs(p.y - 9) < 1e-9, `y=${p.y}`);
  stack.restore();
  const q = applyToPoint(stack.current, 1, 0);
  assert.ok(Math.abs(q.x - 11) < 1e-9 && Math.abs(q.y - 5) < 1e-9);
});

test('deeply nested transform matches manual matrix product', () => {
  const stack = new TransformStack();
  stack.translate(4, 2);
  stack.save();
  stack.scale(2, 3);
  stack.save();
  stack.rotate(Math.PI / 6);
  stack.save();
  stack.translate(1, -1);
  const manual = multiply(
    translation(4, 2),
    multiply(scaling(2, 3), multiply(rotation(Math.PI / 6), translation(1, -1))),
  );
  const points: Array<[number, number]> = [
    [0, 0],
    [1, 0],
    [0, 1],
    [2.5, -3.5],
  ];
  for (const [x, y] of points) {
    const viaStack = applyToPoint(stack.current, x, y);
    const viaManual = applyToPoint(manual, x, y);
    assert.ok(Math.abs(viaStack.x - viaManual.x) < 1e-9);
    assert.ok(Math.abs(viaStack.y - viaManual.y) < 1e-9);
  }
});

test('rasterizing under nested transform equals rasterizing transformed geometry', () => {
  const stack = new TransformStack();
  stack.translate(7.5, 4.25);
  stack.rotate(Math.PI / 5);
  stack.scale(1.5, 0.75);
  const path = new PathBuilder().rect(-2, -1, 4, 2);
  const viaStack = fillPath(path.flatten(stack.current), 20, 16);
  const corners = [
    [-2, -1],
    [2, -1],
    [2, 1],
    [-2, 1],
  ].map(([x, y]) => applyToPoint(stack.current, x, y));
  const direct = fillPath([corners], 20, 16);
  assert.deepEqual([...viaStack.data], [...direct.data]);
});
