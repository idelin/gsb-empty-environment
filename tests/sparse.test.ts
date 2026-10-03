import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SparseVector, ConcurrentSparseVector,
  DENSE_THRESHOLD, SPARSE_THRESHOLD,
} from '../src/sparse.ts';
import { ProtobufDecodeError, ProtobufEncodeError } from '../src/errors.ts';

test('sparse: 海量稀疏场景保持 map 容器且内存紧凑', () => {
  const v = new SparseVector();
  // 跨度 100 万，仅 100 个非零项
  for (let i = 0; i < 100; i++) v.set(i * 10007, i + 1);
  const s = v.stats();
  assert.equal(s.mode, 'map');
  assert.equal(s.count, 100);
  assert.ok(s.span > 99 * 10007);
  assert.ok(s.density < SPARSE_THRESHOLD);
  assert.ok(s.estimatedBytes < 100 * 10007 * 8, '稀疏容器必须远小于稠密数组');
  for (let i = 0; i < 100; i++) assert.equal(v.get(i * 10007), i + 1);
  assert.equal(v.get(5), 0);
});

test('sparse: 密度升高自动切换 dense，删除后回落 map（迟滞）', () => {
  const v = new SparseVector();
  const n = 100;
  for (let i = 0; i < n; i++) v.set(i, i + 1); // 密度 1.0
  assert.equal(v.mode, 'dense');
  assert.ok(v.stats().density >= DENSE_THRESHOLD);
  // 删除到密度处于迟滞区间 (0.25, 0.5)：保持 dense，不抖动
  for (let i = 0; i < 60; i++) v.delete(i);
  assert.equal(v.mode, 'dense');
  // 删除到密度 < 0.25：回落 map
  for (let i = 60; i < 80; i++) v.delete(i);
  assert.equal(v.mode, 'map');
  assert.equal(v.stats().count, 20);
  // 数据在转换后保持一致
  for (let i = 80; i < n; i++) assert.equal(v.get(i), i + 1);
});

test('sparse: 边界索引 0、大索引与非法输入', () => {
  const v = new SparseVector();
  v.set(0, -42);
  assert.equal(v.get(0), -42);
  v.set(Number.MAX_SAFE_INTEGER - 1, 7);
  assert.equal(v.get(Number.MAX_SAFE_INTEGER - 1), 7);
  assert.throws(() => v.set(-1, 1), ProtobufEncodeError);
  assert.throws(() => v.set(1.5, 1), ProtobufEncodeError);
  assert.throws(() => v.set(0, 1.5), ProtobufEncodeError);
  assert.throws(() => v.get(-1), ProtobufEncodeError);
});

test('sparse: set 0 等价删除且不重复计数', () => {
  const v = new SparseVector();
  v.set(3, 9);
  v.set(3, 9);
  assert.equal(v.size, 1);
  v.set(3, 0);
  assert.equal(v.size, 0);
  v.delete(3);
  assert.equal(v.size, 0);
});

test('sparse: RLE+varint 压缩往返（含游程、间隙、负值）', () => {
  const v = new SparseVector();
  for (let i = 0; i < 50; i++) v.set(i, 7);          // 长游程
  v.set(1000, -3);
  v.set(1001, -3);                                    // 负值游程
  v.set(999999, 123456789);
  const compressed = v.compress();
  const restored = SparseVector.decompress(compressed);
  assert.deepEqual([...restored.entries()], [...v.entries()]);
  // 压缩率：50 个相同值应折叠为 1 个游程，整体远小于朴素编码
  assert.ok(compressed.length < 40, `compressed ${compressed.length} bytes`);
});

test('sparse: 压缩流截断/畸形时安全失败', () => {
  const v = new SparseVector();
  v.set(10, 5);
  v.set(20, 6);
  const buf = v.compress();
  for (let len = 0; len < buf.length; len++) {
    assert.throws(
      () => SparseVector.decompress(buf.subarray(0, len)),
      (e: unknown) => {
        assert.ok(e instanceof ProtobufDecodeError, `prefix ${len}: got ${String(e)}`);
        return true;
      },
    );
  }
  // 尾部垃圾
  const longer = new Uint8Array(buf.length + 1);
  longer.set(buf);
  longer[buf.length] = 1;
  assert.throws(() => SparseVector.decompress(longer), ProtobufDecodeError);
});

test('sparse: 并发批量变更结果与串行一致', async () => {
  const cv = new ConcurrentSparseVector();
  const tasks: Array<Promise<void>> = [];
  const workers = 32;
  const perWorker = 200;
  for (let w = 0; w < workers; w++) {
    for (let i = 0; i < perWorker; i++) {
      const idx = w * perWorker + i;
      tasks.push(cv.set(idx, idx * 2 + 1));
    }
  }
  await Promise.all(tasks);
  assert.equal(cv.inner.size, workers * perWorker);
  for (let i = 0; i < workers * perWorker; i++) {
    assert.equal(cv.get(i), i * 2 + 1);
  }
  // 并发删除一半
  const dels: Array<Promise<void>> = [];
  for (let i = 0; i < workers * perWorker; i += 2) dels.push(cv.delete(i));
  await Promise.all(dels);
  assert.equal(cv.inner.size, (workers * perWorker) / 2);
});

test('sparse: clear 释放全部内部存储，无泄漏', async () => {
  const gc = (globalThis as { gc?: () => void }).gc;
  gc?.();
  const before = process.memoryUsage().heapUsed;

  const vectors: SparseVector[] = [];
  for (let k = 0; k < 2000; k++) {
    const v = new SparseVector();
    for (let i = 0; i < 200; i++) v.set(i, i); // 触发 dense 容器
    vectors.push(v);
  }
  // 验证 dense 容器确实被分配过
  assert.ok(vectors.some((v) => v.mode === 'dense'));
  for (const v of vectors) {
    v.clear();
    const s = v.stats();
    assert.equal(s.count, 0);
    assert.equal(s.estimatedBytes, 0);
    assert.equal(s.span, 0);
    assert.equal(v.mode, 'map');
  }
  vectors.length = 0;
  gc?.();
  const after = process.memoryUsage().heapUsed;
  const growth = after - before;
  assert.ok(growth < 8 * 1024 * 1024,
    `heap grew ${growth} bytes after clear+gc, possible leak`);
});
