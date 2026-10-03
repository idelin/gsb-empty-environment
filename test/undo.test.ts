/** Undo 阶段：全局逆序回滚、CLR 补偿链、END 终止、崩溃重启不重复撤销。 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { RecoveryEngine } from '../src/recovery.ts';
import { readWal } from '../src/wal.ts';
import { LogType, type ClrRecord, type EndRecord } from '../src/log-record.ts';
import { DiskStorage } from '../src/storage.ts';
import { buildCrashScenario } from '../examples/scenario.ts';

function tempDir(): string {
  return mkdtempSync(join(tmpdir(), 'aries-undo-'));
}

function values(storage: DiskStorage): Record<string, number> {
  const out: Record<string, number> = {};
  for (const [k, v] of Object.entries(storage.snapshot())) out[k] = v.value;
  return out;
}

test('全局逆序：CLR 按被撤销记录的 LSN 降序写入，undoNextLSN 链接正确', () => {
  const dir = tempDir();
  try {
    const scenario = buildCrashScenario(dir);
    const report = new RecoveryEngine(scenario.walPath, scenario.storagePath).recover();

    // 每个 loser 的 CLR 数量 = 其 UPDATE 数量。
    assert.deepEqual(report.undo.clrsByTxn.get(5), [15, 17]);
    assert.deepEqual(report.undo.clrsByTxn.get(3), [16, 21]);
    assert.deepEqual(report.undo.clrsByTxn.get(4), [19]);
    assert.deepEqual([...report.undo.endedTxns].sort(), [3, 4, 5]);

    const records = readWal(scenario.walPath);
    const clrs = records.filter((r) => r.type === LogType.Clr) as ClrRecord[];
    // CLR 撤销顺序必须是被撤销 UPDATE 的全局 LSN 逆序：14, 13, 12, 11, 8。
    const undoneOrder = clrs.map((c) => {
      if (c.lsn === 15) return 14;
      if (c.lsn === 16) return 13;
      if (c.lsn === 17) return 12;
      if (c.lsn === 19) return 11;
      if (c.lsn === 21) return 8;
      return -1;
    });
    assert.deepEqual(undoneOrder, [14, 13, 12, 11, 8]);

    // undoNextLSN 指向被撤销记录的 prevLSN；链尾为 null。
    const byLsn = new Map(records.map((r) => [r.lsn, r]));
    const clr15 = byLsn.get(15) as ClrRecord;
    assert.equal(clr15.undoNextLSN, 12); // 撤销 T5 的 LSN14，其 prevLSN=12
    const clr17 = byLsn.get(17) as ClrRecord;
    assert.equal(clr17.undoNextLSN, null); // T5 首条记录，链尾
    // CLR 的 prevLSN 构成事务内反向链：CLR17.prevLSN = 15。
    assert.equal(clr17.prevLSN, 15);

    // 每个 loser 恰好一条 END，且 END.prevLSN 指向其最后一条 CLR。
    const ends = records.filter((r) => r.type === LogType.End) as EndRecord[];
    const loserEnds = ends.filter((e) => [3, 4, 5].includes(e.txnId as number));
    assert.equal(loserEnds.length, 3);
    const endOf5 = loserEnds.find((e) => e.txnId === 5);
    assert.equal(endOf5?.prevLSN, 17);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('恢复中再次崩溃：CLR 保证不重复撤销，重启后续走完剩余补偿', () => {
  const dir = tempDir();
  try {
    const scenario = buildCrashScenario(dir);

    // 第一次恢复：只允许写 2 条 CLR 就“断电”。
    const first = new RecoveryEngine(scenario.walPath, scenario.storagePath).recover({ maxUndoClrs: 2 });
    assert.equal(first.undo.truncated, true);
    const walAfterCrash = readWal(scenario.walPath);
    assert.equal(walAfterCrash.filter((r) => r.type === LogType.Clr).length, 2);
    assert.equal(walAfterCrash.filter((r) => r.type === LogType.End).length, 2, '仅崩溃前的 2 条 END');
    // P4 已被两条 CLR 完全补偿（41 -> 40 -> 0）。
    assert.equal(new DiskStorage(scenario.storagePath).readPage('P4').value, 0);

    // 第二次恢复：沿 undoNextLSN 续走，绝不再碰已补偿的 LSN 14/13。
    const second = new RecoveryEngine(scenario.walPath, scenario.storagePath).recover();
    assert.equal(second.undo.truncated, false);
    const clrs2 = [...second.undo.clrsByTxn.values()].flat();
    assert.equal(clrs2.length, 3, '只剩 3 条 UPDATE 待补偿');
    assert.deepEqual(values(new DiskStorage(scenario.storagePath)), scenario.expected);
    assert.deepEqual(second.openTransactions, []);

    const finalWal = readWal(scenario.walPath);
    assert.equal(finalWal.filter((r) => r.type === LogType.Clr).length, 5, '总计 5 条 CLR，无重复补偿');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('Undo 结束后磁盘值精确等于已提交状态，pageLSN 指向各自 CLR', () => {
  const dir = tempDir();
  try {
    const scenario = buildCrashScenario(dir);
    new RecoveryEngine(scenario.walPath, scenario.storagePath).recover();
    const disk = new DiskStorage(scenario.storagePath).snapshot();
    assert.deepEqual(disk, {
      P1: { value: 10, pageLSN: 21 },
      P2: { value: 20, pageLSN: 19 },
      P3: { value: 30, pageLSN: 17 },
      P4: { value: 0, pageLSN: 16 },
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('恢复幂等：完整恢复后再恢复，零重放、零补偿、零新 END', () => {
  const dir = tempDir();
  try {
    const scenario = buildCrashScenario(dir);
    new RecoveryEngine(scenario.walPath, scenario.storagePath).recover();
    const walLenAfterFirst = readWal(scenario.walPath).length;
    const diskAfterFirst = new DiskStorage(scenario.storagePath).snapshot();

    const second = new RecoveryEngine(scenario.walPath, scenario.storagePath).recover();
    assert.deepEqual(second.redo.redone, []);
    assert.equal(second.undo.clrsByTxn.size, 0);
    assert.deepEqual(second.undo.endedTxns, []);
    // 第二次恢复只追加一对空检查点记录。
    assert.equal(readWal(scenario.walPath).length, walLenAfterFirst + 2);
    assert.deepEqual(new DiskStorage(scenario.storagePath).snapshot(), diskAfterFirst);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('空 WAL 恢复：各阶段空转，仅写收尾检查点', () => {
  const dir = tempDir();
  try {
    const walPath = join(dir, 'wal.log');
    const storagePath = join(dir, 'db.json');
    const report = new RecoveryEngine(walPath, storagePath).recover();
    assert.equal(report.analysis.txnTable.size, 0);
    assert.deepEqual(report.redo.redone, []);
    assert.deepEqual(report.undo.endedTxns, []);
    assert.deepEqual(report.openTransactions, []);
    const records = readWal(walPath);
    assert.equal(records.length, 2);
    assert.equal(records[0].type, LogType.BeginCheckpoint);
    assert.equal(records[1].type, LogType.EndCheckpoint);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
