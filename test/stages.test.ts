import test from 'node:test';
import assert from 'node:assert/strict';
import {
  FloatImage,
  Pipeline,
  RGBAImage,
  Stage,
  multiply,
  rotation,
  scaling,
  translation,
} from '../src/index.ts';
import { approx, makeFloat } from './helpers.ts';

function renderOne(stage: Stage, src: FloatImage): FloatImage {
  const dst = new FloatImage(stage.dstW, stage.dstH);
  stage.render(src, dst, { x: 0, y: 0, w: stage.dstW, h: stage.dstH });
  return dst;
}

test('crop extracts the exact pixel window', () => {
  const src = makeFloat(4, 4, (x, y) => [x + y * 4, 0, 0, 1]);
  const dst = renderOne(Stage.crop(4, 4, 1, 1, 2, 2), src);
  assert.equal(dst.get(0, 0)[0], 5);
  assert.equal(dst.get(1, 0)[0], 6);
  assert.equal(dst.get(0, 1)[0], 9);
  assert.equal(dst.get(1, 1)[0], 10);
});

test('crop rejects non-integer origins', () => {
  assert.throws(() => Stage.crop(4, 4, 0.5, 0, 2, 2), /integer/);
});

test('copy filter fills out-of-bounds samples with transparent black', () => {
  const src = makeFloat(2, 2, (x, y) => [1, 0, 0, 1]);
  const dst = renderOne(Stage.crop(2, 2, -1, -1, 3, 3), src);
  assert.deepEqual(dst.get(0, 0), [0, 0, 0, 0]);
  assert.deepEqual(dst.get(1, 1), [1, 0, 0, 1]);
  assert.deepEqual(dst.get(0, 1), [0, 0, 0, 0]);
});

test('bilinear upscale samples at pixel centers with transparent border', () => {
  const src = makeFloat(2, 2, (x, y) => [x + 2 * y, 0, 0, 1]);
  const dst = renderOne(Stage.scale(2, 2, 2, 2), src);
  approx(dst.get(0, 0)[0], 0, 1e-6, 'corner r');
  approx(dst.get(0, 0)[3], 0.5625, 1e-6, 'corner alpha');
  approx(dst.get(1, 1)[0], 0.75, 1e-6, 'interior r');
  approx(dst.get(1, 1)[3], 1, 1e-6, 'interior alpha');
  approx(dst.get(3, 3)[0], 1.6875, 1e-6, 'far corner r');
  approx(dst.get(3, 3)[3], 0.5625, 1e-6, 'far corner alpha');
});

test('area downscale 2x2 to 1x1 is the exact average', () => {
  const src = makeFloat(2, 2, (x, y) => [x + 2 * y, 0, 0, 1]);
  const dst = renderOne(Stage.scale(2, 2, 0.5, 0.5), src);
  approx(dst.get(0, 0)[0], 1.5, 1e-6, 'r');
  approx(dst.get(0, 0)[3], 1, 1e-6, 'alpha');
});

test('area downscale with non-integer ratio 7 to 3 uses exact area weights', () => {
  const src = makeFloat(7, 1, (x) => [x, 0, 0, 1]);
  const dst = renderOne(Stage.scale(7, 1, 3 / 7, 1), src);
  approx(dst.get(0, 0)[0], 5 / 7, 1e-5, 'dst0');
  approx(dst.get(1, 0)[0], 3, 1e-5, 'dst1');
  approx(dst.get(2, 0)[0], 37 / 7, 1e-5, 'dst2');
});

test('1x1 upscale to 3x3 spreads by bilinear weights', () => {
  const src = makeFloat(1, 1, () => [1, 0, 0, 1]);
  const dst = renderOne(Stage.scale(1, 1, 3, 3), src);
  approx(dst.get(1, 1)[0], 1, 1e-6, 'center');
  approx(dst.get(0, 0)[0], 4 / 9, 1e-6, 'corner');
  approx(dst.get(1, 0)[0], 2 / 3, 1e-6, 'edge');
});

test('5x5 downscale to 1x1 averages all pixels', () => {
  const src = makeFloat(5, 5, (x, y) => [x + 5 * y, 0, 0, 1]);
  const dst = renderOne(Stage.scale(5, 5, 0.2, 0.2), src);
  approx(dst.get(0, 0)[0], 12, 1e-5, 'mean');
});

test('1x1 to 1x1 keeps the pixel', () => {
  const src = makeFloat(1, 1, () => [0.25, 0.5, 0.75, 1]);
  const dst = renderOne(Stage.scale(1, 1, 1, 1), src);
  approx(dst.get(0, 0)[0], 0.25, 1e-6, 'r');
  approx(dst.get(0, 0)[2], 0.75, 1e-6, 'b');
});

test('transparent red next to opaque blue downscales without red fringe', () => {
  const img = RGBAImage.fromRows([
    [
      [255, 0, 0, 0],
      [0, 0, 255, 255],
    ],
  ]);
  const out = new Pipeline([Stage.scale(2, 1, 0.5, 1)]).render(img).toRGBA();
  const [r, g, b, a] = out.get(0, 0);
  assert.equal(r, 0, 'no red leaks from fully transparent texel');
  assert.equal(g, 0);
  assert.equal(b, 255);
  assert.ok(a === 127 || a === 128, 'alpha is the average: ' + a);
});

test('black and white average to 188 in linear space, not 128', () => {
  const img = RGBAImage.fromRows([
    [
      [0, 0, 0, 255],
      [255, 255, 255, 255],
    ],
  ]);
  const out = new Pipeline([Stage.scale(2, 1, 0.5, 1)]).render(img).toRGBA();
  const [r, g, b, a] = out.get(0, 0);
  assert.equal(r, 188);
  assert.equal(g, 188);
  assert.equal(b, 188);
  assert.equal(a, 255);
});

test('rotate then downscale conserves covered area', () => {
  const src = makeFloat(16, 16, () => [0, 0, 1, 1]);
  const fwd = multiply(
    translation(8, 8),
    multiply(scaling(0.5, 0.5), multiply(rotation(Math.PI / 6), translation(-8, -8))),
  );
  const dst = renderOne(Stage.affine(16, 16, 16, 16, fwd, 'area'), src);
  let mass = 0;
  for (let i = 0; i < dst.data.length; i += 4) mass += dst.data[i + 3];
  approx(mass * 4, 256, 0.75, 'total alpha mass');
  approx(dst.get(8, 8)[3], 1, 1e-4, 'center fully covered');
});

test('rotate then downscale conserves the mass of a single pixel dot', () => {
  const src = makeFloat(16, 16, (x, y) => (x === 4 && y === 4 ? [1, 1, 1, 1] : [0, 0, 0, 0]));
  const fwd = multiply(
    translation(8, 8),
    multiply(scaling(0.5, 0.5), multiply(rotation(Math.PI / 6), translation(-8, -8))),
  );
  const dst = renderOne(Stage.affine(16, 16, 16, 16, fwd, 'area'), src);
  let mass = 0;
  for (let i = 0; i < dst.data.length; i += 4) mass += dst.data[i + 3];
  approx(mass * 4, 1, 1e-3, 'dot mass');
});

test('dirty rect of a copy stage is the translated rect', () => {
  const s = Stage.translate(8, 8, 2, 3);
  assert.deepEqual(s.dirtyRect({ x: 1, y: 1, w: 2, h: 2 }), { x: 3, y: 4, w: 2, h: 2 });
});

test('dirty rect clamps to output bounds', () => {
  const s = Stage.translate(8, 8, 7, 0);
  assert.deepEqual(s.dirtyRect({ x: 0, y: 0, w: 4, h: 4 }), { x: 7, y: 0, w: 1, h: 4 });
});

test('dirty rect of a downscale covers exactly the overlapping output pixels', () => {
  const s = Stage.scale(8, 8, 0.5, 0.5);
  assert.deepEqual(s.dirtyRect({ x: 2, y: 2, w: 2, h: 2 }), { x: 1, y: 1, w: 1, h: 1 });
  assert.deepEqual(s.dirtyRect({ x: 3, y: 3, w: 1, h: 1 }), { x: 1, y: 1, w: 1, h: 1 });
  assert.deepEqual(s.dirtyRect({ x: 0, y: 0, w: 1, h: 1 }), { x: 0, y: 0, w: 1, h: 1 });
});
