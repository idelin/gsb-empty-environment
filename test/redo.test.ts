/** Redo 阶段：历史重复、幂等性、pageLSN/DPT 三重检查、CLR 重放。 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { analyze, redo } from '../src/recovery.ts';
import { WalIndex, WalWriter, readWal } from '../src/wal.ts';
import { LogType } from '../src/log-record.ts';
import { DiskStorage } from '../src/storage.ts';
import { buildCrashScenario } from '../examples/scenario.ts';

function tempDir(): string {
  return mkdtempSync(join(tmpdir(), 'aries-redo-'));
}

test('验收场景：从 min(recLSN) 起重复历史（含未提交修改）', () => {
  const dir = tempDir();
  try {
    const scenario = buildCrashScenario(dir);
    const index = new WalIndex(readWal(scenario.walPath));
    const analysis = analyze(index);
    const storage = new DiskStorage(scenario.storagePath);

    const result = redo(index, analysis.dirtyPageTable, analysis.redoLSN, storage);

    // LSN 2(P2<-20) 11(P2<-21) 13(P4<-40) 14(P4<-41) 被重放；
    // LSN 5/12(P3 已落盘 pageLSN=12) 与 8(P1 不在 DPT) 被跳过。
    assert.deepEqual(result.redone, [2, 11, 13, 14]);
    assert.equal(result.skipped, 3);
    assert.deepEqual(storage.snapshot(), {
      P1: { value: 11, pageLSN: 8 },
      P2: { value: 21, pageLSN: 11 },
      P3: { value: 31, pageLSN: 12 },
      P4: { value: 41, pageLSN: 14 },
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('幂等：重复执行 Redo 不改变任何状态', () => {
  const dir = tempDir();
  try {
    const scenario = buildCrashScenario(dir);
    const index = new WalIndex(readWal(scenario.walPath));
    const analysis = analyze(index);
    const storage = new DiskStorage(scenario.storagePath);

    redo(index, analysis.dirtyPageTable, analysis.redoLSN, storage);
    const first = storage.snapshot();
    const second = redo(index, analysis.dirtyPageTable, analysis.redoLSN, storage);
    assert.deepEqual(second.redone, [], 'pageLSN >= lsn 的记录必须全部跳过');
    assert.deepEqual(storage.snapshot(), first);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('CLR 与 UPDATE 一样被重放（补偿也可重做）', () => {
  const dir = tempDir();
  try {
    const walPath = join(dir, 'wal.log');
    const storagePath = join(dir, 'db.json');
    const wal = new WalWriter(walPath);
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: null, pageId: 'P1', before: 0, after: 9 });
    wal.append({ type: LogType.Clr, txnId: 1, prevLSN: 1, pageId: 'P1', before: 9, after: 0, undoNextLSN: null });
    wal.close();

    const index = new WalIndex(readWal(walPath));
    const analysis = analyze(index);
    const storage = new DiskStorage(storagePath);
    const result = redo(index, analysis.dirtyPageTable, analysis.redoLSN, storage);
    assert.deepEqual(result.redone, [1, 2]);
    assert.deepEqual(storage.readPage('P1'), { value: 0, pageLSN: 2 });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('record.lsn < DPT.recLSN 的记录被跳过（教科书式防御分支）', () => {
  const dir = tempDir();
  try {
    const walPath = join(dir, 'wal.log');
    const storagePath = join(dir, 'db.json');
    const wal = new WalWriter(walPath);
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: null, pageId: 'P2', before: 0, after: 1 }); // LSN 1
    wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null }); // LSN 2
    // 检查点声明 P1 的 recLSN=5：LSN 4 对 P1 的更新被视为检查点前已落盘。
    wal.append({ type: LogType.EndCheckpoint, txnId: null, prevLSN: null, txnTable: [{ txnId: 1, status: 'RUNNING', lastLSN: 1 }], dirtyPageTable: [['P2', 1], ['P1', 5]] }); // LSN 3
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: 1, pageId: 'P1', before: 0, after: 7 }); // LSN 4
    wal.append({ type: LogType.Update, txnId: 1, prevLSN: 4, pageId: 'P1', before: 7, after: 8 }); // LSN 5
    wal.close();

    const index = new WalIndex(readWal(walPath));
    const analysis = analyze(index);
    assert.equal(analysis.redoLSN, 1);
    const storage = new DiskStorage(storagePath);
    const result = redo(index, analysis.dirtyPageTable, analysis.redoLSN, storage);
    assert.deepEqual(result.redone, [1, 5], 'LSN 4 因 4 < recLSN(5) 被跳过');
    assert.equal(result.skipped, 1);
    assert.deepEqual(storage.readPage('P1'), { value: 8, pageLSN: 5 });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('空 DPT：redoLSN 越过日志末尾，零重放', () => {
  const dir = tempDir();
  try {
    const walPath = join(dir, 'wal.log');
    const storagePath = join(dir, 'db.json');
    const wal = new WalWriter(walPath);
    wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null });
    wal.append({ type: LogType.EndCheckpoint, txnId: null, prevLSN: null, txnTable: [], dirtyPageTable: [] });
    wal.close();

    const index = new WalIndex(readWal(walPath));
    const analysis = analyze(index);
    const storage = new DiskStorage(storagePath);
    const result = redo(index, analysis.dirtyPageTable, analysis.redoLSN, storage);
    assert.deepEqual(result.redone, []);
    assert.equal(result.skipped, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
