import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CancelledError,
  CancelToken,
  FloatImage,
  Pipeline,
  Stage,
  multiply,
  rotation,
  translation,
  tryMerge,
} from '../src/index.ts';
import { approx, approxImage, lcg, makeFloat } from './helpers.ts';

function randomImage(w: number, h: number, seed: number): FloatImage {
  const rnd = lcg(seed);
  return makeFloat(w, h, () => [rnd(), rnd(), rnd(), 0.5 + 0.5 * rnd()]);
}

test('crop merges into affine exactly; merged pipeline matches staged pipeline', () => {
  const src = randomImage(8, 8, 42);
  const crop = Stage.crop(8, 8, 1, 1, 6, 6);
  const rot = Stage.affine(
    6,
    6,
    6,
    6,
    multiply(translation(3, 3), multiply(rotation(Math.PI / 12), translation(-3, -3))),
    'bilinear',
  );
  const merged = tryMerge(crop, rot);
  assert.ok(merged, 'copy stage must merge into a filtered stage');
  const staged = new Pipeline([crop, rot]).render(src);
  const fused = new Pipeline([merged]).render(src);
  approxImage(fused, staged, 1e-5, 'merged equals staged');
  const optimized = new Pipeline([crop, rot]).optimize();
  assert.equal(optimized.stages.length, 1);
  approxImage(optimized.render(src), staged, 1e-5, 'optimize() equals staged');
});

test('non-equivalent merges are refused and would give different results', () => {
  const down1 = Stage.scale(9, 1, 1 / 3, 1);
  const down2 = Stage.scale(3, 1, 2 / 3, 1);
  assert.equal(tryMerge(down1, down2), null, 'area+area must not merge');
  const up1 = Stage.scale(2, 2, 2, 2);
  const up2 = Stage.scale(4, 4, 2, 2);
  assert.equal(tryMerge(up1, up2), null, 'bilinear+bilinear must not merge');
  assert.equal(new Pipeline([down1, down2]).optimize().stages.length, 2);

  const ramp = makeFloat(9, 1, (x) => [x, 0, 0, 1]);
  const staged = new Pipeline([down1, down2]).render(ramp);
  const forced = new Pipeline([Stage.scale(9, 1, 2 / 9, 1)]).render(ramp);
  approx(staged.get(0, 0)[0], 2, 1e-5, 'staged dst0');
  approx(forced.get(0, 0)[0], 16 / 9, 1e-5, 'forced dst0');
  assert.ok(
    Math.abs(staged.get(0, 0)[0] - forced.get(0, 0)[0]) > 0.1,
    'forced merge is observably wrong, so it stays forbidden',
  );
});

test('a local source edit maps to a precise output dirty region', () => {
  const src = randomImage(16, 16, 7);
  const rot = Stage.affine(
    16,
    16,
    16,
    16,
    multiply(translation(8, 8), multiply(rotation(Math.PI / 25), translation(-8, -8))),
    'bilinear',
  );
  const down = Stage.scale(16, 16, 0.5, 0.5);
  const up = Stage.scale(8, 8, 1.5, 1.5);
  const pipe = new Pipeline([rot, down, up]);
  const before = pipe.render(src).clone();

  const edited = src.clone();
  edited.set(5, 5, [1, 0, 0, 1]);
  const dirty = pipe.renderDirty(edited, { x: 5, y: 5, w: 1, h: 1 });
  assert.ok(dirty, 'dirty region exists');
  assert.ok(
    dirty.w * dirty.h < pipe.dstW * pipe.dstH,
    'dirty region is smaller than the whole output',
  );

  const reference = new Pipeline([rot, down, up]).render(edited);
  approxImage(pipe.output(), reference, 1e-5, 'incremental equals full re-render');

  for (let y = 0; y < pipe.dstH; y++) {
    for (let x = 0; x < pipe.dstW; x++) {
      const inside =
        x >= dirty.x && x < dirty.x + dirty.w && y >= dirty.y && y < dirty.y + dirty.h;
      const o = (y * pipe.dstW + x) * 4;
      for (let c = 0; c < 4; c++) {
        const delta = Math.abs(pipe.output().data[o + c] - before.data[o + c]);
        if (!inside) {
          assert.ok(delta === 0, 'pixel outside dirty region must be untouched');
        }
      }
    }
  }
});

test('incremental render touches far fewer rows than a full render', () => {
  const src = randomImage(16, 16, 9);
  const down = Stage.scale(16, 16, 0.5, 0.5);
  const pipe = new Pipeline([down]);
  pipe.render(src);
  const edited = src.clone();
  edited.set(4, 4, [1, 1, 1, 1]);
  const rowsBefore = down.rowsRendered;
  pipe.renderDirty(edited, { x: 4, y: 4, w: 1, h: 1 });
  const rowsUsed = down.rowsRendered - rowsBefore;
  assert.ok(rowsUsed <= 2, 'only the affected rows were rendered: ' + rowsUsed);
});

test('changing a stage transform dirties both old and new regions', () => {
  const src = makeFloat(8, 4, (x, y) =>
    x >= 1 && x < 3 && y >= 1 && y < 3 ? [1, 1, 1, 1] : [0, 0, 0, 0],
  );
  const pipe = new Pipeline([Stage.translate(8, 4, 2, 0)]);
  pipe.render(src);
  assert.equal(pipe.output().get(3, 1)[3], 1, 'block moved to x=3');

  const dirty = pipe.updateStage(0, Stage.translate(8, 4, 5, 0));
  assert.deepEqual(dirty, { x: 2, y: 0, w: 6, h: 4 }, 'union of old and new stage footprints');

  const reference = new Pipeline([Stage.translate(8, 4, 5, 0)]).render(src);
  approxImage(pipe.output(), reference, 1e-6, 'updated output equals full re-render');
  assert.equal(pipe.output().get(3, 1)[3], 0, 'old location cleared');
  assert.equal(pipe.output().get(6, 1)[3], 1, 'new location drawn');
});

test('render can be cancelled mid-flight and before start', () => {
  const src = randomImage(16, 16, 11);
  const rot = Stage.affine(16, 16, 16, 16, rotation(0.3), 'bilinear');
  const down = Stage.scale(16, 16, 0.5, 0.5);
  const up = Stage.scale(8, 8, 2, 2);
  const pipe = new Pipeline([rot, down, up]);

  let calls = 0;
  const abortAfterTwoRows = () => {
    calls++;
    return calls > 2;
  };
  assert.throws(() => pipe.render(src, abortAfterTwoRows), CancelledError);
  assert.equal(down.rowsRendered, 0, 'downstream stage never ran');
  assert.equal(up.rowsRendered, 0, 'downstream stage never ran');
  assert.ok(rot.rowsRendered <= 3, 'stopped right after the abort: ' + rot.rowsRendered);

  const token = new CancelToken();
  token.cancelled = true;
  const rot2 = Stage.affine(16, 16, 16, 16, rotation(0.3), 'bilinear');
  const pipe2 = new Pipeline([rot2]);
  assert.throws(() => pipe2.render(src, token.check), CancelledError);
  assert.equal(rot2.rowsRendered, 0, 'cancelled before the first row');
});

test('out-of-bounds translate leaves transparent columns', () => {
  const src = makeFloat(4, 2, () => [1, 0, 0, 1]);
  const out = new Pipeline([Stage.translate(4, 2, 2, 0)]).render(src);
  assert.deepEqual(out.get(0, 0), [0, 0, 0, 0]);
  assert.deepEqual(out.get(1, 0), [0, 0, 0, 0]);
  assert.deepEqual(out.get(2, 0), [1, 0, 0, 1]);
  assert.deepEqual(out.get(3, 0), [1, 0, 0, 1]);
});

test('1x1 pipeline end to end', () => {
  const src = makeFloat(1, 1, () => [0.5, 0.25, 0.75, 1]);
  const pipe = new Pipeline([Stage.scale(1, 1, 4, 4), Stage.scale(4, 4, 0.25, 0.25)]);
  const out = pipe.render(src);
  approx(out.get(0, 0)[0], 0.5 * 9 / 16, 1e-4, 'premultiplied r follows defined weights');
  approx(out.get(0, 0)[3], 9 / 16, 1e-4, 'alpha follows defined weights');
});

test('propagateDirty reports the rect at every stage boundary', () => {
  const move = Stage.translate(8, 8, 2, 0);
  const down = Stage.scale(8, 8, 0.5, 0.5);
  const pipe = new Pipeline([move, down]);
  const chain = pipe.propagateDirty({ x: 1, y: 1, w: 2, h: 2 });
  assert.deepEqual(chain[0], { x: 1, y: 1, w: 2, h: 2 });
  assert.deepEqual(chain[1], { x: 3, y: 1, w: 2, h: 2 });
  assert.deepEqual(chain[2], { x: 1, y: 0, w: 2, h: 2 });
});
