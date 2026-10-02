/**
 * End-to-end sample: pack textures into fixed pages, render RGBA pages from
 * self-built textures, verify pixels and UVs, then apply an atomic update.
 * Run with: npm run sample
 */
import {
  pack,
  applyChanges,
  verifyLayout,
  createImage,
  setPixel,
  renderPages,
  verifyRenderedPages,
  uvCorners,
} from '../src/index.ts';
import type { ItemSpec, RgbaImage, UpdateOk } from '../src/index.ts';

function makeTexture(w: number, h: number, seed: number): RgbaImage {
  const img = createImage(w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      setPixel(img, x, y, [(x * 37 + seed) % 256, (y * 91 + seed) % 256, (x * y + seed) % 256, 255]);
    }
  }
  return img;
}

const PAGE = { pageW: 64, pageH: 64 };

const items: ItemSpec[] = [
  { id: 'hero', w: 24, h: 16, margin: 2 }, // uniform 2px gap edge
  { id: 'banner', w: 40, h: 8, margin: { top: 1, bottom: 1 } }, // per-side margins
  { id: 'pin', w: 8, h: 8, fixed: { page: 0, x: 0, y: 0 } }, // immovable
  { id: 'portrait', w: 10, h: 20, rotatable: false }, // rotation forbidden
  { id: 'tile-a', w: 12, h: 12, margin: 1 },
  { id: 'tile-b', w: 12, h: 12, margin: 1 },
  { id: 'icon', w: 6, h: 6 },
  { id: 'too-big', w: 100, h: 100 }, // will fail explicitly
];

console.log('=== 1. Pack ===');
const { layout, bounds } = pack(items, PAGE);
console.log(`pages: ${layout.pages}, bounds: [${bounds.lower}, ${bounds.upper}] optimal=${bounds.optimal} (${bounds.certificate})`);
for (const p of layout.placements) {
  console.log(
    `  ${p.id.padEnd(10)} page=${p.page} pos=(${p.x},${p.y}) size=${p.w}x${p.h} rotated=${p.rotated}`,
  );
}
for (const u of layout.unpacked) {
  console.log(`  UNPACKED ${u.id}: ${u.reason} - ${u.detail}`);
}
const placed = items.filter((it) => !layout.unpacked.some((u) => u.id === it.id));
const check = verifyLayout(layout, items);
console.log(`pixel-occupancy verification: ${check.ok ? 'OK' : 'FAILED: ' + check.errors.join('; ')}`);

console.log('\n=== 2. Render RGBA pages and verify pixels + UVs ===');
const sources = new Map(placed.map((it, i) => [it.id, makeTexture(it.w, it.h, i * 29 + 1)]));
const pages = renderPages(layout, sources);
const imgErrors = verifyRenderedPages(layout, sources, pages);
console.log(`rendered ${pages.length} page(s); image verification: ${imgErrors.length === 0 ? 'OK' : imgErrors.join('; ')}`);
const fmtUv = (id: string) => {
  const p = layout.placements.find((pl) => pl.id === id)!;
  const cornerNames = ['TL', 'TR', 'BR', 'BL'];
  const uvs = uvCorners(p, PAGE.pageW, PAGE.pageH)
    .map((c, i) => `${cornerNames[i]}=(${c.u.toFixed(3)},${c.v.toFixed(3)})`)
    .join(' ');
  console.log(`  ${id} rotated=${p.rotated} UVs: ${uvs}`);
};
fmtUv('hero');
fmtUv('banner'); // rotated 90 degrees: source TL maps to stored top-right

console.log('\n=== 3. Atomic update: add / remove / resize ===');
const placedItems = items.filter((it) => it.id !== 'too-big');
const res = applyChanges(layout, placedItems, {
  add: [{ id: 'new-icon', w: 8, h: 8, margin: 1 }],
  remove: ['tile-b'],
  resize: [{ id: 'icon', w: 10, h: 10 }],
});
if (!res.ok) {
  console.log(`update failed atomically (nothing published): ${res.error}`);
} else {
  const ok = res as UpdateOk;
  const moved = ok.plan.filter((op) => op.type === 'move').length;
  console.log(`plan: ${ok.plan.length} ops, ${moved} moved, pages=${ok.layout.pages}, bounds=[${ok.bounds.lower}, ${ok.bounds.upper}] optimal=${ok.bounds.optimal} (${ok.bounds.certificate})`);
  for (const op of ok.plan) {
    if (op.type === 'keep') continue;
    console.log(`  ${JSON.stringify(op)}`);
  }
  const nextItems: ItemSpec[] = [
    ...placedItems.filter((it) => it.id !== 'tile-b').map((it) => (it.id === 'icon' ? { ...it, w: 10, h: 10 } : it)),
    { id: 'new-icon', w: 8, h: 8, margin: 1 },
  ];
  console.log(`post-update verification: ${verifyLayout(ok.layout, nextItems).ok ? 'OK' : 'FAILED'}`);
}

console.log('\n=== 4. Atomic failure: oversized addition publishes nothing ===');
const fail = applyChanges(layout, placedItems, { add: [{ id: 'giant', w: 500, h: 500 }] });
console.log(`ok=${fail.ok}${fail.ok ? '' : `, error="${fail.error}"`}`);

console.log('\n=== 5. Large instance: bounds with heuristic label ===');
const many: ItemSpec[] = [];
// Wide, short textures: only one column fits per page, so the area bound is
// loose and the exact proof is disabled above the threshold.
for (let i = 0; i < 60; i++) many.push({ id: `m${i}`, w: 33 + (i % 3), h: 5, margin: 1 });
const big = pack(many, { pageW: 64, pageH: 64 });
console.log(`pages=${big.layout.pages}, bounds=[${big.bounds.lower}, ${big.bounds.upper}] optimal=${big.bounds.optimal} (${big.bounds.certificate})`);
console.log(`verification: ${verifyLayout(big.layout, many).ok ? 'OK' : 'FAILED'}`);
