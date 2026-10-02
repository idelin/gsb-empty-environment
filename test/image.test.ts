import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  pack,
  createImage,
  getPixel,
  setPixel,
  renderPages,
  uvCorners,
  verifyRenderedPages,
  rotatedOffset,
} from '../src/index.ts';
import type { ItemSpec, RgbaImage } from '../src/index.ts';

/** Self-built RGBA texture: every pixel encodes its own (x, y) for tamper-proof checks. */
function makeTexture(w: number, h: number, seed: number): RgbaImage {
  const img = createImage(w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      setPixel(img, x, y, [(x * 37 + seed) % 256, (y * 91 + seed) % 256, (x + y * 16 + seed) % 256, 255]);
    }
  }
  return img;
}

test('renders pages from RGBA sources and verifies every pixel', () => {
  const items: ItemSpec[] = [
    { id: 'a', w: 3, h: 5, margin: 1 },
    { id: 'b', w: 6, h: 2 },
    { id: 'c', w: 2, h: 7, rotatable: false },
    { id: 'd', w: 4, h: 3 },
  ];
  const { layout } = pack(items, { pageW: 16, pageH: 16 });
  const sources = new Map(items.map((it, i) => [it.id, makeTexture(it.w, it.h, i * 13)]));
  const pages = renderPages(layout, sources);
  assert.equal(pages.length, layout.pages);
  const errors = verifyRenderedPages(layout, sources, pages);
  assert.deepEqual(errors, []);
});

test('rotation maps source pixels to the correct page texels', () => {
  // Force rotation: 5x3 into a 4-wide page.
  const items: ItemSpec[] = [{ id: 'r', w: 5, h: 3 }];
  const { layout } = pack(items, { pageW: 4, pageH: 8 });
  const p = layout.placements[0]!;
  assert.equal(p.rotated, true);

  const src = makeTexture(5, 3, 7);
  const pages = renderPages(layout, new Map([['r', src]]));
  const page = pages[0]!;

  // 90-degree CW: source (sx, sy) -> page (x + (h-1-sy), y + sx).
  for (let sy = 0; sy < 3; sy++) {
    for (let sx = 0; sx < 5; sx++) {
      const { dx, dy } = rotatedOffset(sx, sy, 5, 3, true);
      assert.deepEqual(getPixel(page, p.x + dx, p.y + dy), getPixel(src, sx, sy));
    }
  }
});

test('UV corners of a rotated texture map source corners correctly', () => {
  const items: ItemSpec[] = [{ id: 'r', w: 5, h: 3 }];
  const { layout } = pack(items, { pageW: 4, pageH: 8 });
  const p = layout.placements[0]!;
  assert.equal(p.rotated, true);
  const [tl, tr, br, bl] = uvCorners(p, 4, 8);
  // Stored rect is 3 wide x 5 tall at (0,0). Source TL lands at stored TR.
  assert.deepEqual(tl, { u: 3 / 4, v: 0 / 8 });
  assert.deepEqual(tr, { u: 3 / 4, v: 5 / 8 });
  assert.deepEqual(br, { u: 0 / 4, v: 5 / 8 });
  assert.deepEqual(bl, { u: 0 / 4, v: 0 / 8 });

  // Sampling the atlas at those UVs (stepping half a texel inward) must yield
  // the corresponding source corner texels.
  const src = makeTexture(5, 3, 7);
  const pages = renderPages(layout, new Map([['r', src]]));
  const page = pages[0]!;
  const sample = (u: number, v: number, inwardX: number, inwardY: number) =>
    getPixel(page, Math.floor(u * 4 + inwardX), Math.floor(v * 8 + inwardY));
  assert.deepEqual(sample(tl.u, tl.v, -0.5, 0.5), getPixel(src, 0, 0));
  assert.deepEqual(sample(tr.u, tr.v, -0.5, -0.5), getPixel(src, 4, 0));
  assert.deepEqual(sample(br.u, br.v, 0.5, -0.5), getPixel(src, 4, 2));
  assert.deepEqual(sample(bl.u, bl.v, 0.5, 0.5), getPixel(src, 0, 2));
});

test('unrotated UVs are the plain stored rect corners', () => {
  const items: ItemSpec[] = [{ id: 'a', w: 4, h: 2 }];
  const { layout } = pack(items, { pageW: 8, pageH: 8 });
  const p = layout.placements[0]!;
  assert.equal(p.rotated, false);
  const [tl, tr, br, bl] = uvCorners(p, 8, 8);
  assert.deepEqual(tl, { u: p.x / 8, v: p.y / 8 });
  assert.deepEqual(tr, { u: (p.x + 4) / 8, v: p.y / 8 });
  assert.deepEqual(br, { u: (p.x + 4) / 8, v: (p.y + 2) / 8 });
  assert.deepEqual(bl, { u: p.x / 8, v: (p.y + 2) / 8 });
});

test('image verifier detects a single tampered pixel', () => {
  const items: ItemSpec[] = [{ id: 'a', w: 3, h: 3 }];
  const { layout } = pack(items, { pageW: 8, pageH: 8 });
  const src = makeTexture(3, 3, 1);
  const sources = new Map([['a', src]]);
  const pages = renderPages(layout, sources);
  const p = layout.placements[0]!;
  const before = getPixel(pages[0]!, p.x + 1, p.y + 1);
  setPixel(pages[0]!, p.x + 1, p.y + 1, [before[0] ^ 0xff, before[1], before[2], 255]);
  const errors = verifyRenderedPages(layout, sources, pages);
  assert.ok(errors.some((e) => e.includes('pixel mismatch')));
});

test('blit of an out-of-page placement throws instead of overflowing', () => {
  const items: ItemSpec[] = [{ id: 'a', w: 4, h: 4 }];
  const { layout } = pack(items, { pageW: 8, pageH: 8 });
  layout.placements[0] = { ...layout.placements[0]!, x: 7, y: 7 };
  const sources = new Map([['a', makeTexture(4, 4, 0)]]);
  assert.throws(() => renderPages(layout, sources), /outside page/);
});
