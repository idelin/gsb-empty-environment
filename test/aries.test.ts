/**
 * ARIES 恢复引擎验收测试：
 * 3 个未提交事务 + 2 个已提交事务 + 检查点后崩溃，验证三阶段恢复正确性。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WriteAheadLog } from '../src/log.ts';
import { ARIESRecovery } from '../src/recovery.ts';
import { buildCrashScenario, EXPECTED_FINAL } from '../src/scenario.ts';
import { BufferPool, DiskManager } from '../src/storage.ts';
import { AllocationTracker, SparsePage } from '../src/sparse.ts';

function readDiskValue(disk: DiskManager, pageId: number, offset: number): number {
  const image = disk.read(pageId);
  if (image === undefined) return SparsePage.DEFAULT_VALUE;
  const page = SparsePage.fromRLE(pageId, image.blocks, image.pageLSN);
  const value = page.read(offset);
  page.dispose();
  return value;
}

test('Analysis: 重建事务表与脏页表', () => {
  const { wal, disk } = buildCrashScenario();
  const pool = new BufferPool(disk);
  const analysis = new ARIESRecovery(wal, pool).analyze();

  // 事务表：只剩 3 个未提交事务 + 1 个 abort 半途事务；已提交的 T1/T2 因 END 被移除
  assert.equal(analysis.txnTable.size, 4);
  assert.deepEqual(
    [...analysis.txnTable.entries()].sort(([a], [b]) => a - b),
    [
      [3, { txnId: 3, status: 'RUNNING', lastLSN: 14 }],
      [4, { txnId: 4, status: 'RUNNING', lastLSN: 7 }],
      [5, { txnId: 5, status: 'RUNNING', lastLSN: 13 }],
      [6, { txnId: 6, status: 'ABORTING', lastLSN: 18 }],
    ],
  );

  // 脏页表：来自检查点 {p1:1, p2:2}，检查点后的记录不降低 recLSN
  assert.deepEqual([...analysis.dirtyPageTable.entries()].sort(), [[1, 1], [2, 2]]);
  assert.equal(analysis.redoLSN, 1);
  pool.shutdown();
});

test('Redo + Undo: 持久化已提交数据，回滚未提交修改', () => {
  const baseline = AllocationTracker.live;
  const { wal, disk } = buildCrashScenario();
  const pool = new BufferPool(disk);
  const stats = new ARIESRecovery(wal, pool).recover();

  // Redo：p2 磁盘 pageLSN=7，重放 LSN 9/13/16 及崩溃前的 CLR 18（历史重复含未提交与 CLR）
  assert.equal(stats.redoCount, 4);
  // 败者：T3, T4, T5, T6
  assert.deepEqual(stats.loserTxns, [3, 4, 5, 6]);
  // CLR：T3 两条 + T4 一条 + T5 两条 + T6 一条（LSN 15）= 6
  assert.equal(stats.clrCount, 6);

  for (const [pageId, offset, expected] of EXPECTED_FINAL) {
    assert.equal(readDiskValue(disk, pageId, offset), expected,
      `p${pageId}[${offset}] should be ${expected}`);
  }

  // 恢复新增 6 条 CLR + 4 条 END（每个败者事务一条）
  const clrs = [...wal.scanFrom(19)].filter((r) => r.type === 'CLR');
  assert.equal(clrs.length, 6);
  const ends = [...wal.scanFrom(19)].filter((r) => r.type === 'END');
  assert.equal(ends.length, 4);

  pool.shutdown();
  assert.equal(AllocationTracker.live, baseline, 'no leaked pages');
});

test('WAL 文件持久化：从文件恢复得到相同结果', () => {
  const path = '/tmp/aries-test.wal';
  const { disk } = buildCrashScenario(path);
  const wal = WriteAheadLog.loadFromFile(path);
  assert.equal(wal.length, 18);
  assert.equal(wal.lastCheckpointLSN(), 8);

  const pool = new BufferPool(disk);
  const stats = new ARIESRecovery(wal, pool).recover();
  assert.deepEqual(stats.loserTxns, [3, 4, 5, 6]);
  for (const [pageId, offset, expected] of EXPECTED_FINAL) {
    assert.equal(readDiskValue(disk, pageId, offset), expected);
  }
  pool.shutdown();
});

test('恢复幂等：崩溃于恢复之后，再次恢复结果一致', () => {
  const { wal, disk } = buildCrashScenario();
  const pool1 = new BufferPool(disk);
  const stats1 = new ARIESRecovery(wal, pool1).recover();
  pool1.crash(); // 恢复完成后再次崩溃（CLR 已写入 WAL）

  const pool2 = new BufferPool(disk);
  const stats2 = new ARIESRecovery(wal, pool2).recover();
  assert.equal(stats2.loserTxns.length, 0, '所有败者已 END，无 loser');
  assert.equal(stats2.clrCount, 0);
  // Redo 重放 CLR 后磁盘值不变
  for (const [pageId, offset, expected] of EXPECTED_FINAL) {
    assert.equal(readDiskValue(disk, pageId, offset), expected);
  }
  pool2.shutdown();
  assert.ok(stats1.clrCount > 0);
});

test('无检查点 + 空 WAL 边界', () => {
  const wal = new WriteAheadLog(null);
  const emptyDisk = new DiskManager();
  const pool = new BufferPool(emptyDisk);
  const stats = new ARIESRecovery(wal, pool).recover();
  assert.equal(stats.analysis.txnTable.size, 0);
  assert.equal(stats.analysis.dirtyPageTable.size, 0);
  assert.equal(stats.redoCount, 0);
  assert.equal(stats.clrCount, 0);
  pool.shutdown();
});
