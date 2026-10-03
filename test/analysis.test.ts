/** Analysis 阶段：从事务日志精确重建 TransactionTable 与 DirtyPageTable。 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { analyze } from '../src/recovery.ts';
import { WalIndex, WalWriter, readWal } from '../src/wal.ts';
import { LogType, TxnStatus } from '../src/log-record.ts';
import { buildCrashScenario } from '../examples/scenario.ts';

function tempDir(): string {
  return mkdtempSync(join(tmpdir(), 'aries-analysis-'));
}

test('验收场景：检查点种子 + 前向扫描重建 TT/DPT', () => {
  const dir = tempDir();
  try {
    const scenario = buildCrashScenario(dir);
    const result = analyze(new WalIndex(readWal(scenario.walPath)));

    // 从事务表：恰好 3 个 loser，lastLSN 为各自最后一条记录。
    assert.equal(result.txnTable.size, 3);
    assert.deepEqual(result.txnTable.get(3), { txnId: 3, status: TxnStatus.Running, lastLSN: 13 });
    assert.deepEqual(result.txnTable.get(4), { txnId: 4, status: TxnStatus.Running, lastLSN: 11 });
    assert.deepEqual(result.txnTable.get(5), { txnId: 5, status: TxnStatus.Running, lastLSN: 14 });

    // 脏页表：recLSN 为每页检查点后首次变脏的 LSN（P1 已落盘故不在表内）。
    assert.deepEqual([...result.dirtyPageTable.entries()].sort(), [['P2', 2], ['P3', 5], ['P4', 13]]);
    assert.equal(result.redoLSN, 2);
    assert.equal(result.scanStartLSN, 10); // END_CHECKPOINT 的 LSN
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('空 WAL：空表，redoLSN=1', () => {
  const result = analyze(new WalIndex([]));
  assert.equal(result.txnTable.size, 0);
  assert.equal(result.dirtyPageTable.size, 0);
  assert.equal(result.redoLSN, 1);
});

test('无检查点：从日志头扫描；END 从事务表移除；COMMIT 置态', () => {
  const dir = tempDir();
  try {
    const path = join(dir, 'wal.log');
    const wal = new WalWriter(path);
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: null, pageId: 'P1', before: 0, after: 1 });
    wal.append({ type: LogType.Commit, txnId: 1, prevLSN: 1 });
    wal.append({ type: LogType.End, txnId: 1, prevLSN: 2 });
    wal.append({ type: LogType.Update, txnId: 2, prevLSN: null, pageId: 'P2', before: 0, after: 9 });
    wal.append({ type: LogType.Commit, txnId: 2, prevLSN: 4 });
    wal.append({ type: LogType.Update, txnId: 3, prevLSN: null, pageId: 'P2', before: 9, after: 7 });
    wal.close();

    const result = analyze(new WalIndex(readWal(path)));
    assert.equal(result.scanStartLSN, 1);
    assert.equal(result.txnTable.has(1), false, '已 END 的事务必须移除');
    assert.deepEqual(result.txnTable.get(2), { txnId: 2, status: TxnStatus.Committed, lastLSN: 5 });
    assert.deepEqual(result.txnTable.get(3), { txnId: 3, status: TxnStatus.Running, lastLSN: 6 });
    assert.deepEqual([...result.dirtyPageTable.entries()], [['P1', 1], ['P2', 4]]);
    assert.equal(result.redoLSN, 1);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('DPT 的 recLSN 取首次变脏 LSN，不被后续更新覆盖', () => {
  const dir = tempDir();
  try {
    const path = join(dir, 'wal.log');
    const wal = new WalWriter(path);
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: null, pageId: 'P1', before: 0, after: 1 });
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: 1, pageId: 'P1', before: 1, after: 2 });
    wal.append({ type: LogType.Update, txnId: 2, prevLSN: null, pageId: 'P1', before: 2, after: 3 });
    wal.close();
    const result = analyze(new WalIndex(readWal(path)));
    assert.equal(result.dirtyPageTable.get('P1'), 1);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('ABORT 置为 ABORTING；CLR 更新 lastLSN 并保持 ABORTING 状态', () => {
  const dir = tempDir();
  try {
    const path = join(dir, 'wal.log');
    const wal = new WalWriter(path);
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: null, pageId: 'P1', before: 0, after: 1 });
    wal.append({ type: LogType.Abort, txnId: 1, prevLSN: 1 });
    wal.append({ type: LogType.Clr, txnId: 1, prevLSN: 2, pageId: 'P1', before: 1, after: 0, undoNextLSN: null });
    wal.close();
    const result = analyze(new WalIndex(readWal(path)));
    assert.deepEqual(result.txnTable.get(1), { txnId: 1, status: TxnStatus.Aborting, lastLSN: 3 });
    assert.equal(result.dirtyPageTable.get('P1'), 1);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('孤儿 COMMIT（检查点后无前置记录的事务）触发一致性错误', () => {
  const dir = tempDir();
  try {
    const path = join(dir, 'wal.log');
    const wal = new WalWriter(path);
    wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null });
    wal.append({ type: LogType.EndCheckpoint, txnId: null, prevLSN: null, txnTable: [], dirtyPageTable: [] });
    wal.append({ type: LogType.Commit, txnId: 9, prevLSN: null });
    wal.close();
    assert.throws(() => analyze(new WalIndex(readWal(path))), /unknown txn 9/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('多个检查点取最后一个；截断的 BEGIN（无 END）被忽略', () => {
  const dir = tempDir();
  try {
    const path = join(dir, 'wal.log');
    const wal = new WalWriter(path);
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: null, pageId: 'P1', before: 0, after: 1 });
    wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null });
    wal.append({ type: LogType.EndCheckpoint, txnId: null, prevLSN: null, txnTable: [{ txnId: 1, status: TxnStatus.Running, lastLSN: 1 }], dirtyPageTable: [['P1', 1]] });
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: 1, pageId: 'P2', before: 0, after: 5 });
    wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null }); // 崩溃截断
    wal.close();
    const result = analyze(new WalIndex(readWal(path)));
    assert.equal(result.scanStartLSN, 3);
    assert.deepEqual(result.txnTable.get(1), { txnId: 1, status: TxnStatus.Running, lastLSN: 4 });
    assert.deepEqual([...result.dirtyPageTable.entries()], [['P1', 1], ['P2', 4]]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
