import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { buildCrashScenario } from '../src/scenario.ts';
import { Wal } from '../src/wal.ts';
import { RecoveryEngine } from '../src/recovery.ts';
import { Inspector } from '../src/inspector.ts';
import { DiskStorage, BufferPool } from '../src/storage.ts';
import { TransactionManager } from '../src/txn-manager.ts';
import type { LogRecord } from '../src/types.ts';

function tmpWal(): { dir: string; walPath: string } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aries-test-'));
  return { dir, walPath: path.join(dir, 'test.wal') };
}

test('验收：3 未提交 + 2 已提交事务，Checkpoint 后崩溃，三阶段恢复', async () => {
  const { dir, walPath } = tmpWal();
  try {
    const scenario = await buildCrashScenario(walPath);

    // 崩溃前磁盘：只有 checkpoint 前刷出的 A、B 与 C
    const crashed = scenario.crashedDisk.snapshot();
    assert.deepEqual(
      Object.fromEntries([...crashed].map(([k, v]) => [k, v.data])),
      { A: 10, B: 20, C: 30, D: 0, E: 0 },
    );

    const engine = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);

    // --- Analysis ---
    const analysis = engine.analyze();
    const tt = analysis.transactionTable;
    assert.equal(tt.size, 3, '分析后事务表应恰含 3 个未提交事务');
    assert.deepEqual([...tt.keys()].sort(), ['T1', 'T3', 'T5']);
    assert.equal(tt.get('T1')!.status, 'running');
    assert.equal(tt.get('T1')!.lastLSN, 14);
    assert.equal(tt.get('T3')!.lastLSN, 10);
    assert.equal(tt.get('T5')!.lastLSN, 11);
    assert.deepEqual(
      [...analysis.dirtyPageTable.entries()].sort(),
      [['A', 1], ['B', 10], ['C', 3], ['D', 4], ['E', 9]],
      'DPT 应合并检查点快照与检查点后的新脏页',
    );

    // --- Redo：历史重复 ---
    const redone = engine.redo(analysis);
    assert.equal(redone, 5, 'D/E 的 4 条 UPDATE 与 B 的 1 条 UPDATE 需要重放');
    const afterRedo = scenario.crashedDisk.snapshot();
    assert.deepEqual(
      Object.fromEntries([...afterRedo].map(([k, v]) => [k, v.data])),
      { A: 10, B: 60, C: 30, D: 70, E: 80 },
      'Redo 后磁盘应重演全部历史（含未提交修改）',
    );

    // --- Undo：反向补偿 ---
    const undo = await engine.undo(analysis);
    assert.deepEqual(undo.loserTxns.sort(), ['T1', 'T3', 'T5']);
    assert.equal(undo.clrCount, 6, '6 条未提交 UPDATE（T1×3、T3×2、T5×1）各产生 1 条 CLR');
    assert.equal(undo.endCount, 3, '3 个败者事务各写 1 条 END');

    const final = scenario.crashedDisk.snapshot();
    assert.deepEqual(
      Object.fromEntries([...final].map(([k, v]) => [k, v.data])),
      scenario.expected,
      '已提交修改持久化，未提交修改全部回滚',
    );

    // CLR 链结构验证
    const log = Wal.resume(walPath).records;
    const clrs = log.filter((r) => r.type === 'CLR');
    assert.equal(clrs.length, 6);
    for (const clr of clrs) {
      assert.ok(clr.undoNextLSN < clr.lsn, 'CLR 的 undoNextLSN 必须指向更早记录');
    }
    const ends = log.filter((r) => r.type === 'END').map((r) => r.txnId);
    for (const txn of ['T1', 'T2', 'T3', 'T4', 'T5']) {
      assert.ok(ends.includes(txn), `事务 ${txn} 必须有 END 记录`);
    }

    // 无泄漏：恢复后事务表为空
    assert.equal(analysis.transactionTable.size, 0);

    // 全量巡检零违规
    const inspector = new Inspector();
    const report = inspector.inspectAll(log, scenario.crashedDisk, analysis, {
      requireClosedTxns: true,
    });
    inspector.assertOk(report);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('Redo 幂等：重复执行不改变状态', async () => {
  const { dir, walPath } = tmpWal();
  try {
    const scenario = await buildCrashScenario(walPath);
    const engine = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);
    const analysis = engine.analyze();
    engine.redo(analysis);
    const first = scenario.crashedDisk.snapshot();
    const second = engine.redo(analysis);
    assert.equal(second, 0, '第二次 Redo 不应再重放任何记录');
    assert.deepEqual(scenario.crashedDisk.snapshot(), first);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('Redo 跳过：磁盘 pageLSN 更新时不覆盖', async () => {
  const { dir, walPath } = tmpWal();
  try {
    const disk = new DiskStorage({ P: 0 });
    const pool = new BufferPool(disk);
    const wal = Wal.create(walPath);
    const tm = new TransactionManager(wal, pool);
    await tm.update('T1', 'P', 5);
    pool.flushAll();
    await tm.update('T1', 'P', 9);
    pool.flushAll(); // 磁盘 pageLSN 已是最新
    const engine = new RecoveryEngine(Wal.resume(walPath), disk);
    const analysis = engine.analyze();
    assert.equal(engine.redo(analysis), 0, '磁盘已最新时 Redo 应为空操作');
    assert.equal(disk.readPage('P')!.data, 9);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('Undo 可重启：前向 abort 后崩溃，恢复从 CLR 的 undoNextLSN 继续', async () => {
  const { dir, walPath } = tmpWal();
  try {
    const disk = new DiskStorage({ P: 0, Q: 0, R: 0 });
    const pool = new BufferPool(disk);
    const wal = Wal.create(walPath);
    const tm = new TransactionManager(wal, pool);
    await tm.update('T1', 'P', 1);
    await tm.update('T1', 'Q', 2);
    await tm.update('T1', 'R', 3);
    // 前向 abort 已撤销 R，随后崩溃（P、Q 尚未补偿）
    const abortPromise = tm.abort('T1');
    await tm.quiesce();
    await abortPromise;
    // 前向 abort 完整写入了 3 条 CLR；模拟“abort 中途崩溃”：截断 WAL 去掉最后 2 条 CLR 与 END
    const records = wal.records;
    const cut = records.findIndex((r) => r.type === 'ABORT');
    // 保留 ABORT + 第一条 CLR（撤销 R），制造 aborting 中崩溃
    const keep = records.slice(0, cut + 2);
    fs.writeFileSync(walPath, keep.map((r) => JSON.stringify(r)).join('\n') + '\n');
    pool.flushAll();

    const engine = new RecoveryEngine(Wal.resume(walPath), disk);
    const analysis = engine.analyze();
    assert.equal(analysis.transactionTable.get('T1')!.status, 'aborting');
    const undo = await engine.undo(analysis);
    assert.equal(undo.clrCount, 2, '只需补偿剩余的 P、Q，已撤销的 R 被 undoNextLSN 跳过');
    assert.equal(disk.readPage('P')!.data, 0);
    assert.equal(disk.readPage('Q')!.data, 0);
    assert.equal(disk.readPage('R')!.data, 0);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('恢复可重复：对恢复后的 WAL 再次恢复不改变结果', async () => {
  const { dir, walPath } = tmpWal();
  try {
    const scenario = await buildCrashScenario(walPath);
    const engine1 = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);
    await engine1.recover();
    const first = scenario.crashedDisk.snapshot();

    const engine2 = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);
    const report2 = await engine2.recover();
    assert.equal(report2.undo.clrCount, 0, '二次恢复不应再产生 CLR');
    assert.deepEqual(scenario.crashedDisk.snapshot(), first);
    assert.deepEqual(
      Object.fromEntries([...scenario.crashedDisk.snapshot()].map(([k, v]) => [k, v.data])),
      scenario.expected,
    );
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('无检查点与空 WAL 的边界恢复', async () => {
  const { dir, walPath } = tmpWal();
  try {
    // 空 WAL
    const disk = new DiskStorage({ A: 1 });
    Wal.create(walPath);
    const engine = new RecoveryEngine(Wal.resume(walPath), disk);
    const report = await engine.recover();
    assert.equal(report.analysis.transactionTable.size, 0);
    assert.equal(report.analysis.dirtyPageTable.size, 0);
    assert.equal(report.redoneCount, 0);
    assert.equal(report.undo.clrCount, 0);

    // 无检查点：从头分析
    const walPath2 = path.join(dir, 't2.wal');
    const disk2 = new DiskStorage({ A: 0 });
    const pool2 = new BufferPool(disk2);
    const wal2 = Wal.create(walPath2);
    const tm2 = new TransactionManager(wal2, pool2);
    await tm2.update('T1', 'A', 42);
    await tm2.commit('T1');
    pool2.flushAll();
    const engine2 = new RecoveryEngine(Wal.resume(walPath2), disk2);
    const report2 = await engine2.recover();
    assert.equal(report2.analysis.checkpointLSN, null);
    assert.equal(disk2.readPage('A')!.data, 42);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('Analysis 忽略检查点之前已结束的事务', async () => {
  const { dir, walPath } = tmpWal();
  try {
    const disk = new DiskStorage({ A: 0, B: 0 });
    const pool = new BufferPool(disk);
    const wal = Wal.create(walPath);
    const tm = new TransactionManager(wal, pool);
    await tm.update('T1', 'A', 1);
    await tm.commit('T1');
    pool.flushAll();
    await tm.checkpoint();
    await tm.update('T2', 'B', 2);
    const engine = new RecoveryEngine(Wal.resume(walPath), disk);
    const analysis = engine.analyze();
    assert.deepEqual([...analysis.transactionTable.keys()], ['T2']);
    assert.equal(analysis.dirtyPageTable.has('A'), false, '已刷出的 A 不应留在 DPT');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
