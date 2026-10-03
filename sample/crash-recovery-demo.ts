/**
 * 运行样例：ARIES 崩溃恢复全流程演示
 * 场景：1 个种子事务 + 5 个业务事务（T2/T3 提交，T4/T5/T6 未提交），检查点之后崩溃，
 * 恢复引擎执行 Analysis → Redo → Undo 三阶段，回滚未提交修改、持久化已提交数据。
 */
import * as fs from 'node:fs';
import * as path from 'node:path';
import { DBSystem } from '../src/system.ts';
import type { LogRecord } from '../src/types.ts';

const DIR = path.join(process.cwd(), 'demo-data');
fs.rmSync(DIR, { recursive: true, force: true });

const line = (s = '') => console.log(s);
const fmtRec = (r: LogRecord) => {
  const base = `LSN=${String(r.lsn).padStart(2)} ${r.type.padEnd(16)}`;
  if (r.type === 'UPDATE' || r.type === 'CLR') {
    const extra = r.type === 'CLR' ? ` undoNext=${r.undoNextLSN}` : '';
    return `${base} T${r.txId} ${r.pageId}: '${r.before}'->'${r.after}'${extra}`;
  }
  if (r.type === 'END_CHECKPOINT') {
    return `${base} beginLSN=${r.beginLSN} TT=[${r.transactionTable
      .map((e) => `T${e.txId}:${e.status}@${e.lastLSN}`)
      .join(',')}] DPT={${r.dirtyPageTable.map((e) => `${e.pageId}@${e.recLSN}`).join(',')}}`;
  }
  if (r.type === 'BEGIN_CHECKPOINT') return base;
  return `${base} T${r.txId}`;
};

// ============ 阶段 0：正常运行，制造崩溃 ============
line('=== Phase 0: forward processing (buffer pool capacity = 3, 6 pages) ===');
let db = DBSystem.open(DIR, { bufferPoolCapacity: 3 });

// 种子数据：T0 写入初始值并提交、刷盘
const t0 = db.tx.begin();
await db.tx.update(t0, 'P1', 'A');
await db.tx.update(t0, 'P2', 'B');
await db.tx.update(t0, 'P3', 'C');
await db.tx.commit(t0);
db.flush();
line('seed committed & flushed: P1=A P2=B P3=C');

// T2、T3：最终会提交
const t1 = db.tx.begin();
await db.tx.update(t1, 'P1', 'A1');
await db.tx.update(t1, 'P2', 'B1');
const t2 = db.tx.begin();
await db.tx.update(t2, 'P3', 'C2');

// 模糊检查点：T2、T3 仍在运行
db.checkpoint();
line('--- fuzzy CHECKPOINT taken (T2, T3 active) ---');

await db.tx.commit(t1);

// T4、T5、T6：崩溃时未提交（Loser）
const t3 = db.tx.begin();
await db.tx.update(t3, 'P1', 'A3');
await db.tx.update(t3, 'P4', 'D3');
await db.tx.commit(t2);
const t4 = db.tx.begin();
await db.tx.update(t4, 'P2', 'B4');
await db.tx.update(t4, 'P5', 'E4');
const t5 = db.tx.begin();
await db.tx.update(t5, 'P3', 'C5');
await db.tx.update(t5, 'P6', 'F5');
await db.tx.update(t3, 'P4', 'D3b');

line(`\nWAL at crash (${db.wal.lastLSN} records, buffer evictions=${db.bufferPool.evictionCount}):`);
for (const rec of db.wal.scanAll()) line('  ' + fmtRec(rec));

const persistedAtCrash = db.storage.allPages();
line('\npages persisted on disk at crash:');
for (const [p, img] of [...persistedAtCrash.entries()].sort()) {
  line(`  ${p} = '${img.data}' (pageLSN=${img.pageLSN})`);
}

line('\n*** CRASH *** (buffer pool / transaction table lost)');
db.crash();

// ============ 阶段 1-3：重启恢复 ============
db = DBSystem.open(DIR, { bufferPoolCapacity: 3 });
const report = db.recover();

line('\n=== Phase 1: Analysis ===');
line(`checkpoint used at LSN=${report.analysis.checkpointLSN}, redo starts at LSN=${report.analysis.redoLSN}`);
line('transaction table after analysis:');
for (const e of report.analysis.txTable.values()) {
  line(`  T${e.txId} status=${e.status} lastLSN=${e.lastLSN}`);
}
line('dirty page table after analysis:');
for (const [p, recLSN] of [...report.analysis.dirtyPageTable.entries()].sort()) {
  line(`  ${p} recLSN=${recLSN}`);
}
line(`winners (committed, END pending): [${report.winners.map((t) => 'T' + t)}]  losers (to undo): [${report.losers.map((t) => 'T' + t)}]`);

line('\n=== Phase 2: Redo (repeat history) ===');
line(`redone LSNs: [${report.redoneLSNs.join(', ')}]`);

line('\n=== Phase 3: Undo (reverse compensation via CLR) ===');
for (const clr of report.undoCLRs) {
  line(`  CLR LSN=${clr.lsn} T${clr.txId} ${clr.pageId}: '${clr.before}'->'${clr.after}' undoNext=${clr.undoNextLSN}`);
}

// ============ 验证最终一致性 ============
line('\n=== final persisted state (after recovery flush) ===');
const expected: Record<string, string> = {
  P1: 'A1', // T1 已提交，T3 的 A3 被撤销
  P2: 'B1', // T1 已提交，T4 的 B4 被撤销
  P3: 'C2', // T2 已提交，T5 的 C5 被撤销
  P4: '',   // T3 未提交，全部撤销
  P5: '',   // T4 未提交，全部撤销
  P6: '',   // T5 未提交，全部撤销
};
let ok = true;
for (const [p, want] of Object.entries(expected)) {
  const got = db.readPersistedPage(p).data;
  const pass = got === want;
  ok &&= pass;
  line(`  ${p} = '${got}'  expected '${want}'  ${pass ? 'OK' : 'MISMATCH'}`);
}

// 幂等性：再次恢复必须无副作用
const walSizeBefore = db.wal.lastLSN;
db.crash();
const db2 = DBSystem.open(DIR, { bufferPoolCapacity: 3 });
const report2 = db2.recover();
const idempotent =
  report2.undoCLRs.length === 0 &&
  report2.losers.length === 0 &&
  report2.redoneLSNs.length === 0 &&
  db2.wal.lastLSN === walSizeBefore;
line(`\nre-recovery idempotent: ${idempotent ? 'OK' : 'FAILED'} (no new CLRs, no losers, no redo)`);
line(`no leaks: txTable=${report.analysis.txTable.size === 0 ? 'empty' : 'LEAK'} after undo, locks held=${db.lockManager.heldCount}`);

line(`\n${ok && idempotent ? 'DEMO PASSED' : 'DEMO FAILED'}`);
process.exit(ok && idempotent ? 0 : 1);
