import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Rect } from '../src/index.ts';
import {
  Bitmap, BlendMode, Layer, DirtyTracker, render, translation, rect,
} from '../src/index.ts';

const CANVAS = 8;

function makeScene() {
  const root = new Layer('root');
  const bg = root.addChild(new Layer('bg', { bitmap: solid(CANVAS, CANVAS, 30, 30, 30, 255) }));
  const sprite = root.addChild(new Layer('sprite', {
    bitmap: solid(2, 2, 255, 0, 0, 255),
    transform: translation(2, 2),
  }));
  return { root, bg, sprite };
}

function solid(w: number, h: number, r: number, g: number, b: number, a: number): Bitmap {
  const bmp = new Bitmap(w, h);
  bmp.fill(r, g, b, a);
  return bmp;
}

/** Assert every pixel outside `region` is identical in a and b. */
function assertSameOutside(a: Bitmap, b: Bitmap, region: Rect | null): void {
  for (let y = 0; y < a.height; y++) {
    for (let x = 0; x < a.width; x++) {
      const inside = region !== null &&
        x >= region.x && y >= region.y &&
        x < region.x + region.width && y < region.y + region.height;
      if (!inside) {
        assert.deepEqual(a.getPixel(x, y), b.getPixel(x, y),
          `pixel (${x},${y}) outside dirty region must be untouched`);
      }
    }
  }
}

test('full render places sprite at translated position', () => {
  const { root } = makeScene();
  const out = new Bitmap(CANVAS, CANVAS);
  render(root, out);
  assert.deepEqual(out.getPixel(2, 2), [255, 0, 0, 255]);
  assert.deepEqual(out.getPixel(3, 3), [255, 0, 0, 255]);
  assert.deepEqual(out.getPixel(1, 1), [30, 30, 30, 255]);
  assert.deepEqual(out.getPixel(4, 4), [30, 30, 30, 255]);
});

test('incremental repaint after local edit equals full re-render', () => {
  const { root, sprite } = makeScene();
  const tracker = new DirtyTracker(root);

  const before = new Bitmap(CANVAS, CANVAS);
  render(root, before);

  // local edit: repaint one pixel of the sprite green
  sprite.bitmap!.setPixel(0, 0, 0, 255, 0, 255);
  tracker.markLocalEdit(sprite, rect(0, 0, 1, 1));
  const dirty = tracker.dirtyRegion()!;
  assert.deepEqual(dirty, { x: 2, y: 2, width: 1, height: 1 });

  const incremental = before.clone();
  render(root, incremental, dirty);

  const full = new Bitmap(CANVAS, CANVAS);
  render(root, full);

  assert.ok(incremental.equals(full), 'repainting only the dirty region matches full render');
  assertSameOutside(before, full, dirty);
});

test('incremental repaint after move covers old and new positions', () => {
  const { root, sprite } = makeScene();
  const tracker = new DirtyTracker(root);

  const before = new Bitmap(CANVAS, CANVAS);
  render(root, before);

  tracker.setTransform(sprite, translation(5, 5));
  const dirty = tracker.dirtyRegion()!;

  const incremental = before.clone();
  render(root, incremental, dirty);

  const full = new Bitmap(CANVAS, CANVAS);
  render(root, full);

  assert.ok(incremental.equals(full));
  assert.deepEqual(full.getPixel(2, 2), [30, 30, 30, 255], 'old position restored to background');
  assert.deepEqual(full.getPixel(5, 5), [255, 0, 0, 255], 'new position painted');
  assertSameOutside(before, full, dirty);
});

test('incremental repaint after opacity change equals full re-render', () => {
  const { root, sprite } = makeScene();
  const tracker = new DirtyTracker(root);

  const before = new Bitmap(CANVAS, CANVAS);
  render(root, before);

  tracker.setOpacity(sprite, 0.5);
  const dirty = tracker.dirtyRegion()!;

  const incremental = before.clone();
  render(root, incremental, dirty);

  const full = new Bitmap(CANVAS, CANVAS);
  render(root, full);

  assert.ok(incremental.equals(full));
  // 50% red over rgb(30,30,30): r = 255*.5+30*.5 = 142.5 -> 143 (round), g/b = 15
  assert.deepEqual(full.getPixel(2, 2), [143, 15, 15, 255]);
  assertSameOutside(before, full, dirty);
});

test('shadow renders within declared radius and is covered by dirty region', () => {
  const root = new Layer('root');
  root.addChild(new Layer('bg', { bitmap: solid(CANVAS, CANVAS, 0, 0, 0, 0) }));
  const sprite = root.addChild(new Layer('sprite', {
    bitmap: solid(2, 2, 255, 255, 255, 255),
    transform: translation(3, 3),
    effects: [{ kind: 'shadow', radius: 2, offsetX: 0, offsetY: 0, color: [0, 0, 0, 200] }],
  }));
  const tracker = new DirtyTracker(root);

  const before = new Bitmap(CANVAS, CANVAS);
  render(root, before);

  // shadow pixels exist around the sprite (e.g. one pixel left of it)
  assert.ok(before.getPixel(2, 3)[3] > 0, 'shadow visible beside sprite');

  sprite.bitmap!.setPixel(0, 0, 0, 0, 255, 255);
  tracker.markLocalEdit(sprite, rect(0, 0, 1, 1));
  const dirty = tracker.dirtyRegion()!;

  const incremental = before.clone();
  render(root, incremental, dirty);
  const full = new Bitmap(CANVAS, CANVAS);
  render(root, full);

  assert.ok(incremental.equals(full), 'shadow region repainted consistently');
  assertSameOutside(before, full, dirty);
});

test('clip change repaints exactly old+new visible area', () => {
  const root = new Layer('root');
  root.addChild(new Layer('bg', { bitmap: solid(CANVAS, CANVAS, 30, 30, 30, 255) }));
  const sprite = root.addChild(new Layer('sprite', {
    bitmap: solid(6, 6, 255, 0, 0, 255),
    transform: translation(1, 1),
    clip: rect(0, 0, 2, 2),
  }));
  const tracker = new DirtyTracker(root);

  const before = new Bitmap(CANVAS, CANVAS);
  render(root, before);
  assert.deepEqual(before.getPixel(1, 1), [255, 0, 0, 255], 'inside clip visible');
  assert.deepEqual(before.getPixel(4, 4), [30, 30, 30, 255], 'outside clip hidden');

  tracker.setClip(sprite, rect(3, 3, 2, 2));
  const dirty = tracker.dirtyRegion()!;

  const incremental = before.clone();
  render(root, incremental, dirty);
  const full = new Bitmap(CANVAS, CANVAS);
  render(root, full);

  assert.ok(incremental.equals(full));
  assert.deepEqual(full.getPixel(1, 1), [30, 30, 30, 255], 'old clip area cleared');
  assert.deepEqual(full.getPixel(4, 4), [255, 0, 0, 255], 'new clip area visible');
  assertSameOutside(before, full, dirty);
});

test('blend modes: source-over / multiply / additive math', () => {
  const mk = (mode: BlendMode, sr: number, sg: number, sb: number, sa: number) => {
    const root = new Layer('root');
    root.addChild(new Layer('bg', { bitmap: solid(1, 1, 100, 150, 200, 255) }));
    root.addChild(new Layer('fg', { bitmap: solid(1, 1, sr, sg, sb, sa), blendMode: mode }));
    const out = new Bitmap(1, 1);
    render(root, out);
    return out.getPixel(0, 0);
  };

  // source-over, half-alpha red over (100,150,200)
  const so = mk(BlendMode.SourceOver, 255, 0, 0, 128);
  const sa = 128 / 255;
  assert.deepEqual(so, [
    Math.round(255 * sa + 100 * (1 - sa)),
    Math.round(150 * (1 - sa)),
    Math.round(200 * (1 - sa)),
    255,
  ]);

  // multiply: (200,100,50) x (100,150,200)
  assert.deepEqual(mk(BlendMode.Multiply, 200, 100, 50, 255), [
    Math.round((200 * 100) / 255),
    Math.round((100 * 150) / 255),
    Math.round((50 * 200) / 255),
    255,
  ]);

  // additive: (100,200,255) + (100,150,200), clamped
  assert.deepEqual(mk(BlendMode.Additive, 100, 200, 255, 255), [200, 255, 255, 255]);
});

test('parent opacity multiplies into child during compositing', () => {
  const root = new Layer('root');
  root.addChild(new Layer('bg', { bitmap: solid(1, 1, 0, 0, 0, 255) }));
  const group = root.addChild(new Layer('group', { opacity: 0.5 }));
  group.addChild(new Layer('child', { bitmap: solid(1, 1, 255, 255, 255, 255) }));
  const out = new Bitmap(1, 1);
  render(root, out);
  assert.deepEqual(out.getPixel(0, 0), [128, 128, 128, 255]);
});
