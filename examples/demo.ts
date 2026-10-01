import {
  FloatImage,
  Pipeline,
  RGBAImage,
  Stage,
  linearToSrgbByte,
  multiply,
  rotation,
  translation,
} from '../src/index.ts';

function alphaArt(img: FloatImage): string {
  const ramp = ' .:-=+*#%@';
  const lines: string[] = [];
  for (let y = 0; y < img.height; y++) {
    let line = '';
    for (let x = 0; x < img.width; x++) {
      const a = Math.min(0.999, Math.max(0, img.get(x, y)[3]));
      line += ramp[Math.floor(a * ramp.length)];
    }
    lines.push(line);
  }
  return lines.join('\n');
}

console.log('=== 1. transparent red next to opaque blue, downscaled 2x (no red fringe) ===');
const src = RGBAImage.fromRows(
  Array.from({ length: 8 }, () => [
    [255, 0, 0, 0],
    [255, 0, 0, 0],
    [255, 0, 0, 0],
    [255, 0, 0, 0],
    [0, 0, 255, 255],
    [0, 0, 255, 255],
    [0, 0, 255, 255],
    [0, 0, 255, 255],
  ] as [number, number, number, number][]),
);
const shrunk = new Pipeline([Stage.scale(8, 8, 0.5, 0.5)]).render(src).toRGBA();
for (let y = 0; y < shrunk.height; y += 3) {
  const row: string[] = [];
  for (let x = 0; x < shrunk.width; x++) row.push('(' + shrunk.get(x, y).join(',') + ')');
  console.log('row ' + y + ': ' + row.join(' '));
}

console.log('');
console.log('=== 2. linear-space filtering: black|white -> 188, not 128 ===');
const bw = RGBAImage.fromRows([
  [
    [0, 0, 0, 255],
    [255, 255, 255, 255],
  ],
]);
const avg = new Pipeline([Stage.scale(2, 1, 0.5, 1)]).render(bw).toRGBA();
console.log('downscaled pixel: (' + avg.get(0, 0).join(',') + '), linearToSrgb(0.5) = ' + linearToSrgbByte(0.5));

console.log('');
console.log('=== 3. crop -> rotate -> downscale pipeline, with stage merging ===');
const sprite = new FloatImage(16, 16);
for (let y = 0; y < 16; y++) {
  for (let x = 0; x < 16; x++) {
    const inside = x >= 2 && x < 14 && y >= 2 && y < 14;
    sprite.set(x, y, inside ? [0.1, 0.4, 0.9, 1] : [0, 0, 0, 0]);
  }
}
const crop = Stage.crop(16, 16, 1, 1, 14, 14);
const rot = Stage.affine(
  14,
  14,
  14,
  14,
  multiply(translation(7, 7), multiply(rotation(Math.PI / 9), translation(-7, -7))),
  'bilinear',
);
const down = Stage.scale(14, 14, 0.5, 0.5);
const pipe = new Pipeline([crop, rot, down]);
console.log('stages before optimize: ' + pipe.stages.map((s) => s.filter).join(' -> '));
const optimized = pipe.optimize();
console.log('stages after optimize:  ' + optimized.stages.map((s) => s.filter).join(' -> '));
const out = optimized.render(sprite);
console.log(alphaArt(out));

console.log('');
console.log('=== 4. local edit -> precise dirty region, incremental render ===');
const full1 = optimized.render(sprite).clone();
const edited = sprite.clone();
edited.set(4, 4, [1, 0, 0, 1]);
const chain = optimized.propagateDirty({ x: 4, y: 4, w: 1, h: 1 });
console.log('dirty chain: ' + chain.map((r) => (r ? r.x + ',' + r.y + ' ' + r.w + 'x' + r.h : 'none')).join(' -> '));
const rowsBefore = optimized.stages.map((s) => s.rowsRendered);
const dirty = optimized.renderDirty(edited, { x: 4, y: 4, w: 1, h: 1 });
const rowsUsed = optimized.stages.map((s, k) => s.rowsRendered - rowsBefore[k]);
console.log('output dirty: ' + (dirty ? dirty.x + ',' + dirty.y + ' ' + dirty.w + 'x' + dirty.h : 'none'));
console.log('rows rendered per stage: ' + rowsUsed.join(', ') + ' (full heights: ' + optimized.stages.map((s) => s.dstH).join(', ') + ')');
const full2 = new Pipeline(optimized.stages).render(edited);
let maxDiff = 0;
for (let i = 0; i < full2.data.length; i++) maxDiff = Math.max(maxDiff, Math.abs(full2.data[i] - optimized.output().data[i]));
console.log('incremental vs full re-render max diff: ' + maxDiff.toExponential(2));
let changed = 0;
for (let i = 0; i < full2.data.length; i += 4) {
  if (Math.abs(full2.data[i] - full1.data[i]) > 1e-9) changed++;
}
console.log('changed pixels: ' + changed + ' of ' + full2.width * full2.height);

console.log('');
console.log('=== 5. transform update dirties old + new region ===');
const mover = new Pipeline([Stage.translate(8, 4, 2, 0)]);
const dot = new FloatImage(8, 4);
dot.set(1, 1, [1, 1, 1, 1]);
mover.render(dot);
const moveDirty = mover.updateStage(0, Stage.translate(8, 4, 5, 0));
console.log('dirty after update: ' + (moveDirty ? moveDirty.x + ',' + moveDirty.y + ' ' + moveDirty.w + 'x' + moveDirty.h : 'none'));
console.log('old spot alpha: ' + mover.output().get(3, 1)[3] + ', new spot alpha: ' + mover.output().get(6, 1)[3]);

console.log('');
console.log('=== 6. cancellation ===');
const big = new Pipeline([
  Stage.affine(64, 64, 64, 64, rotation(0.4), 'bilinear'),
  Stage.scale(64, 64, 0.5, 0.5),
]);
let rows = 0;
try {
  big.render(new FloatImage(64, 64), () => ++rows > 5);
  console.log('not cancelled (unexpected)');
} catch (err) {
  console.log('cancelled after ' + rows + ' row checks: ' + (err as Error).name);
}
