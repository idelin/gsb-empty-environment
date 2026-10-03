/** 运行样例：构造崩溃场景 -> 三阶段恢复 -> 打印各阶段结果与最终磁盘状态。 */
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { buildCrashScenario } from './scenario.ts';
import { RecoveryEngine } from '../src/recovery.ts';
import { readWal } from '../src/wal.ts';
import { DiskStorage } from '../src/storage.ts';

const dir = mkdtempSync(join(tmpdir(), 'aries-demo-'));
const scenario = buildCrashScenario(dir);

console.log('=== 崩溃前 WAL ===');
for (const r of readWal(scenario.walPath)) {
  const extra =
    r.type === 'UPDATE' || r.type === 'CLR'
      ? ` ${r.pageId}: ${r.before} -> ${r.after}` + (r.type === 'CLR' ? ` (undoNext=${r.undoNextLSN})` : '')
      : r.type === 'END_CHECKPOINT'
        ? ` TT=${JSON.stringify(r.txnTable)} DPT=${JSON.stringify(r.dirtyPageTable)}`
        : '';
  console.log(`LSN ${String(r.lsn).padStart(2)}  ${r.type.padEnd(16)} txn=${r.txnId ?? '-'}${extra}`);
}
console.log('\n崩溃时磁盘状态:', new DiskStorage(scenario.storagePath).snapshot());

const engine = new RecoveryEngine(scenario.walPath, scenario.storagePath);
const report = engine.recover();

console.log('\n=== Analysis ===');
console.log('TransactionTable:', [...report.analysis.txnTable.entries()]);
console.log('DirtyPageTable:  ', [...report.analysis.dirtyPageTable.entries()]);
console.log('redoLSN:', report.analysis.redoLSN);

console.log('\n=== Redo ===');
console.log('重放 LSN:', report.redo.redone, ' 跳过:', report.redo.skipped);

console.log('\n=== Undo ===');
console.log('CLR:', [...report.undo.clrsByTxn.entries()]);
console.log('END 事务:', report.undo.endedTxns);

const finalDisk = new DiskStorage(scenario.storagePath).snapshot();
console.log('\n=== 恢复后磁盘状态 ===');
console.log(finalDisk);
console.log('期望值:    ', scenario.expected);
console.log('未提交事务:', scenario.txns.uncommitted, '已提交事务:', scenario.txns.committed);
console.log('恢复后打开事务(应为空):', report.openTransactions);

const ok = Object.entries(scenario.expected).every(
  ([p, v]) => finalDisk[p]?.value === v,
);
console.log(ok ? '\n✔ 恢复正确：已提交数据持久，未提交修改全部回滚' : '\n✘ 恢复结果不符');
process.exit(ok ? 0 : 1);
