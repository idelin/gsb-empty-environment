import { test } from 'node:test';
import assert from 'node:assert/strict';

import { CHUNK_SIZE, ALIGNMENT, Vector, makeVector } from '../src/vector.ts';
import { add, mul, addScalar, mulScalar, mulAddScalar } from '../src/kernels.ts';
import { VectorizedExecutor, VolcanoExecutor, InterpretedRowExecutor } from '../src/executor.ts';
import { runBenchmark } from '../src/benchmark.ts';

test('vectors are fixed-length chunks of 1024 int32 in contiguous memory', () => {
  const v = new Vector();
  assert.equal(v.length, CHUNK_SIZE);
  assert.equal(v.data.length, 1024);
  assert.ok(v.data instanceof Int32Array);
  assert.ok(v.data.buffer.byteLength >= 1024 * 4);
});

test('vectors are 64-byte aligned for SIMD-friendly access', () => {
  for (let i = 0; i < 8; i++) {
    const v = new Vector();
    assert.equal(v.data.byteOffset % ALIGNMENT, 0);
    assert.ok(v.isAligned);
  }
});

test('column add: out[i] = a[i] + b[i]', () => {
  const a = makeVector(CHUNK_SIZE, 1);
  const b = makeVector(CHUNK_SIZE, 2);
  const out = new Vector();
  add(a, b, out);
  for (let i = 0; i < CHUNK_SIZE; i++) {
    assert.equal(out.data[i], a.data[i] + b.data[i]);
  }
});

test('column mul: out[i] = a[i] * b[i]', () => {
  const a = makeVector(CHUNK_SIZE, 3);
  const b = makeVector(CHUNK_SIZE, 4);
  const out = new Vector();
  mul(a, b, out);
  for (let i = 0; i < CHUNK_SIZE; i++) {
    assert.equal(out.data[i], Math.imul(a.data[i], b.data[i]));
  }
});

test('scalar ops: addScalar and mulScalar', () => {
  const a = makeVector(CHUNK_SIZE, 5);
  const outAdd = new Vector();
  const outMul = new Vector();
  addScalar(a, 10, outAdd);
  mulScalar(a, 3, outMul);
  for (let i = 0; i < CHUNK_SIZE; i++) {
    assert.equal(outAdd.data[i], a.data[i] + 10);
    assert.equal(outMul.data[i], Math.imul(a.data[i], 3));
  }
});

test('acceptance: A * B + 10 over two 1024-int columns', () => {
  const a = makeVector(CHUNK_SIZE, 42);
  const b = makeVector(CHUNK_SIZE, 1337);
  const out = new Vector();
  mulAddScalar(a, b, 10, out);
  for (let i = 0; i < CHUNK_SIZE; i++) {
    assert.equal(out.data[i], Math.imul(a.data[i], b.data[i]) + 10);
  }
});

test('all execution models agree on A * B + 10', () => {
  const a = makeVector(CHUNK_SIZE, 11);
  const b = makeVector(CHUNK_SIZE, 22);
  const vectorized = new VectorizedExecutor(a, b, 10).run();
  const volcano = new VolcanoExecutor(a, b, 10).run();
  const interpreted = new InterpretedRowExecutor(a, b, 10).run();
  assert.deepEqual(Array.from(vectorized.data), Array.from(volcano));
  assert.deepEqual(Array.from(vectorized.data), Array.from(interpreted));
});

test('fused kernel matches composed add(mul(a,b), 10)', () => {
  const a = makeVector(CHUNK_SIZE, 6);
  const b = makeVector(CHUNK_SIZE, 8);
  const tmp = new Vector();
  const composed = new Vector();
  mul(a, b, tmp);
  addScalar(tmp, 10, composed);
  const fused = new Vector();
  mulAddScalar(a, b, 10, fused);
  assert.deepEqual(Array.from(fused.data), Array.from(composed.data));
});

test('throughput: vectorized model is several times faster than row-at-a-time models', () => {
  const report = runBenchmark(5000);
  console.log(
    `  vectorized=${report.vectorized.millionRowsPerSec.toFixed(1)} M rows/s, ` +
      `volcano=${report.volcano.millionRowsPerSec.toFixed(1)} M rows/s, ` +
      `interpreted=${report.interpreted.millionRowsPerSec.toFixed(1)} M rows/s`,
  );
  console.log(
    `  speedup vs volcano=${report.speedupVsVolcano.toFixed(2)}x, ` +
      `vs interpreter=${report.speedupVsInterpreted.toFixed(2)}x`,
  );
  assert.ok(
    report.speedupVsInterpreted > 2,
    `expected vectorized speedup > 2x vs row interpreter, got ${report.speedupVsInterpreted.toFixed(2)}x`,
  );
  assert.ok(
    report.speedupVsVolcano > 2,
    `expected vectorized speedup > 2x vs volcano iterator, got ${report.speedupVsVolcano.toFixed(2)}x`,
  );
});

test('Vector.from round-trips plain arrays', () => {
  const v = Vector.from([1, 2, 3, 4]);
  assert.equal(v.length, 4);
  assert.deepEqual(Array.from(v.data), [1, 2, 3, 4]);
});
