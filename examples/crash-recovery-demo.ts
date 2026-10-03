/**
 * 运行样例：构造 WAL（2 已提交 + 3 未提交，checkpoint 后崩溃），
 * 执行 ARIES 三阶段恢复，随后演示多节点并发冲突仲裁。
 */
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LogManager } from '../src/log/logManager.ts';
import { DiskStorage } from '../src/storage/disk.ts';
import { BufferPool } from '../src/storage/bufferPool.ts';
import { TransactionManager } from '../src/txn/transactionManager.ts';
import { RecoveryEngine } from '../src/recovery/recoveryEngine.ts';
import { ConflictArbiter } from '../src/concurrency/arbiter.ts';

const dir = mkdtempSync(join(tmpdir(), 'aries-demo-'));
const walPath = join(dir, 'wal.log');
const dbPath = join(dir, 'db.json');

// ---------- 正常处理：产生 WAL ----------
const log = new LogManager(walPath);
const pool = new BufferPool(new DiskStorage(dbPath));
const tm = new TransactionManager(log, pool);

const t1 = tm.begin();
tm.update(t1, 'P1', 0, 'T1-committed');
tm.commit(t1);
tm.checkpoint(); // 模糊检查点
const t2 = tm.begin();
tm.update(t2, 'P2', 0, 'T2-committed');
tm.commit(t2);
const t3 = tm.begin();
tm.update(t3, 'P1', 1, 'T3-dirty');
tm.update(t3, 'P3', 0, 'T3-dirty');
const t4 = tm.begin();
tm.update(t4, 'P2', 1, 'T4-dirty');
const t5 = tm.begin();
tm.update(t5, 'P1', 2, 'T5-dirty');
log.flushAll();

console.log('=== 崩溃：T1/T2 已提交，T3/T4/T5 未提交，脏页全部丢失 ===');
console.log(`WAL 共 ${LogManager.readAll(walPath).length} 条记录\n`);

// ---------- 崩溃恢复 ----------
const engine = new RecoveryEngine(walPath, new LogManager(walPath), new BufferPool(new DiskStorage(dbPath)));
const stats = engine.recover();

console.log('[Analysis] 败者事务:', stats.analysis.loserTxnIds);
console.log('[Analysis] 脏页表:', [...stats.analysis.dirtyPageTable.entries()].map(([p, d]) => `${p}(recLSN=${d.recLSN})`).join(', '));
console.log('[Analysis] redoLSN =', stats.analysis.redoLSN);
console.log(`[Redo] 重做 ${stats.redoneCount} 条，跳过 ${stats.skippedRedoCount} 条`);
console.log(`[Undo] 补偿 ${stats.undoneCount} 条修改，写入 ${stats.clrCount} 条 CLR`);

const pages = new DiskStorage(dbPath).readAll();
console.log('\n=== 恢复后磁盘状态 ===');
for (const [pid, p] of Object.entries(pages)) {
  console.log(`${pid}: [${p.data.slice(0, 3).map((v) => v || '<empty>').join(', ')}]`);
}

// ---------- 多节点并发仲裁 ----------
console.log('\n=== 多节点并发竞争仲裁 ===');
const arb = new ConflictArbiter();
const nodes = Array.from({ length: 6 }, () => arb.registerNode());
await Promise.all(
  nodes.map((n) =>
    (async () => {
      for (let i = 0; i < 20; i++) {
        await arb.transactWithRetry<number>(n, 'counter', (cur) => (cur ?? 0) + 1);
      }
    })(),
  ),
);
console.log(`6 节点 × 20 次递增，counter = ${arb.readRegister('counter')}（期望 120）`);
console.log(`仲裁事件：GRANT=${arb.events.filter((e) => e.kind === 'GRANT').length}, COMMIT=${arb.events.filter((e) => e.kind === 'COMMIT').length}, WOUND=${arb.events.filter((e) => e.kind === 'WOUND').length}`);
arb.assertQuiescent();
console.log('仲裁器无泄漏 ✔');
