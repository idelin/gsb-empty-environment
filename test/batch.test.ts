import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BatchError, Pipeline, processBatch, D65, type ColorSpaceDef } from '../src/index.ts';
import { standardCompiledGraph, standardGraph } from './helpers.ts';

/** Self-built 4x3 RGBA test image (bytes): gradient, primaries, transparent. */
function makeByteImage(): Uint8ClampedArray {
  const pixels: number[] = [];
  for (let y = 0; y < 3; y++) {
    for (let x = 0; x < 4; x++) {
      pixels.push(x * 85, y * 127, (x + y) * 40, 255);
    }
  }
  const set = (i: number, r: number, g: number, b: number, a: number) => {
    pixels.splice(i * 4, 4, r, g, b, a);
  };
  set(0, 0, 0, 0, 255);
  set(1, 255, 255, 255, 255);
  set(2, 255, 0, 0, 255);
  set(5, 128, 64, 200, 0);
  return new Uint8ClampedArray(pixels);
}

test('chunked processing matches per-pixel reference and reports chunk count', () => {
  const graph = standardCompiledGraph();
  const pipeline = new Pipeline(graph, { source: 'srgb', target: 'display-p3', strategy: 'preserve' });
  const image = makeByteImage();
  const result = processBatch(pipeline, image, { chunkSize: 5 });
  assert.equal(result.chunks, 3);
  for (let p = 0; p < 12; p++) {
    const expected = pipeline.convertPixel(image[p * 4] / 255, image[p * 4 + 1] / 255, image[p * 4 + 2] / 255).rgb;
    for (let c = 0; c < 3; c++) {
      // output buffer is Float32: allow float32 rounding
      assert.ok(Math.abs(result.output[p * 4 + c] - expected[c]) <= 1e-6);
    }
    assert.equal(result.output[p * 4 + 3], image[p * 4 + 3] / 255);
  }
  assert.equal(result.reversible, true);
  assert.ok(result.roundTrip !== null && result.roundTrip.maxError <= 1e-6);
});

test('clip strategy clamps out-of-gamut pixels and marks them irreversible', () => {
  const graph = standardCompiledGraph();
  const pipeline = new Pipeline(graph, { source: 'srgb-linear', target: 'srgb', strategy: 'clip' });
  const input = new Float32Array([
    1.4, -0.3, 0.5, 1,
    0.2, 0.4, 0.6, 1,
  ]);
  const result = processBatch(pipeline, input, { chunkSize: 1 });
  assert.equal(result.chunks, 2);
  assert.deepEqual([...result.clipMask], [1, 0]);
  assert.equal(result.clippedPixels, 1);
  assert.equal(result.reversible, false);
  assert.ok(result.output[0] <= 1 && result.output[1] >= 0, 'clamped into [0,1]');
  assert.ok(result.roundTrip !== null && result.roundTrip.maxError > 1e-3, 'clip error is measured honestly');
});

test('preserve strategy keeps out-of-gamut values and stays reversible', () => {
  const graph = standardCompiledGraph();
  const pipeline = new Pipeline(graph, { source: 'srgb-linear', target: 'srgb', strategy: 'preserve' });
  const input = new Float32Array([1.4, -0.3, 0.5, 1]);
  const result = processBatch(pipeline, input);
  assert.deepEqual([...result.clipMask], [0]);
  assert.equal(result.reversible, true);
  assert.ok(result.output[0] > 1, 'super-white preserved');
  assert.ok(result.output[1] < 0, 'negative component preserved, not NaN');
  assert.ok(result.roundTrip !== null && result.roundTrip.maxError <= 1e-6);
});

test('repeated clipping never claims reversibility (cumulative mask)', () => {
  const graph = standardCompiledGraph();
  const clipPipeline = new Pipeline(graph, { source: 'srgb-linear', target: 'srgb', strategy: 'clip' });
  const identityClip = new Pipeline(graph, { source: 'srgb', target: 'srgb', strategy: 'clip' });
  const input = new Float32Array([1.4, -0.3, 0.5, 1]);
  const first = processBatch(clipPipeline, input);
  assert.equal(first.reversible, false);
  const second = processBatch(identityClip, first.output, { priorMask: first.clipMask });
  assert.deepEqual([...second.clipMask], [1], 'prior clip history must be carried');
  assert.equal(second.reversible, false, 'must not claim reversibility after earlier clip');
  const naive = processBatch(identityClip, first.output);
  assert.equal(naive.reversible, true);
});

test('a non-finite pixel fails the whole batch; no partial output is published', () => {
  const graph = standardCompiledGraph();
  const pipeline = new Pipeline(graph, { source: 'srgb', target: 'display-p3', strategy: 'preserve' });
  const input = new Float32Array(8 * 4).fill(0.5);
  input[6 * 4 + 1] = Number.NaN;
  assert.throws(
    () => processBatch(pipeline, input, { chunkSize: 4 }),
    (err: unknown) => err instanceof BatchError && err.failedChunk === 1,
  );
});

test('a conversion failure mid-batch aborts everything (all-or-nothing)', () => {
  const graph = standardGraph();
  const explosive: ColorSpaceDef = {
    id: 'explosive',
    kind: 'rgb',
    primaries: { r: [0.64, 0.33], g: [0.3, 0.6], b: [0.15, 0.06] },
    white: D65,
    transfer: {
      id: 'explosive',
      decode: (v) => v,
      encode: (v) => {
        if (v > 0.45) throw new Error('boom');
        return v;
      },
    },
  };
  graph.addSpace(explosive);
  const compiled = graph.compile();
  const pipeline = new Pipeline(compiled, { source: 'srgb-linear', target: 'explosive', strategy: 'preserve' });
  const input = new Float32Array([
    0.1, 0.1, 0.1, 1,
    0.9, 0.9, 0.9, 1,
  ]);
  assert.throws(
    () => processBatch(pipeline, input, { chunkSize: 1 }),
    (err: unknown) => err instanceof BatchError && err.failedChunk === 1,
  );
  const ok = processBatch(pipeline, new Float32Array([0.1, 0.2, 0.3, 1]));
  assert.equal(ok.chunks, 1);
});

test('transparent pixels keep exact alpha and never produce NaN', () => {
  const graph = standardCompiledGraph();
  const pipeline = new Pipeline(graph, { source: 'srgb', target: 'display-p3', strategy: 'preserve' });
  const image = makeByteImage();
  const result = processBatch(pipeline, image);
  const transparentPixel = 5;
  assert.equal(result.output[transparentPixel * 4 + 3], 0, 'alpha 0 preserved');
  for (let c = 0; c < 3; c++) {
    assert.ok(Number.isFinite(result.output[transparentPixel * 4 + c]));
  }
});

test('malformed inputs are rejected before processing', () => {
  const graph = standardCompiledGraph();
  const pipeline = new Pipeline(graph, { source: 'srgb', target: 'xyz', strategy: 'preserve' });
  assert.throws(() => processBatch(pipeline, new Float32Array(5)), BatchError);
  assert.throws(
    () => processBatch(pipeline, new Float32Array(4), { priorMask: new Uint8Array(3) }),
    BatchError,
  );
});
