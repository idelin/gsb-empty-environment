import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SparseVector, MemoryAccountant } from '../src/sparse.ts';
import { writeVarint, readVarint, zigzagEncode, zigzagDecode } from '../src/codec.ts';

test('空向量边界：默认零值、空序列化往返', () => {
  const v = new SparseVector();
  assert.equal(v.mode, 'empty');
  assert.equal(v.size, 0);
  assert.equal(v.get(0), 0);
  assert.equal(v.get(1_000_000), 0);
  assert.equal(v.byteSize(), 0);
  const round = SparseVector.deserialize(v.serialize());
  assert.equal(round.size, 0);
  assert.equal(round.get(7), 0);
});

test('稀疏模式：巨大索引空间下内存紧凑', () => {
  const v = new SparseVector();
  v.set(0, 11);
  v.set(1_000_000_000, -22);
  v.set(50_000_000, 33);
  assert.equal(v.mode, 'sparse');
  assert.equal(v.size, 3);
  assert.equal(v.get(1_000_000_000), -22);
  assert.equal(v.get(999_999_999), 0);
  assert.ok(v.byteSize() < 1024);
  const denseCost = 8 * (1_000_000_001);
  assert.ok(v.byteSize() < denseCost / 1_000_000);
});

test('写零等价于删除', () => {
  const v = new SparseVector();
  v.set(3, 5);
  assert.equal(v.size, 1);
  v.set(3, 0);
  assert.equal(v.size, 0);
  assert.equal(v.has(3), false);
  assert.equal(v.get(3), 0);
});

test('自适应转换：稀疏到稠密，再稀疏化回归', () => {
  const v = new SparseVector();
  const n = 1000;
  for (let i = 0; i < n; i++) v.set(i, i + 1);
  assert.equal(v.mode, 'dense');
  assert.equal(v.size, n);
  for (let i = 0; i < n; i++) assert.equal(v.get(i), i + 1);
  for (let i = 0; i < n - 10; i++) v.delete(i);
  assert.equal(v.mode, 'sparse');
  assert.equal(v.size, 10);
  for (let i = n - 10; i < n; i++) assert.equal(v.get(i), i + 1);
});

test('稠密度迟滞区间不抖动', () => {
  const v = new SparseVector();
  for (let i = 0; i < 100; i++) v.set(i, 1);
  assert.equal(v.mode, 'dense');
  for (let i = 0; i < 60; i++) v.delete(i);
  assert.equal(v.mode, 'dense');
  for (let i = 60; i < 80; i++) v.delete(i);
  assert.equal(v.mode, 'sparse');
});

test('maxDenseLength 上限：超限索引强制回落稀疏', () => {
  const v = new SparseVector({ maxDenseLength: 128 });
  for (let i = 0; i < 100; i++) v.set(i, 7);
  assert.equal(v.mode, 'dense');
  v.set(10_000, 9);
  assert.equal(v.mode, 'sparse');
  assert.equal(v.get(10_000), 9);
  for (let i = 0; i < 100; i++) assert.equal(v.get(i), 7);
});

test('RLE 压缩：长同值行程序列化体积极小且无损', () => {
  const v = new SparseVector();
  for (let i = 0; i < 10_000; i++) v.set(i, 777);
  const buf = v.serialize();
  assert.ok(buf.length < 64);
  const round = SparseVector.deserialize(buf);
  assert.equal(round.size, 10_000);
  assert.equal(round.get(0), 777);
  assert.equal(round.get(9_999), 777);
});

test('序列化往返：负值、大索引、混合分布', () => {
  const v = new SparseVector();
  v.set(0, -1);
  v.set(5, 2 ** 52 - 1);
  v.set(6, -(2 ** 52 - 1));
  v.set(123_456_789, 42);
  const round = SparseVector.deserialize(v.serialize());
  assert.deepEqual([...round.entries()], [...v.entries()]);
});

test('entries 按索引升序迭代', () => {
  const v = new SparseVector();
  v.set(9, 1);
  v.set(1, 2);
  v.set(5, 3);
  assert.deepEqual([...v.entries()].map(([i]) => i), [1, 5, 9]);
});

test('非法输入拒绝：负索引、非整数、非安全整数', () => {
  const v = new SparseVector();
  assert.throws(() => v.set(-1, 1), RangeError);
  assert.throws(() => v.get(1.5), RangeError);
  assert.throws(() => v.set(0, 2.5), RangeError);
  assert.throws(() => v.set(0, Number.MAX_SAFE_INTEGER + 1), RangeError);
  assert.throws(() => new SparseVector({ denseThreshold: 0.1, sparseThreshold: 0.5 }), RangeError);
});

test('varint 边界值往返', () => {
  for (const value of [0, 1, 127, 128, 300, 2 ** 21, 2 ** 32, Number.MAX_SAFE_INTEGER]) {
    const out: number[] = [];
    writeVarint(out, value);
    const back = readVarint(Uint8Array.from(out), { offset: 0 });
    assert.equal(back, value);
  }
  assert.throws(() => writeVarint([], -1), RangeError);
  assert.throws(() => readVarint(Uint8Array.from([0x80]), { offset: 0 }), /truncated/);
});

test('zigzag 编解码往返', () => {
  for (const value of [0, 1, -1, 2, -2, 1_000_000, -1_000_000]) {
    assert.equal(zigzagDecode(zigzagEncode(value)), value);
  }
});

test('损坏的序列化数据被拒绝', () => {
  const v = new SparseVector();
  v.set(1, 10);
  v.set(2, 20);
  const buf = v.serialize();
  assert.throws(() => SparseVector.deserialize(buf.subarray(0, buf.length - 2)));
  const trailing = new Uint8Array([...buf, 0xff, 0xff]);
  assert.throws(() => SparseVector.deserialize(trailing));
});

test('海量稀疏负载：50 万元素分布于 1 亿索引空间', () => {
  const accountant = new MemoryAccountant();
  const v = new SparseVector({ accountant });
  const n = 500_000;
  let seed = 42;
  const rand = () => {
    seed = (seed * 1_103_515_245 + 12_345) % 2 ** 31;
    return seed;
  };
  const keys = new Set<number>();
  for (let i = 0; i < n; i++) {
    const index = rand() % 100_000_000;
    keys.add(index);
    v.set(index, (i % 997) + 1);
  }
  assert.equal(v.size, keys.size);
  assert.equal(v.mode, 'sparse');
  assert.ok(v.byteSize() < 8 * 100_000_000);
  assert.equal(accountant.bytes, v.byteSize());
  for (const k of keys) assert.ok(v.get(k) > 0);
  v.clear();
  assert.equal(accountant.bytes, 0);
});

test('内存记账：转换与清理严格归零', () => {
  const accountant = new MemoryAccountant();
  const v = new SparseVector({ accountant });
  assert.equal(accountant.bytes, 0);
  for (let i = 0; i < 500; i++) v.set(i, i + 1);
  assert.equal(v.mode, 'dense');
  assert.equal(accountant.bytes, v.byteSize());
  for (let i = 0; i < 499; i++) v.delete(i);
  assert.equal(v.mode, 'sparse');
  assert.equal(accountant.bytes, v.byteSize());
  v.clear();
  assert.equal(accountant.bytes, 0);
  assert.throws(() => accountant.free(1), /underflow/);
});
