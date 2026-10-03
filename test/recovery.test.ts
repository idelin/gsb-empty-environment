import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LogManager } from '../src/log/logManager.ts';
import { DiskStorage } from '../src/storage/disk.ts';
import { BufferPool } from '../src/storage/bufferPool.ts';
import { TransactionManager } from '../src/txn/transactionManager.ts';
import { RecoveryEngine } from '../src/recovery/recoveryEngine.ts';

interface Scenario {
  dir: string;
  walPath: string;
  dbPath: string;
}

/**
 * 构造验收 WAL：
 *  T1 已提交（checkpoint 之前），T2 已提交（checkpoint 之后），
 *  T3/T4/T5 未提交，随后在 checkpoint 之后崩溃。
 * 崩溃时脏页全部留在内存缓冲池（no-force），仅 WAL 与旧磁盘页留存。
 */
function buildCrashScenario(opts?: { stealPage?: string }): Scenario {
  const dir = mkdtempSync(join(tmpdir(), 'aries-'));
  const walPath = join(dir, 'wal.log');
  const dbPath = join(dir, 'db.json');

  const log = new LogManager(walPath);
  const disk = new DiskStorage(dbPath);
  const pool = new BufferPool(disk);
  const tm = new TransactionManager(log, pool);

  // T1：checkpoint 前提交
  const t1 = tm.begin();
  tm.update(t1, 'P1', 0, 'T1-P1');
  tm.commit(t1);

  // 模糊检查点（此刻 P1 脏页未落盘，DPT 快照应含 P1）
  tm.checkpoint();

  // T2：checkpoint 后提交
  const t2 = tm.begin();
  tm.update(t2, 'P2', 0, 'T2-P2');
  tm.commit(t2);

  // T3/T4/T5：未提交
  const t3 = tm.begin();
  tm.update(t3, 'P1', 1, 'T3-P1');
  tm.update(t3, 'P3', 0, 'T3-P3');
  const t4 = tm.begin();
  tm.update(t4, 'P2', 1, 'T4-P2');
  const t5 = tm.begin();
  tm.update(t5, 'P1', 2, 'T5-P1');

  // steal：允许未提交的脏页被刷回磁盘（ARIES 允许，恢复必须能纠正）
  if (opts?.stealPage) pool.flushPage(opts.stealPage);

  // 崩溃：丢弃全部内存态（缓冲池/事务表），WAL 已刷盘部分留存
  log.flushAll();
  return { dir, walPath, dbPath };
}

function recoverFrom(s: Scenario, hooks?: { afterClr?: (n: number) => void }) {
  const log = new LogManager(s.walPath);
  const disk = new DiskStorage(s.dbPath);
  const pool = new BufferPool(disk);
  const engine = new RecoveryEngine(s.walPath, log, pool);
  const stats = engine.recover(true, hooks);
  return { stats, disk, log };
}

test('验收：checkpoint 后崩溃，三阶段恢复回滚未提交、持久化已提交', (t) => {
  const s = buildCrashScenario();
  try {
    const { stats, disk } = recoverFrom(s);

    // Analysis：败者事务恰为 T3/T4/T5；DPT 覆盖全部脏页
    assert.deepEqual([...stats.analysis.loserTxnIds].sort(), [3, 4, 5]);
    assert.deepEqual(
      [...stats.analysis.dirtyPageTable.keys()].sort(),
      ['P1', 'P2', 'P3'],
    );
    // redoLSN = DPT 最小 recLSN = T1 的 UPDATE（checkpoint 时 P1 已脏）
    assert.equal(stats.analysis.redoLSN, 2);

    // Redo：重复历史，6 条 UPDATE 全部重做（含已提交与未提交）
    assert.equal(stats.redoneCount, 6);
    assert.equal(stats.skippedRedoCount, 0);

    // Undo：4 条未提交修改被补偿，产生 4 条 CLR
    assert.equal(stats.undoneCount, 4);
    assert.equal(stats.clrCount, 4);

    // 最终磁盘状态：已提交持久化，未提交回滚
    const pages = disk.readAll();
    assert.equal(pages['P1'].data[0], 'T1-P1'); // 已提交保留
    assert.equal(pages['P1'].data[1], ''); // T3 回滚
    assert.equal(pages['P1'].data[2], ''); // T5 回滚
    assert.equal(pages['P2'].data[0], 'T2-P2'); // 已提交保留
    assert.equal(pages['P2'].data[1], ''); // T4 回滚
    assert.equal(pages['P3'].data[0], ''); // T3 回滚

    // WAL 中每个败者事务都有 END 收尾，且 CLR 的 undoNextLSN 链完整
    const wal = LogManager.readAll(s.walPath);
    const ends = wal.filter((r) => r.type === 'END').map((r) => r.txnId);
    assert.deepEqual(ends.sort(), [1, 2, 3, 4, 5]);
    const clrs = wal.filter((r) => r.type === 'CLR');
    assert.equal(clrs.length, 4);
    for (const clr of clrs) {
      if (clr.type === 'CLR') assert.ok(clr.undoNextLSN < clr.lsn);
    }
  } finally {
    rmSync(s.dir, { recursive: true, force: true });
  }
});

test('steal：未提交脏页崩溃前已落盘，恢复仍能正确回滚', () => {
  const s = buildCrashScenario({ stealPage: 'P1' });
  try {
    // 崩溃前 P1（含未提交的 T3/T5 修改）已被偷回磁盘
    const before = new DiskStorage(s.dbPath).readAll();
    assert.equal(before['P1'].data[1], 'T3-P1');

    const { stats, disk } = recoverFrom(s);
    // P1 的 redo 因 pageLSN 已新而被幂等跳过
    assert.ok(stats.skippedRedoCount >= 3);
    const pages = disk.readAll();
    assert.equal(pages['P1'].data[0], 'T1-P1');
    assert.equal(pages['P1'].data[1], '');
    assert.equal(pages['P1'].data[2], '');
  } finally {
    rmSync(s.dir, { recursive: true, force: true });
  }
});

test('恢复过程中再次崩溃：CLR 的 undoNextLSN 保证幂等，不重复回滚', () => {
  const s = buildCrashScenario();
  try {
    // 第一次恢复：写 2 条 CLR 后模拟再次崩溃（缓冲池丢弃，CLR 已入 WAL）
    const log1 = new LogManager(s.walPath);
    const pool1 = new BufferPool(new DiskStorage(s.dbPath));
    const engine1 = new RecoveryEngine(s.walPath, log1, pool1);
    const a1 = engine1.analysis();
    engine1.redo(a1);
    assert.throws(
      () =>
        engine1.undo(a1, {
          afterClr: (n) => {
            if (n === 2) throw new Error('CRASH during undo');
          },
        }),
      /CRASH during undo/,
    );
    // 注意：undo 在异常处中断，T5/T4 已补偿，T3 尚未

    // 第二次恢复：应沿 undoNextLSN 跳过已补偿记录
    const { stats, disk } = recoverFrom(s);
    assert.equal(stats.undoneCount, 2); // 只剩 T3 的 2 条待补偿
    assert.equal(stats.clrCount, 2);
    const pages = disk.readAll();
    assert.equal(pages['P1'].data[0], 'T1-P1');
    assert.equal(pages['P1'].data[1], '');
    assert.equal(pages['P1'].data[2], '');
    assert.equal(pages['P2'].data[0], 'T2-P2');
    assert.equal(pages['P2'].data[1], '');
    assert.equal(pages['P3'].data[0], '');
  } finally {
    rmSync(s.dir, { recursive: true, force: true });
  }
});

test('重复执行完整恢复是幂等的', () => {
  const s = buildCrashScenario();
  try {
    const first = recoverFrom(s);
    const snapshot1 = JSON.stringify(first.disk.readAll());
    const second = recoverFrom(s);
    const snapshot2 = JSON.stringify(second.disk.readAll());
    assert.equal(snapshot1, snapshot2);
    assert.equal(second.stats.undoneCount, 0); // 无败者事务
  } finally {
    rmSync(s.dir, { recursive: true, force: true });
  }
});
