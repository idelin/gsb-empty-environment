import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rectsEqual, type Rect } from '../src/geometry.ts';
import { Layer, Scene } from '../src/layer.ts';
import { Compositor } from '../src/compositor.ts';
import { render } from '../src/renderer.ts';

function assertRect(actual: Rect, expected: Rect): void {
  assert.ok(
    rectsEqual(actual, expected),
    `expected rect ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`,
  );
}

test('moving a layer dirties the union of old and new positions', () => {
  const scene = new Scene(32, 24);
  const layer = Layer.solid(4, 4, [255, 0, 0, 255], 'sprite');
  layer.transform = { tx: 2, ty: 2, sx: 1, sy: 1 };
  scene.add(layer);
  const compositor = new Compositor(scene);

  const dirty = compositor.setTransform(layer, { tx: 10, ty: 2, sx: 1, sy: 1 });
  assertRect(dirty.rect, { x: 2, y: 2, w: 12, h: 4 });
  assert.ok(dirty.reasons.some((r) => r.startsWith('moved(sprite)')));
});

test('edit fully outside the clip produces no repaint', () => {
  const scene = new Scene(32, 24);
  const layer = Layer.solid(8, 8, [0, 255, 0, 255], 'clipped');
  layer.clip = { x: 0, y: 0, w: 4, h: 4 };
  scene.add(layer);
  const compositor = new Compositor(scene);

  const dirty = compositor.editContent(layer, { x: 5, y: 5, w: 2, h: 2 });
  assertRect(dirty.rect, { x: 5, y: 5, w: 0, h: 0 });
  assert.ok(dirty.reasons.some((r) => r.includes('no-repaint')));
  // nothing accumulated either
  assert.ok(compositor.takeDirty().rect.w === 0);
});

test('edit partially inside the clip is clipped to the visible part', () => {
  const scene = new Scene(32, 24);
  const layer = Layer.solid(8, 8, [0, 255, 0, 255], 'clipped');
  layer.clip = { x: 0, y: 0, w: 4, h: 4 };
  scene.add(layer);
  const compositor = new Compositor(scene);

  const dirty = compositor.editContent(layer, { x: 3, y: 3, w: 4, h: 4 });
  assertRect(dirty.rect, { x: 3, y: 3, w: 1, h: 1 });
});

test('shadow expands the dirty region by the declared radius', () => {
  const scene = new Scene(32, 24);
  const layer = Layer.solid(8, 8, [255, 255, 255, 255], 'card');
  layer.shadow = { radius: 3, offsetX: 1, offsetY: 0, color: [0, 0, 0, 128] };
  scene.add(layer);
  const compositor = new Compositor(scene);

  const dirty = compositor.editContent(layer, { x: 2, y: 2, w: 2, h: 2 });
  // content (2,2,2,2) U shadow extent inflate(offset((2,2,2,2),(1,0)),3) = (0,-1,8,8),
  // clamped to the canvas -> (0,0,8,7)
  assertRect(dirty.rect, { x: 0, y: 0, w: 8, h: 7 });
  assert.ok(dirty.reasons.some((r) => r.startsWith('content-changed(card)')));
  assert.ok(dirty.reasons.some((r) => r.startsWith('shadow-expanded(radius=3)')));
});

test('opacity change propagates to the whole subtree', () => {
  const scene = new Scene(32, 24);
  const group = new Layer(0, 0, 'group');
  group.transform = { tx: 4, ty: 4, sx: 1, sy: 1 };
  const childA = Layer.solid(2, 2, [255, 0, 0, 255], 'a');
  childA.transform = { tx: 1, ty: 1, sx: 1, sy: 1 };
  const childB = Layer.solid(3, 3, [0, 0, 255, 255], 'b');
  childB.transform = { tx: 8, ty: 6, sx: 1, sy: 1 };
  group.add(childA);
  group.add(childB);
  scene.add(group);
  const compositor = new Compositor(scene);

  const dirty = compositor.setOpacity(group, 0.5);
  // union of child footprints: (5,5,2,2) U (12,10,3,3) = (5,5,10,8)
  assertRect(dirty.rect, { x: 5, y: 5, w: 10, h: 8 });
  assert.ok(dirty.reasons.includes('opacity-changed(group)'));
  assert.ok(dirty.reasons.includes('ancestor-opacity-changed(a)'));
  assert.ok(dirty.reasons.includes('ancestor-opacity-changed(b)'));
});

test('parent transform change propagates to children (old + new bounds)', () => {
  const scene = new Scene(32, 24);
  const group = new Layer(0, 0, 'group');
  const child = Layer.solid(2, 2, [255, 0, 0, 255], 'child');
  child.transform = { tx: 1, ty: 1, sx: 1, sy: 1 };
  group.add(child);
  scene.add(group);
  const compositor = new Compositor(scene);

  const dirty = compositor.setTransform(group, { tx: 5, ty: 0, sx: 1, sy: 1 });
  // child old (1,1,2,2) U new (6,1,2,2) = (1,1,7,2)
  assertRect(dirty.rect, { x: 1, y: 1, w: 7, h: 2 });
  assert.ok(dirty.reasons.includes('moved(group):old+new-bounds'));
  assert.ok(dirty.reasons.includes('parent-transform-changed(child)'));
});

test('clip change dirties union of old and new clipped footprints', () => {
  const scene = new Scene(32, 24);
  const layer = Layer.solid(8, 8, [0, 255, 0, 255], 'panel');
  scene.add(layer);
  const compositor = new Compositor(scene);

  const dirty = compositor.setClip(layer, { x: 2, y: 2, w: 4, h: 4 });
  assertRect(dirty.rect, { x: 0, y: 0, w: 8, h: 8 });
  assert.ok(dirty.reasons.includes('clip-changed(panel)'));
});

test('takeDirty merges accumulated regions and clears them', () => {
  const scene = new Scene(32, 24);
  const layer = Layer.solid(4, 4, [255, 0, 0, 255], 'sprite');
  scene.add(layer);
  const compositor = new Compositor(scene);

  compositor.editContent(layer, { x: 0, y: 0, w: 1, h: 1 });
  compositor.editContent(layer, { x: 3, y: 3, w: 1, h: 1 });
  const dirty = compositor.takeDirty();
  assertRect(dirty.rect, { x: 0, y: 0, w: 4, h: 4 });
  assert.ok(compositor.takeDirty().rect.w === 0);
});

test('partial repaint after content edit matches a full re-render everywhere', () => {
  const scene = new Scene(16, 16);
  scene.add(Layer.solid(16, 16, [10, 20, 30, 255], 'bg'));
  const sprite = Layer.solid(4, 4, [200, 100, 50, 255], 'sprite');
  sprite.transform = { tx: 3, ty: 3, sx: 1, sy: 1 };
  sprite.shadow = { radius: 1, offsetX: 1, offsetY: 0, color: [0, 0, 0, 128] };
  scene.add(sprite);
  const compositor = new Compositor(scene);

  const before = render(scene);
  sprite.setPixel(1, 1, 0, 255, 0, 255);
  const dirty = compositor.editContent(sprite, { x: 1, y: 1, w: 1, h: 1 });

  const repainted = new Uint8ClampedArray(before);
  render(scene, repainted, dirty.rect);
  const full = render(scene);
  assert.deepStrictEqual([...repainted], [...full]);
});

test('partial repaint after a move matches a full re-render everywhere', () => {
  const scene = new Scene(16, 16);
  scene.add(Layer.solid(16, 16, [10, 20, 30, 255], 'bg'));
  const sprite = Layer.solid(3, 3, [250, 200, 100, 255], 'sprite');
  sprite.transform = { tx: 2, ty: 2, sx: 1, sy: 1 };
  sprite.shadow = { radius: 2, offsetX: 1, offsetY: 1, color: [0, 0, 0, 200] };
  scene.add(sprite);
  const compositor = new Compositor(scene);

  const before = render(scene);
  const dirty = compositor.setTransform(sprite, { tx: 9, ty: 7, sx: 1, sy: 1 });

  const repainted = new Uint8ClampedArray(before);
  render(scene, repainted, dirty.rect);
  const full = render(scene);
  assert.deepStrictEqual([...repainted], [...full]);
});
