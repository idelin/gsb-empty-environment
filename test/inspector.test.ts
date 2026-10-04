import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { Inspector, InvariantViolationError } from '../src/inspector.ts';
import { DiskStorage } from '../src/storage.ts';
import { buildCrashScenario } from '../src/scenario.ts';
import { Wal } from '../src/wal.ts';
import { RecoveryEngine } from '../src/recovery.ts';
import type { LogRecord, UpdateRecord } from '../src/types.ts';

const inspector = new Inspector();

function upd(lsn: number, txnId: string, prevLSN: number, pageId: string, after: number): UpdateRecord {
  return { type: 'UPDATE', lsn, txnId, prevLSN, pageId, before: 0, after };
}

test('合法 WAL 全量巡检零违规', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aries-insp-'));
  try {
    const walPath = path.join(dir, 'ok.wal');
    const scenario = await buildCrashScenario(walPath);
    const engine = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);
    const report = await engine.recover();
    const log = Wal.resume(walPath).records;
    const inspection = inspector.inspectAll(log, scenario.crashedDisk, report.analysis, {
      requireClosedTxns: true,
    });
    assert.deepEqual(inspection.violations, []);
    assert.ok(inspection.checks.length >= 8, '应覆盖全部检查项');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('LSN 断裂与乱序被检出', () => {
  const log: LogRecord[] = [upd(1, 'T1', 0, 'A', 1), upd(3, 'T1', 1, 'A', 2)];
  const report = inspector.inspectWalStructure(log);
  assert.ok(report.violations.some((v) => v.includes('LSN 断裂')));
});

test('prevLSN 链断裂被检出', () => {
  const log: LogRecord[] = [upd(1, 'T1', 0, 'A', 1), upd(2, 'T1', 9, 'A', 2)];
  const report = inspector.inspectWalStructure(log);
  assert.ok(report.violations.some((v) => v.includes('prevLSN 链断裂')));
});

test('悬挂页面引用被检出', () => {
  const disk = new DiskStorage({ A: 0 });
  const log: LogRecord[] = [upd(1, 'T1', 0, 'ZZZ', 1)];
  const report = inspector.inspectDanglingReferences(log, disk, null);
  assert.ok(report.violations.some((v) => v.includes('悬挂页面引用')));
});

test('悬挂 undoNextLSN 被检出', () => {
  const log: LogRecord[] = [
    upd(1, 'T1', 0, 'A', 1),
    { type: 'CLR', lsn: 2, txnId: 'T1', prevLSN: 1, pageId: 'A', before: 1, after: 0, undoNextLSN: 99 },
  ];
  const report = inspector.inspectWalStructure(log);
  assert.ok(report.violations.some((v) => v.includes('undoNextLSN=99')));
});

test('undoNextLSN 指向其他事务或未来记录被检出', () => {
  const crossTxn: LogRecord[] = [
    upd(1, 'T1', 0, 'A', 1),
    upd(2, 'T2', 0, 'B', 2),
    { type: 'CLR', lsn: 3, txnId: 'T1', prevLSN: 1, pageId: 'A', before: 1, after: 0, undoNextLSN: 2 },
  ];
  const r1 = inspector.inspectWalStructure(crossTxn);
  assert.ok(r1.violations.some((v) => v.includes('其他事务')));

  const forward: LogRecord[] = [
    upd(1, 'T1', 0, 'A', 1),
    { type: 'CLR', lsn: 2, txnId: 'T1', prevLSN: 1, pageId: 'A', before: 1, after: 0, undoNextLSN: 0 },
    upd(3, 'T1', 2, 'A', 5),
    { type: 'CLR', lsn: 4, txnId: 'T1', prevLSN: 3, pageId: 'A', before: 5, after: 0, undoNextLSN: 4 },
  ];
  const r2 = inspector.inspectWalStructure(forward);
  assert.ok(r2.violations.some((v) => v.includes('未指向更早')));
});

test('孤立环路被检出（prevLSN 成环）', () => {
  const log: LogRecord[] = [
    upd(1, 'T1', 0, 'A', 1),
    { ...upd(2, 'T1', 3, 'A', 2) },
    { ...upd(3, 'T1', 2, 'A', 3) },
  ];
  const report = inspector.inspectOrphanLoops(log);
  assert.ok(report.violations.some((v) => v.includes('孤立环路')));
});

test('合法 WAL 的链接图是 DAG（无环误报）', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aries-insp-'));
  try {
    const walPath = path.join(dir, 'dag.wal');
    const scenario = await buildCrashScenario(walPath);
    const engine = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);
    await engine.recover();
    const report = inspector.inspectOrphanLoops(Wal.resume(walPath).records);
    assert.deepEqual(report.violations, []);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('END 之后的孤立记录被检出', () => {
  const log: LogRecord[] = [
    upd(1, 'T1', 0, 'A', 1),
    { type: 'COMMIT', lsn: 2, txnId: 'T1', prevLSN: 1 },
    { type: 'END', lsn: 3, txnId: 'T1', prevLSN: 2 },
    upd(4, 'T1', 3, 'A', 2),
  ];
  const report = inspector.inspectWalStructure(log);
  assert.ok(report.violations.some((v) => v.includes('孤立记录')));
});

test('悬挂事务（缺 END）在 requireClosedTxns 下被检出，崩溃现场则豁免', () => {
  const log: LogRecord[] = [
    upd(1, 'T1', 0, 'A', 1),
    { type: 'COMMIT', lsn: 2, txnId: 'T1', prevLSN: 1 },
  ];
  const strict = inspector.inspectWalStructure(log, { requireClosedTxns: true });
  assert.ok(strict.violations.some((v) => v.includes('悬挂事务')));
  const lenient = inspector.inspectWalStructure(log, { requireClosedTxns: false });
  assert.deepEqual(lenient.violations, []);
});

test('检查点快照中的悬挂 lastLSN / recLSN 被检出', () => {
  const log: LogRecord[] = [
    upd(1, 'T1', 0, 'A', 1),
    { type: 'BEGIN_CHECKPOINT', lsn: 2, txnId: null, prevLSN: 0 },
    {
      type: 'END_CHECKPOINT', lsn: 3, txnId: null, prevLSN: 0,
      transactions: [{ txnId: 'T1', status: 'running', lastLSN: 77 }],
      dirtyPages: [{ pageId: 'A', recLSN: 88 }],
    },
  ];
  const report = inspector.inspectDanglingReferences(log, null, null);
  assert.ok(report.violations.some((v) => v.includes('lastLSN=77')));
  assert.ok(report.violations.some((v) => v.includes('recLSN=88')));
});

test('分析结果中的悬挂指针被检出', () => {
  const log: LogRecord[] = [upd(1, 'T1', 0, 'A', 1)];
  const analysis = {
    transactionTable: new Map([['T1', { txnId: 'T1', status: 'running' as const, lastLSN: 42 }]]),
    dirtyPageTable: new Map([['B', 43]]),
    checkpointLSN: null,
  };
  const report = inspector.inspectDanglingReferences(log, null, analysis);
  assert.ok(report.violations.some((v) => v.includes('事务表 T1')));
  assert.ok(report.violations.some((v) => v.includes('脏页表 B')));
});

test('数据倾斜：热点页触发告警，均匀分布不触发', () => {
  // 10 次访问中 9 次命中同一页 -> 占比 90% > 60%
  const skewed: LogRecord[] = [];
  for (let i = 1; i <= 9; i++) skewed.push(upd(i, 'T1', i - 1, 'HOT', i));
  skewed.push(upd(10, 'T1', 9, 'COLD', 1));
  const r1 = inspector.inspectDataSkew(skewed);
  assert.ok(r1.warnings.some((w) => w.includes('数据倾斜')));

  // 均匀分布
  const uniform: LogRecord[] = [];
  for (let i = 1; i <= 10; i++) uniform.push(upd(i, 'T1', i - 1, `P${i % 5}`, i));
  const r2 = inspector.inspectDataSkew(uniform);
  assert.deepEqual(r2.warnings, []);
});

test('数据倾斜边界：样本不足、单页、恰在阈值均不告警', () => {
  // 样本不足（< minSamples）
  const few: LogRecord[] = [upd(1, 'T1', 0, 'A', 1), upd(2, 'T1', 1, 'A', 2), upd(3, 'T1', 2, 'B', 1)];
  assert.deepEqual(inspector.inspectDataSkew(few).warnings, []);

  // 单页独占（页面数 < 2 无倾斜可言）
  const single: LogRecord[] = [];
  for (let i = 1; i <= 20; i++) single.push(upd(i, 'T1', i - 1, 'ONLY', i));
  assert.deepEqual(inspector.inspectDataSkew(single).warnings, []);

  // 占比恰等于阈值（严格大于才告警）：10 次中 6 次 = 60%
  const atThreshold: LogRecord[] = [];
  for (let i = 1; i <= 6; i++) atThreshold.push(upd(i, 'T1', i - 1, 'HOT', i));
  for (let i = 7; i <= 10; i++) atThreshold.push(upd(i, 'T1', i - 1, `C${i}`, i));
  const r = inspector.inspectDataSkew(atThreshold, { zScore: 99 });
  assert.deepEqual(r.warnings, [], '占比恰为阈值时不应告警');
});

test('恢复后不变式：事务泄漏与丢失更新被检出', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aries-insp-'));
  try {
    const walPath = path.join(dir, 'leak.wal');
    const scenario = await buildCrashScenario(walPath);
    const engine = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);
    const analysis = engine.analyze();
    // 不做 undo，直接检查 -> 事务表非空 = 泄漏
    const log = Wal.resume(walPath).records;
    const leaked = inspector.inspectRecoveredState(log, scenario.crashedDisk, analysis);
    assert.ok(leaked.violations.some((v) => v.includes('事务泄漏')));
    assert.ok(leaked.violations.some((v) => v.includes('丢失更新')));
    assert.throws(() => inspector.assertOk(leaked), InvariantViolationError);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('空 WAL 巡检边界：全部检查安全通过', () => {
  const disk = new DiskStorage({ A: 0 });
  const analysis = { transactionTable: new Map(), dirtyPageTable: new Map(), checkpointLSN: null };
  const report = inspector.inspectAll([], disk, analysis, { requireClosedTxns: true });
  assert.deepEqual(report.violations, []);
  assert.deepEqual(report.warnings, []);
});
