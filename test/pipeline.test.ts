import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  BatchError,
  D50,
  D65,
  PlanCache,
  compileGraph,
  linearSrgbSpace,
  makeTestImage,
  processBatch,
  srgbSpace,
  xyzSpace,
} from '../src/index.ts';
import type { GraphConfig } from '../src/index.ts';

function plan() {
  const config: GraphConfig = {
    spaces: [srgbSpace(), linearSrgbSpace(), xyzSpace(D65, 'xyz-d65')],
    edges: [
      { from: 'srgb', to: 'linear-srgb' },
      { from: 'linear-srgb', to: 'xyz-d65' },
    ],
  };
  return compileGraph(config);
}

test('chunked execution matches single-shot execution', () => {
  const p = plan();
  const img = makeTestImage();
  const whole = processBatch(img.data, p, 'srgb', 'linear-srgb', { strategy: 'preserve', chunkSize: 1024 });
  const chunked = processBatch(img.data, p, 'srgb', 'linear-srgb', { strategy: 'preserve', chunkSize: 3 });
  assert.deepEqual([...chunked.pixels], [...whole.pixels]);
  assert.deepEqual([...chunked.clipMask], [...whole.clipMask]);
  assert.equal(chunked.roundtrip.maxError, whole.roundtrip.maxError);
  assert.equal(chunked.chunks, Math.ceil(16 / 3));
});

test('onChunk reports progress chunk by chunk', () => {
  const p = plan();
  const img = makeTestImage();
  const calls: Array<[number, number]> = [];
  processBatch(img.data, p, 'srgb', 'linear-srgb', {
    strategy: 'preserve',
    chunkSize: 5,
    onChunk: (done, total) => calls.push([done, total]),
  });
  assert.deepEqual(calls, [[1, 4], [2, 4], [3, 4], [4, 4]]);
});

test('partial invalid batch aborts everything: no half-published result', () => {
  const p = plan();
  const img = makeTestImage();
  const bad = Float64Array.from(img.data);
  bad[5 * 4 + 1] = Number.NaN; // pixel 5, green channel
  let chunksBeforeFailure = 0;
  let err: BatchError | null = null;
  try {
    processBatch(bad, p, 'srgb', 'linear-srgb', {
      strategy: 'preserve',
      chunkSize: 2,
      onChunk: () => chunksBeforeFailure++,
    });
  } catch (e) {
    err = e as BatchError;
  }
  assert.ok(err instanceof BatchError, 'must throw BatchError');
  assert.equal(err.pixelIndex, 5);
  // chunks 1-2 completed, chunk 3 (pixels 4-5) failed mid-way; the throw
  // escapes before any result object exists, so nothing is published.
  assert.equal(chunksBeforeFailure, 2);
});

test('alpha outside [0,1] is invalid and aborts the batch', () => {
  const p = plan();
  const img = makeTestImage();
  const bad = Float64Array.from(img.data);
  bad[3] = 1.5;
  assert.throws(() => processBatch(bad, p, 'srgb', 'linear-srgb', { strategy: 'preserve' }), BatchError);
});

test('transparent pixels convert without NaN and keep alpha', () => {
  const p = plan();
  const img = makeTestImage();
  const res = processBatch(img.data, p, 'srgb', 'linear-srgb', { strategy: 'preserve' });
  const t = 10 * 4; // pixel 10 has alpha 0
  assert.equal(res.pixels[t + 3], 0);
  for (let c = 0; c < 3; c++) assert.ok(Number.isFinite(res.pixels[t + c]));
  const s = 11 * 4; // pixel 11 has alpha 0.5
  assert.equal(res.pixels[s + 3], 0.5);
});

test('clip strategy clamps, masks irreversible pixels and reports honest error', () => {
  const p = plan();
  const img = makeTestImage();
  const res = processBatch(img.data, p, 'srgb', 'srgb', { strategy: 'clip' });
  assert.equal(res.reversible, false);
  // pixel 12 = [1.2, 0.5, -0.2] -> R and B clipped
  assert.equal(res.clipMask[12], 1 | 4);
  assert.equal(res.pixels[12 * 4], 1);
  assert.equal(res.pixels[12 * 4 + 2], 0);
  assert.ok(res.roundtrip.maxError >= 0.2, 'clipped pixels must show up in roundtrip error');
  // all other pixels untouched
  for (let i = 0; i < 16; i++) {
    if (i !== 12) assert.equal(res.clipMask[i], 0, `pixel ${i} must not be masked`);
  }
});

test('preserve strategy keeps out-of-gamut and negative values, fully reversible', () => {
  const p = plan();
  const img = makeTestImage();
  const res = processBatch(img.data, p, 'srgb', 'linear-srgb', { strategy: 'preserve' });
  assert.equal(res.reversible, true);
  assert.ok(res.roundtrip.maxError < 1e-9);
  const linB = res.pixels[12 * 4 + 2];
  assert.ok(linB < 0, `negative linear component must be preserved, got ${linB}`);
  assert.ok(Number.isFinite(linB));
});

test('repeated clipping never claims to be reversible', () => {
  const p = plan();
  const img = makeTestImage();
  const first = processBatch(img.data, p, 'srgb', 'srgb', { strategy: 'clip' });
  assert.equal(first.reversible, false);
  // second pass clips nothing new, but the prior mask carries the state
  const second = processBatch(first.pixels, p, 'srgb', 'srgb', {
    strategy: 'clip',
    priorClipMask: first.clipMask,
  });
  assert.equal(second.reversible, false);
  assert.deepEqual([...second.clipMask], [...first.clipMask]);
  // without the prior mask the same data would look reversible: the mask
  // is the only honest record of the earlier irreversible clip.
  const naive = processBatch(first.pixels, p, 'srgb', 'srgb', { strategy: 'clip' });
  assert.equal(naive.reversible, true);
});

test('hot config swap: new version, old cache entries stay isolated', () => {
  const cache = new PlanCache();
  const v1: GraphConfig = {
    spaces: [srgbSpace(), linearSrgbSpace(), xyzSpace(D65, 'xyz-d65')],
    edges: [
      { from: 'srgb', to: 'linear-srgb' },
      { from: 'linear-srgb', to: 'xyz-d65' },
    ],
  };
  const plan1 = cache.compileOrGet(v1);
  assert.equal(cache.compileOrGet(v1), plan1, 'same config must hit the cache');

  const v2: GraphConfig = {
    spaces: [srgbSpace(), linearSrgbSpace(), xyzSpace(D50, 'xyz-d50')],
    edges: [
      { from: 'srgb', to: 'linear-srgb' },
      { from: 'linear-srgb', to: 'xyz-d50' },
    ],
  };
  const plan2 = cache.compileOrGet(v2);
  assert.notEqual(plan1.version, plan2.version);
  assert.equal(cache.size, 2);
  assert.equal(cache.get(plan1.version), plan1, 'old version entry must be untouched');
  assert.equal(cache.get(plan2.version), plan2);

  const img = makeTestImage();
  const out1 = processBatch(img.data, plan1, 'srgb', 'xyz-d65', { strategy: 'preserve' });
  const out2 = processBatch(img.data, plan2, 'srgb', 'xyz-d50', { strategy: 'preserve' });
  assert.equal(out1.version, plan1.version);
  assert.equal(out2.version, plan2.version);
  // pixel 1 is white: its XYZ differs between the D65 and D50 graphs
  assert.notEqual(out1.pixels[4], out2.pixels[4]);
});
