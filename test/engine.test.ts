import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CHUNK_SIZE, Int32Vector } from '../src/vector.ts';
import { add, mul, addScalar, mulScalar, mulAddScalar } from '../src/kernels.ts';
import { makeColumn, evalMulAddScalar } from '../src/engine.ts';
import { evalRowWise } from '../src/rowwise.ts';

function fill(vec: Int32Vector, fn: (i: number) => number): Int32Vector {
  for (let i = 0; i < vec.length; i++) vec.data[i] = fn(i);
  return vec;
}

test('Vector 默认 1024 行定长，内存紧凑连续', () => {
  const v = new Int32Vector();
  assert.equal(v.length, CHUNK_SIZE);
  assert.equal(v.data.length, 1024);
  assert.equal(v.data.byteLength, 1024 * 4);
});

test('列加法内核 add', () => {
  const a = fill(new Int32Vector(), (i) => i);
  const b = fill(new Int32Vector(), (i) => 2 * i);
  const out = new Int32Vector();
  add(a, b, out);
  for (let i = 0; i < CHUNK_SIZE; i++) assert.equal(out.data[i], 3 * i);
});

test('列乘法内核 mul', () => {
  const a = fill(new Int32Vector(), (i) => i);
  const b = fill(new Int32Vector(), (i) => i + 1);
  const out = new Int32Vector();
  mul(a, b, out);
  for (let i = 0; i < CHUNK_SIZE; i++) assert.equal(out.data[i], i * (i + 1));
});

test('标量内核 addScalar / mulScalar', () => {
  const a = fill(new Int32Vector(), (i) => i - 500);
  const out = new Int32Vector();
  addScalar(a, 10, out);
  for (let i = 0; i < CHUNK_SIZE; i++) assert.equal(out.data[i], i - 490);
  mulScalar(a, 3, out);
  for (let i = 0; i < CHUNK_SIZE; i++) assert.equal(out.data[i], (i - 500) * 3);
});

test("融合内核 mulAddScalar 计算 'A * B + 10'", () => {
  const a = fill(new Int32Vector(), (i) => (i % 97) - 48);
  const b = fill(new Int32Vector(), (i) => (i % 31) - 15);
  const out = new Int32Vector();
  mulAddScalar(a, b, 10, out);
  for (let i = 0; i < CHUNK_SIZE; i++) {
    assert.equal(out.data[i], a.data[i] * b.data[i] + 10);
  }
});

test("引擎批量执行 'A * B + 10'（1024 行两列）", () => {
  const n = 1024;
  const colA = makeColumn('A', fill(new Int32Vector(n), (i) => i - 512).data);
  const colB = makeColumn('B', fill(new Int32Vector(n), (i) => (i * 7) % 100).data);
  const result = evalMulAddScalar(colA, colB, 10);
  assert.equal(result.length, 1); // 恰好一个 Chunk
  for (let i = 0; i < n; i++) {
    assert.equal(result[0].data[i], colA.chunks[0].data[i] * colB.chunks[0].data[i] + 10);
  }
});

test('引擎跨多个 Chunk 处理任意长度列', () => {
  const n = CHUNK_SIZE * 3 + 17;
  const colA = makeColumn('A', fill(new Int32Vector(n), (i) => i).data);
  const colB = makeColumn('B', fill(new Int32Vector(n), () => 2).data);
  const result = evalMulAddScalar(colA, colB, 10);
  assert.equal(result.length, 4);
  assert.equal(result[3].length, 17);
  let idx = 0;
  for (const chunk of result) {
    for (let i = 0; i < chunk.length; i++, idx++) {
      assert.equal(chunk.data[i], idx * 2 + 10);
    }
  }
});

test('向量化结果与逐行（火山模型）基线完全一致', () => {
  const n = CHUNK_SIZE * 2;
  const colA = makeColumn('A', fill(new Int32Vector(n), (i) => (i * 13) % 251 - 125).data);
  const colB = makeColumn('B', fill(new Int32Vector(n), (i) => (i * 29) % 173 - 86).data);
  const fast = evalMulAddScalar(colA, colB, 10);
  const slow = evalRowWise(colA, colB, 10);
  let idx = 0;
  for (const chunk of fast) {
    for (let i = 0; i < chunk.length; i++, idx++) {
      assert.equal(chunk.data[i], slow[idx]);
    }
  }
});

test('长度不匹配时抛出错误', () => {
  const a = new Int32Vector(1024);
  const b = new Int32Vector(512);
  const out = new Int32Vector(1024);
  assert.throws(() => add(a, b, out), /length mismatch/);
});

test('吞吐性能：向量化数倍快于逐行迭代模型', () => {
  const n = CHUNK_SIZE * 64; // 65536 行
  const colA = makeColumn('A', fill(new Int32Vector(n), (i) => i % 997).data);
  const colB = makeColumn('B', fill(new Int32Vector(n), (i) => i % 89).data);

  const bench = (fn: () => unknown, iters: number): number => {
    fn(); // 预热 JIT
    const t0 = process.hrtime.bigint();
    for (let i = 0; i < iters; i++) fn();
    return Number(process.hrtime.bigint() - t0) / 1e6;
  };

  const iters = 100;
  const vecMs = bench(() => evalMulAddScalar(colA, colB, 10), iters);
  const rowMs = bench(() => evalRowWise(colA, colB, 10), iters);
  const speedup = rowMs / vecMs;
  console.log(
    `    [perf] vectorized=${vecMs.toFixed(1)}ms row-wise=${rowMs.toFixed(1)}ms ` +
      `speedup=${speedup.toFixed(2)}x (${iters} iters x ${n} rows)`,
  );
  assert.ok(speedup >= 2, `expected >=2x speedup, got ${speedup.toFixed(2)}x`);
});
