import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TransformStack } from '../src/matrix.ts';
import { PathBuilder } from '../src/path.ts';
import {
  applyMask,
  fillPath,
  invertMask,
  maskUnion,
} from '../src/raster.ts';
import { referenceMask } from '../src/reference.ts';
import { RgbaImage } from '../src/image.ts';

test('composite matches hand-computed source-over', () => {
  const img = new RgbaImage(4, 4, [255, 255, 255, 255]);
  const mask = { width: 4, height: 4, data: new Float32Array(16) };
  mask.data[0] = 0.5;
  img.composite(mask, [0, 0, 0, 255]);
  const [r, g, b, a] = img.pixel(0, 0);
  assert.equal(a, 255);
  assert.ok(Math.abs(r - 128) <= 1, `r=${r}`);
  assert.ok(Math.abs(g - 128) <= 1);
  assert.ok(Math.abs(b - 128) <= 1);
  assert.deepEqual(img.pixel(1, 0), [255, 255, 255, 255]);
});

test('semi-transparent color over transparent background scales alpha by coverage', () => {
  const img = new RgbaImage(2, 2, [0, 0, 0, 0]);
  const mask = { width: 2, height: 2, data: new Float32Array([0.5, 0, 0, 0]) };
  img.composite(mask, [200, 100, 50, 128]);
  const [r, g, b, a] = img.pixel(0, 0);
  const expectedA = (128 / 255) * 0.5;
  assert.ok(Math.abs(a / 255 - expectedA) <= 1 / 255 + 1e-9, `a=${a}`);
  assert.ok(Math.abs(r - 200) <= 1);
  assert.ok(Math.abs(g - 100) <= 1);
  assert.ok(Math.abs(b - 50) <= 1);
});

test('end-to-end scene with nested transforms, boolean ops and inverse mask matches reference', () => {
  const width = 32;
  const height = 24;

  const stackA = new TransformStack();
  stackA.translate(12, 11);
  stackA.rotate(Math.PI / 8);
  stackA.scale(1.2, 0.9);
  const contoursA = new PathBuilder()
    .moveTo(-6, 0)
    .cubicTo(-6, -5, 6, -5, 6, 0)
    .cubicTo(6, 5, -6, 5, -6, 0)
    .closePath()
    .flatten(stackA.current);

  const stackB = new TransformStack();
  stackB.translate(21, 13);
  stackB.rotate(-Math.PI / 10);
  const contoursB = new PathBuilder().rect(-5, -4, 10, 8).flatten(stackB.current);

  const region = { x: 2, y: 2, width: 28, height: 20 };

  const maskA = fillPath(contoursA, width, height);
  const maskB = fillPath(contoursB, width, height);
  const union = maskUnion(maskA, maskB);
  const inverse = invertMask(maskB, region);
  const chained = applyMask(union, inverse, maskA);

  const refA = referenceMask(contoursA, width, height, 32);
  const refB = referenceMask(contoursB, width, height, 32);
  const refUnion = maskUnion(refA, refB);
  const refInverse = invertMask(refB, region);
  const refChained = applyMask(refUnion, refInverse, refA);

  const image = new RgbaImage(width, height, [255, 255, 255, 255]);
  image.composite(union, [40, 90, 220, 255]);
  image.composite(chained, [230, 60, 60, 200]);

  const reference = new RgbaImage(width, height, [255, 255, 255, 255]);
  reference.composite(refUnion, [40, 90, 220, 255]);
  reference.composite(refChained, [230, 60, 60, 200]);

  let maxDiff = 0;
  for (let i = 0; i < image.data.length; i++) {
    maxDiff = Math.max(maxDiff, Math.abs(image.data[i] - reference.data[i]));
  }
  assert.ok(maxDiff <= 21, `max channel diff ${maxDiff} exceeds tolerance`);
});
