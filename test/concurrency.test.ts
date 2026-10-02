import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  decodeMessage, encodeMessage,
  EntityGraph, IntegrityInspector,
} from '../src/index.ts';
import type { MessageDescriptor, DecodedMessage } from '../src/index.ts';

const Doc: MessageDescriptor = {
  typeName: 'Doc',
  fields: [
    { name: 'id', number: 1, kind: 'uint64', id: true },
    { name: 'parentId', number: 2, kind: 'uint64', ref: 'Doc' },
    { name: 'title', number: 3, kind: 'string' },
    { name: 'weights', number: 4, kind: 'sint64', repeated: true, packed: true },
    { name: 'blob', number: 5, kind: 'bytes' },
  ],
};

function makeCorpus(n: number): Uint8Array[] {
  const out: Uint8Array[] = [];
  for (let i = 0; i < n; i++) {
    out.push(encodeMessage(Doc, {
      id: BigInt(i + 1),
      parentId: BigInt(((i + 1) % n) + 1),
      title: `doc-${i}-并发安全`,
      weights: [BigInt(i), -BigInt(i), 9223372036854775807n, -9223372036854775808n],
      blob: new Uint8Array([i & 0xff, 0xaa, 0xbb]),
    }));
  }
  return out;
}

const tick = () => new Promise((resolve) => setImmediate(resolve));

test('并发: 200 路并行解码结果与顺序解码逐字节一致', async () => {
  const corpus = makeCorpus(200);
  const sequential = corpus.map((b) => decodeMessage(Doc, b));
  const parallel = await Promise.all(
    corpus.map(async (b, i) => {
      if (i % 3 === 0) await tick(); // 交错让出事件循环
      return decodeMessage(Doc, b);
    }),
  );
  assert.deepEqual(parallel, sequential);
});

test('并发: 解码不修改输入缓冲，输出之间完全隔离', async () => {
  const corpus = makeCorpus(64);
  const hashesBefore = corpus.map((b) => createHash('sha256').update(b).digest('hex'));
  const results = await Promise.all(corpus.map(async (b) => decodeMessage(Doc, b)));
  const hashesAfter = corpus.map((b) => createHash('sha256').update(b).digest('hex'));
  assert.deepEqual(hashesAfter, hashesBefore, '输入缓冲被解码过程修改');
  // 修改一个解码结果不影响其它结果
  const first = results[0] as Record<string, unknown>;
  (first.weights as bigint[])[0] = -999n;
  const second = results[1] as Record<string, unknown>;
  assert.notEqual((second.weights as bigint[])[0], -999n);
});

test('无泄漏: 解码产物不保留输入缓冲引用（bytes 字段为拷贝）', () => {
  const buf = encodeMessage(Doc, { id: 1n, blob: new Uint8Array([1, 2, 3]) });
  const decoded = decodeMessage(Doc, buf) as Record<string, unknown>;
  buf.fill(0xee); // 解码后破坏源缓冲
  assert.deepEqual([...(decoded.blob as Uint8Array)], [1, 2, 3]);
});

test('无泄漏: 重复解码不累积全局状态，堆增长有界', () => {
  const gc = (globalThis as { gc?: () => void }).gc;
  if (!gc) return; // 未以 --expose-gc 运行时跳过硬性断言
  const buf = makeCorpus(1)[0];
  const digest = () => JSON.stringify(decodeMessage(Doc, buf), (_, v) => (typeof v === 'bigint' ? String(v) : v));
  digest();
  gc();
  const before = process.memoryUsage().heapUsed;
  for (let i = 0; i < 20000; i++) digest(); // 结果立即丢弃
  gc();
  const growth = process.memoryUsage().heapUsed - before;
  assert.ok(growth < 8 * 1024 * 1024, `堆增长 ${growth} 字节，疑似泄漏`);
});

test('并发: 多巡检器对同一实体图并行巡检结果一致且图不被修改', async () => {
  const corpus = makeCorpus(50);
  const graph = new EntityGraph();
  for (const buf of corpus) graph.collect(Doc, decodeMessage(Doc, buf) as DecodedMessage);
  graph.addRoot('Doc', '1');
  const snapshot = JSON.stringify([...graph.entities.keys()]);
  const reports = await Promise.all(
    Array.from({ length: 32 }, async (_, i) => {
      if (i % 2 === 0) await tick();
      return new IntegrityInspector(graph).inspect();
    }),
  );
  const first = JSON.stringify(reports[0]);
  for (const r of reports) assert.equal(JSON.stringify(r), first);
  assert.equal(JSON.stringify([...graph.entities.keys()]), snapshot, '巡检修改了实体图');
});
