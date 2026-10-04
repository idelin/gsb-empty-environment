import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { buildCrashScenario } from './scenario.ts';
import { Wal } from './wal.ts';
import { RecoveryEngine } from './recovery.ts';
import { Inspector } from './inspector.ts';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aries-demo-'));
const walPath = path.join(dir, 'demo.wal');

console.log('=== ARIES 崩溃恢复演示 ===\n');
console.log('[1] 构造 WAL：5 个事务（T2/T4 提交，T1/T3/T5 未提交），Checkpoint 后崩溃');
const scenario = await buildCrashScenario(walPath);
const log = Wal.resume(walPath).records;
for (const r of log) {
  const extra =
    r.type === 'UPDATE' || r.type === 'CLR'
      ? ` ${r.pageId} ${r.before}->${r.after}` + (r.type === 'CLR' ? ` undoNext=${r.undoNextLSN}` : '')
      : r.type === 'END_CHECKPOINT'
        ? ` TT=${r.transactions.map((t) => t.txnId).join(',')} DPT=${r.dirtyPages.map((d) => `${d.pageId}:${d.recLSN}`).join(',')}`
        : '';
  console.log(`    LSN ${String(r.lsn).padStart(2)}  ${r.type.padEnd(17)} ${r.txnId ?? '-'}${extra}`);
}

console.log('\n[2] 崩溃瞬间磁盘状态:', Object.fromEntries([...scenario.crashedDisk.snapshot()].map(([k, v]) => [k, v.data])));

console.log('\n[3] 三阶段恢复');
const engine = new RecoveryEngine(Wal.resume(walPath), scenario.crashedDisk);
const analysis = engine.analyze();
console.log('    Analysis: TT =', [...analysis.transactionTable.values()].map((e) => `${e.txnId}(${e.status},last=${e.lastLSN})`).join(', '));
console.log('             DPT =', [...analysis.dirtyPageTable.entries()].map(([p, l]) => `${p}:${l}`).join(', '));
const redoneCount = engine.redo(analysis);
console.log(`    Redo: 重放 ${redoneCount} 条记录（历史重复）`);
const undo = await engine.undo(analysis);
console.log(`    Undo: 回滚败者事务 [${undo.loserTxns.join(', ')}]，写入 ${undo.clrCount} 条 CLR、${undo.endCount} 条 END`);

console.log('\n[4] 恢复后磁盘状态:', Object.fromEntries([...scenario.crashedDisk.snapshot()].map(([k, v]) => [k, v.data])));
console.log('    期望值      :', scenario.expected);

console.log('\n[5] 形式化约束巡检（恢复后全量状态）');
const inspector = new Inspector();
const inspection = inspector.inspectAll(
  Wal.resume(walPath).records,
  scenario.crashedDisk,
  analysis,
  { requireClosedTxns: true },
);
inspector.assertOk(inspection);
console.log(`    通过 ${inspection.checks.length} 项检查，违规 ${inspection.violations.length} 项，告警 ${inspection.warnings.length} 项`);
console.log('    无悬挂引用 / 无孤立环路 / 无事务泄漏');

fs.rmSync(dir, { recursive: true, force: true });
