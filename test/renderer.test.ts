import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Layer, Scene } from '../src/layer.ts';
import { render } from '../src/renderer.ts';

function px(buf: Uint8ClampedArray, width: number, x: number, y: number): number[] {
  const i = (y * width + x) * 4;
  return [buf[i], buf[i + 1], buf[i + 2], buf[i + 3]];
}

function assertPixel(actual: number[], expected: number[], tolerance = 1): void {
  for (let c = 0; c < 4; c++) {
    assert.ok(
      Math.abs(actual[c] - expected[c]) <= tolerance,
      `channel ${c}: expected ${expected[c]}, got ${actual[c]}`,
    );
  }
}

test('normal blend: opaque source over opaque dest', () => {
  const scene = new Scene(2, 1);
  scene.add(Layer.solid(2, 1, [50, 100, 200, 255], 'bg'));
  const top = Layer.solid(1, 1, [200, 100, 50, 255], 'top');
  top.transform.tx = 1;
  scene.add(top);
  const buf = render(scene);
  assertPixel(px(buf, 2, 0, 0), [50, 100, 200, 255], 0);
  assertPixel(px(buf, 2, 1, 0), [200, 100, 50, 255], 0);
});

test('normal blend: semi-transparent source over opaque dest', () => {
  const scene = new Scene(1, 1);
  scene.add(Layer.solid(1, 1, [50, 100, 200, 255], 'bg'));
  scene.add(Layer.solid(1, 1, [200, 100, 50, 128], 'top'));
  const buf = render(scene);
  // sa = 128/255; out = src*sa + dst*(1-sa)
  assertPixel(px(buf, 1, 0, 0), [125, 100, 125, 255]);
});

test('multiply blend', () => {
  const scene = new Scene(1, 1);
  scene.add(Layer.solid(1, 1, [128, 128, 128, 255], 'bg'));
  const top = Layer.solid(1, 1, [128, 255, 0, 255], 'mul');
  top.blendMode = 'multiply';
  scene.add(top);
  const buf = render(scene);
  assertPixel(px(buf, 1, 0, 0), [64, 128, 0, 255]);
});

test('additive blend clamps at 255', () => {
  const scene = new Scene(1, 1);
  scene.add(Layer.solid(1, 1, [100, 200, 255, 255], 'bg'));
  const top = Layer.solid(1, 1, [200, 50, 0, 255], 'add');
  top.blendMode = 'additive';
  scene.add(top);
  const buf = render(scene);
  assertPixel(px(buf, 1, 0, 0), [255, 250, 255, 255]);
});

test('layer opacity scales source alpha', () => {
  const scene = new Scene(1, 1);
  scene.add(Layer.solid(1, 1, [200, 100, 150, 255], 'bg'));
  const top = Layer.solid(1, 1, [100, 200, 50, 255], 'half');
  top.opacity = 0.5;
  scene.add(top);
  const buf = render(scene);
  assertPixel(px(buf, 1, 0, 0), [150, 150, 100, 255]);
});

test('scale + translate transform (nearest sampling)', () => {
  const scene = new Scene(4, 4);
  const dot = Layer.solid(1, 1, [255, 0, 0, 255], 'dot');
  dot.transform = { tx: 2, ty: 1, sx: 2, sy: 2 };
  scene.add(dot);
  const buf = render(scene);
  for (const [x, y] of [[2, 1], [3, 1], [2, 2], [3, 2]]) {
    assertPixel(px(buf, 4, x, y), [255, 0, 0, 255], 0);
  }
  assertPixel(px(buf, 4, 0, 0), [0, 0, 0, 0], 0);
  assertPixel(px(buf, 4, 1, 1), [0, 0, 0, 0], 0);
});

test('clip restricts drawn pixels', () => {
  const scene = new Scene(4, 4);
  const layer = Layer.solid(4, 4, [255, 255, 255, 255], 'clipped');
  layer.clip = { x: 1, y: 1, w: 2, h: 2 };
  scene.add(layer);
  const buf = render(scene);
  for (const [x, y] of [[1, 1], [2, 1], [1, 2], [2, 2]]) {
    assertPixel(px(buf, 4, x, y), [255, 255, 255, 255], 0);
  }
  assertPixel(px(buf, 4, 0, 0), [0, 0, 0, 0], 0);
  assertPixel(px(buf, 4, 3, 3), [0, 0, 0, 0], 0);
});

test('shadow covers pixels within declared radius only', () => {
  const scene = new Scene(10, 10);
  const layer = Layer.solid(2, 2, [255, 255, 255, 255], 'card');
  layer.transform = { tx: 4, ty: 4, sx: 1, sy: 1 };
  layer.shadow = { radius: 2, offsetX: 1, offsetY: 1, color: [0, 0, 0, 255] };
  scene.add(layer);
  const buf = render(scene);
  // (3,3): chebyshev distance 2 from content corner (4,4) -> covered
  assertPixel(px(buf, 10, 3, 3), [0, 0, 0, 255], 0);
  // (6,6): within shadow extent diagonally -> covered
  assertPixel(px(buf, 10, 6, 6), [0, 0, 0, 255], 0);
  // (1,1): distance 4 > radius 2 -> untouched
  assertPixel(px(buf, 10, 1, 1), [0, 0, 0, 0], 0);
  // content drawn on top of its own shadow
  assertPixel(px(buf, 10, 4, 4), [255, 255, 255, 255], 0);
});

test('children inherit parent transform and opacity stacks via compositing', () => {
  const scene = new Scene(4, 4);
  scene.add(Layer.solid(4, 4, [0, 0, 0, 255], 'bg'));
  const group = new Layer(0, 0, 'group');
  group.transform = { tx: 2, ty: 2, sx: 1, sy: 1 };
  const child = Layer.solid(1, 1, [255, 255, 255, 255], 'child');
  group.add(child);
  scene.add(group);
  const buf = render(scene);
  assertPixel(px(buf, 4, 2, 2), [255, 255, 255, 255], 0);
  assertPixel(px(buf, 4, 1, 1), [0, 0, 0, 255], 0);
});
