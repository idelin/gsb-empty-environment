import {
  pack,
  planUpdate,
  verifyAll,
  uvToPage,
  RGBAImage,
  colorForId,
  type LayoutSnapshot,
} from '../src/index.ts';

const pageWidth = 32;
const pageHeight = 32;
const spacing = 2;

const items = [
  { id: 'hero', width: 12, height: 8, margin: 1 },
  { id: 'tree', width: 6, height: 10, rotatable: false },
  { id: 'rock', width: 5, height: 5 },
  { id: 'logo', width: 8, height: 8, fixed: { page: 0, x: 22, y: 2 } },
  { id: 'grass', width: 4, height: 4, margin: 1 },
  { id: 'ui-panel', width: 14, height: 6 },
  { id: 'giant', width: 40, height: 40 }, // too large on purpose
];

console.log('=== pack ===');
const result = pack({ pageWidth, pageHeight, spacing, items });
console.log(`pages: ${result.pages}, proof: ${JSON.stringify(result.proof)}`);
for (const p of result.placements) {
  console.log(
    `  ${p.id.padEnd(9)} page=${p.page} x=${p.x} y=${p.y} w=${p.width} h=${p.height} rotated=${p.rotated}`,
  );
}
for (const u of result.unpacked) console.log(`  UNPACKED ${u.id}: ${u.reason}`);

// Independent verification, rasterized into self-built RGBA images.
const check = verifyAll(pageWidth, pageHeight, spacing, items, result.placements);
console.log(`verify: ${check.ok ? 'OK' : 'FAILED ' + check.errors.join('; ')}`);

// Render page 0 occupancy.
const img = new RGBAImage(pageWidth, pageHeight);
for (const p of result.placements) {
  if (p.page !== 0) continue;
  img.fillRect(p.x, p.y, p.width, p.height, colorForId(p.id));
}
console.log('--- page 0 sprite pixels ---');
console.log(img.toAscii());

// UV mapping demo for a rotated item.
const rotated = result.placements.find((p) => p.rotated);
if (rotated) {
  const tl = uvToPage(rotated, 0, 0);
  const br = uvToPage(rotated, 1, 1);
  console.log(`uv demo (${rotated.id}): uv(0,0)->(${tl.x},${tl.y}) uv(1,1)->(${br.x},${br.y})`);
}

console.log('=== update: add "bush", resize "rock", remove "grass" ===');
const snapshot: LayoutSnapshot = { pageWidth, pageHeight, spacing, items, ...result };
const update = planUpdate(snapshot, {
  add: [{ id: 'bush', width: 6, height: 6 }],
  remove: ['grass'],
  resize: [{ id: 'rock', width: 7, height: 7 }],
});
if (!update.ok) {
  console.log(`update rejected: ${update.error}`);
} else {
  const moved = update.moves.filter((m) => m.kind !== 'stay');
  console.log(`moves: ${update.moves.filter((m) => m.kind === 'stay').length} stay, ${moved.length} changed`);
  for (const m of moved) {
    const to = m.to ? `page=${m.to.page} x=${m.to.x} y=${m.to.y} w=${m.to.width} h=${m.to.height}` : '-';
    console.log(`  ${m.kind.padEnd(6)} ${m.id.padEnd(9)} -> ${to}`);
  }
  const allItems = [
    ...items.filter((i) => i.id !== 'grass' && i.id !== 'rock'),
    { id: 'rock', width: 7, height: 7 },
    { id: 'bush', width: 6, height: 6 },
  ];
  const recheck = verifyAll(pageWidth, pageHeight, spacing, allItems, update.placements);
  console.log(`verify after update: ${recheck.ok ? 'OK' : 'FAILED ' + recheck.errors.join('; ')}`);
}
