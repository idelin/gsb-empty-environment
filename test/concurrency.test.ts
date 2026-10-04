import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { Wal } from '../src/wal.ts';
import { DiskStorage, BufferPool } from '../src/storage.ts';
import { TransactionManager } from '../src/txn-manager.ts';
import { Inspector } from '../src/inspector.ts';
import { RecoveryEngine } from '../src/recovery.ts';

function tmp(): { dir: string; walPath: string } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aries-conc-'));
  return { dir, walPath: path.join(dir, 'conc.wal') };
}

test('并发追加：200 条并发日志 LSN 唯一且连续', async () => {
  const { dir, walPath } = tmp();
  try {
    const wal = Wal.create(walPath);
    const promises = [];
    for (let i = 0; i < 200; i++) {
      promises.push(
        wal.append((lsn) => ({ type: 'COMMIT', lsn, txnId: `T${i % 10}`, prevLSN: 0 })),
      );
    }
    const records = await Promise.all(promises);
    await wal.drain();
    const lsns = records.map((r) => r.lsn).sort((a, b) => a - b);
    assert.deepEqual(lsns, Array.from({ length: 200 }, (_, i) => i + 1), 'LSN 必须唯一且连续');
    // 落盘顺序与 LSN 顺序一致
    const onDisk = Wal.resume(walPath).records;
    onDisk.forEach((r, i) => assert.equal(r.lsn, i + 1));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('并发同事务更新：prevLSN 链保持严格连续', async () => {
  const { dir, walPath } = tmp();
  try {
    const disk = new DiskStorage({ A: 0, B: 0, C: 0 });
    const pool = new BufferPool(disk);
    const wal = Wal.create(walPath);
    const tm = new TransactionManager(wal, pool);
    const pages = ['A', 'B', 'C'];
    const promises = [];
    for (let i = 0; i < 60; i++) {
      promises.push(tm.update('T-HOT', pages[i % 3], i));
    }
    promises.push(tm.commit('T-HOT'));
    await Promise.all(promises);
    await tm.quiesce();

    const inspector = new Inspector();
    const report = inspector.inspectWalStructure(Wal.resume(walPath).records, {
      requireClosedTxns: true,
    });
    inspector.assertOk(report);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('并发检查点：BEGIN/END 对原子落盘不被打断', async () => {
  const { dir, walPath } = tmp();
  try {
    const disk = new DiskStorage({ A: 0 });
    const pool = new BufferPool(disk);
    const wal = Wal.create(walPath);
    const tm = new TransactionManager(wal, pool);
    const promises = [];
    for (let i = 0; i < 50; i++) {
      promises.push(tm.update(`T${i % 5}`, 'A', i));
      if (i % 10 === 0) promises.push(tm.checkpoint());
    }
    await Promise.all(promises);
    await tm.quiesce();

    const log = Wal.resume(walPath).records;
    for (let i = 0; i < log.length; i++) {
      if (log[i].type === 'BEGIN_CHECKPOINT') {
        assert.equal(log[i + 1]?.type, 'END_CHECKPOINT', `LSN ${log[i].lsn} 的检查点对被并发写入打断`);
        assert.equal(log[i + 1].lsn, log[i].lsn + 1);
      }
    }
    assert.ok(log.some((r) => r.type === 'END_CHECKPOINT'));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('并发多事务提交后崩溃恢复：结果与串行执行一致', async () => {
  const { dir, walPath } = tmp();
  try {
    // 严格 2PL 假设下，不同事务写不相交的页面集合
    const disk = new DiskStorage({ A: 0, B: 0, C: 0, D: 0, E: 0, F: 0 });
    const pool = new BufferPool(disk);
    const wal = Wal.create(walPath);
    const tm = new TransactionManager(wal, pool);
    const committedPages = ['A', 'B', 'C', 'D'];
    const loserPages = ['E', 'F'];

    // 4 个已提交事务并发写各自页面；2 个未提交事务并发写后崩溃
    const promises = [];
    for (let t = 0; t < 4; t++) {
      for (let i = 0; i < 10; i++) {
        promises.push(tm.update(`TC${t}`, committedPages[t], t * 100 + i));
      }
      promises.push(tm.commit(`TC${t}`));
    }
    for (let t = 0; t < 2; t++) {
      for (let i = 0; i < 5; i++) {
        promises.push(tm.update(`TU${t}`, loserPages[t], -(t * 100 + i + 1)));
      }
    }
    await Promise.all(promises);
    await tm.quiesce();
    // 崩溃：缓冲池丢失，磁盘为初始值
    const engine = new RecoveryEngine(Wal.resume(walPath), disk);
    const report = await engine.recover();

    // 已提交事务的最后值持久化
    for (let t = 0; t < 4; t++) {
      assert.equal(disk.readPage(committedPages[t])!.data, t * 100 + 9, `页面 ${committedPages[t]} 应保留已提交终值`);
    }
    // 未提交事务（TU0/TU1 写 E、F）被完全回滚到初值
    assert.equal(report.undo.loserTxns.sort().join(','), 'TU0,TU1');
    assert.equal(report.undo.clrCount, 10);
    assert.equal(disk.readPage('E')!.data, 0);
    assert.equal(disk.readPage('F')!.data, 0);

    const inspector = new Inspector();
    const inspection = inspector.inspectAll(Wal.resume(walPath).records, disk, report.analysis, {
      requireClosedTxns: true,
    });
    inspector.assertOk(inspection);
    assert.equal(report.finalTransactionTable.size, 0, '恢复后事务表必须为空（无泄漏）');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('无泄漏：WAL 追加队列在 drain 后无悬挂任务', async () => {
  const { dir, walPath } = tmp();
  try {
    const wal = Wal.create(walPath);
    await Promise.all(
      Array.from({ length: 100 }, (_, i) =>
        wal.append((lsn) => ({ type: 'COMMIT', lsn, txnId: `T${i}`, prevLSN: 0 })),
      ),
    );
    await wal.drain();
    assert.equal(wal.currentLSN, 100);
    assert.equal(wal.records.length, 100);
    // drain 之后再次 drain 应立即 resolve（无悬挂 promise）
    await assert.doesNotReject(Promise.race([wal.drain(), new Promise((_, rej) => setTimeout(() => rej(new Error('悬挂')), 500))]));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
