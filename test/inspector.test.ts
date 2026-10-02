import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TableStore } from '../src/store.ts';
import { ChecksumIndex } from '../src/checksumIndex.ts';
import { Inspector } from '../src/inspector.ts';
import type { Row } from '../src/rowHash.ts';

function makePair(partitionSize = 4096, bucketSize = 256) {
  const sourceStore = new TableStore('source');
  const targetStore = new TableStore('target');
  const sourceIndex = new ChecksumIndex('source', { partitionSize, bucketSize });
  const targetIndex = new ChecksumIndex('target', { partitionSize, bucketSize });
  sourceIndex.track(sourceStore);
  targetIndex.track(targetStore);
  return { sourceStore, targetStore, inspector: new Inspector(sourceIndex, targetIndex) };
}

function syncRows(pair: ReturnType<typeof makePair>, count: number, offset = 0): void {
  for (let i = 0; i < count; i++) {
    const row: Row = { id: offset + i, name: `user_${offset + i}`, balance: i * 1.5 };
    pair.sourceStore.upsert(row);
    pair.targetStore.upsert({ ...row });
  }
}

test('两端一致时巡检报告无差异', () => {
  const pair = makePair();
  syncRows(pair, 10_000);
  const report = pair.inspector.patrol();
  assert.equal(report.consistent, true);
  assert.equal(report.diffs.length, 0);
  assert.equal(report.mismatchedPartitions.length, 0);
});

test('目标库单行单字段被篡改 -> 当周期捕获并定位主键', () => {
  const pair = makePair();
  syncRows(pair, 50_000);

  const victim = pair.targetStore.get(31_337)!;
  pair.targetStore.upsert({ ...victim, balance: (victim.balance as number) + 0.01 });

  const report = pair.inspector.patrol();
  assert.equal(report.consistent, false);
  assert.deepEqual(report.diffs.map((d) => d.id), [31_337]);
  assert.equal(report.diffs[0].kind, 'tampered');
  assert.deepEqual(report.mismatchedPartitions, [Math.floor(31_337 / 4096)]);
  // 下钻只扫描失配分桶，而非全量 5 万行
  assert.ok(report.rowsScanned <= 256, `rowsScanned=${report.rowsScanned}`);
});

test('目标库丢数（行被删除）-> 报告 missing_in_target', () => {
  const pair = makePair();
  syncRows(pair, 5_000);
  pair.targetStore.delete(2_048);

  const report = pair.inspector.patrol();
  assert.equal(report.consistent, false);
  assert.deepEqual(report.diffs, [{
    id: 2_048,
    kind: 'missing_in_target',
    sourceChecksum: report.diffs[0].sourceChecksum,
  }]);
});

test('目标库多出数据 -> 报告 extra_in_target', () => {
  const pair = makePair();
  syncRows(pair, 5_000);
  pair.targetStore.upsert({ id: 99_999, name: 'ghost', balance: 0 });

  const report = pair.inspector.patrol();
  assert.equal(report.consistent, false);
  assert.deepEqual(report.diffs.map((d) => [d.id, d.kind]), [[99_999, 'extra_in_target']]);
});

test('跨分区多处异常一次巡检全部定位', () => {
  const pair = makePair();
  syncRows(pair, 100_000);
  pair.targetStore.upsert({ ...pair.targetStore.get(100)!, balance: -1 });
  pair.targetStore.upsert({ ...pair.targetStore.get(50_000)!, name: 'hacked' });
  pair.targetStore.delete(99_999);

  const report = pair.inspector.patrol();
  assert.deepEqual(report.diffs.map((d) => d.id), [100, 50_000, 99_999]);
  assert.deepEqual(report.diffs.map((d) => d.kind),
    ['tampered', 'tampered', 'missing_in_target']);
  assert.equal(report.mismatchedPartitions.length, 3);
});

test('修复差异后复巡恢复一致', () => {
  const pair = makePair();
  syncRows(pair, 10_000);
  pair.targetStore.upsert({ ...pair.targetStore.get(7)!, balance: 0 });
  assert.equal(pair.inspector.patrol().consistent, false);

  pair.targetStore.upsert({ ...pair.sourceStore.get(7)! });
  assert.equal(pair.inspector.patrol().consistent, true);
});

test('大规模场景：20 万行 + 持续变更流中篡改单行，毫秒级定位', () => {
  const pair = makePair();
  syncRows(pair, 200_000);

  // 模拟持续变更流：1 万条随机更新同步到两端
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let i = 0; i < 10_000; i++) {
    const id = Math.floor(rand() * 200_000);
    const row: Row = { id, name: `user_${id}`, balance: rand() * 1000 };
    pair.sourceStore.upsert(row);
    pair.targetStore.upsert({ ...row });
  }
  assert.equal(pair.inspector.patrol().consistent, true);

  // 悄悄篡改目标库单行单字段
  const tamperId = 137_777;
  const victim = pair.targetStore.get(tamperId)!;
  pair.targetStore.upsert({ ...victim, balance: (victim.balance as number) + 0.01 });

  const report = pair.inspector.patrol();
  assert.equal(report.consistent, false);
  assert.deepEqual(report.diffs.map((d) => d.id), [tamperId]);
  assert.equal(report.diffs[0].kind, 'tampered');
  assert.ok(report.durationMs < 100, `巡检耗时 ${report.durationMs}ms，超出毫秒级要求`);
  assert.ok(report.rowsScanned <= 256, `下钻扫描行数 ${report.rowsScanned}，应只扫失配分桶`);
});
