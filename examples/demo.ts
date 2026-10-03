import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/database.ts';
import { WAL } from '../src/wal.ts';
import { SparseVector, MemoryAccountant } from '../src/sparse.ts';

const dir = await mkdtemp(join(tmpdir(), 'aries-demo-'));
console.log('=== ARIES 崩溃恢复演示 ===\n');
console.log(`工作目录: ${dir}\n`);

console.log('[1] 构建负载: T1/T2 已提交, T3/T4/T5 未提交, Checkpoint 后崩溃');
const db = await Database.open(dir);

const t1 = await db.begin();
await db.set(t1, 1, 5, 100);
await db.set(t1, 2, 7, 200);
await db.commit(t1);
console.log('  T1 已提交: P1[5]=100, P2[7]=200');

await db.checkpoint();
console.log('  --- Checkpoint (脏页落盘, TT/DPT 快照写入 WAL) ---');

const t2 = await db.begin();
await db.set(t2, 1, 5, 150);
await db.commit(t2);
console.log('  T2 已提交: P1[5]=150');

const t3 = await db.begin();
await db.set(t3, 2, 7, 250);
await db.set(t3, 3, 1, 300);
const t4 = await db.begin();
await db.set(t4, 1, 5, 175);
const t5 = await db.begin();
await db.set(t5, 3, 2, 400);
await db.set(t5, 2, 9, 500);
console.log('  T3/T4/T5 未提交: P2[7]=250, P3[1]=300, P1[5]=175, P3[2]=400, P2[9]=500');

await db.wal.flush();
db.crash();
console.log('  *** 崩溃: 缓冲池与内存表丢失, 仅磁盘页与 WAL 幸存 ***\n');

const recovered = await Database.open(dir);
const report = await recovered.recover();

console.log('[2] Analysis 阶段');
for (const entry of report.analysis.txTable.values()) {
  console.log(`  事务 T${entry.txId}: status=${entry.status}, lastLSN=${entry.lastLSN}`);
}
for (const [pageId, recLSN] of [...report.analysis.dpt].sort((a, b) => a[0] - b[0])) {
  console.log(`  脏页 P${pageId}: recLSN=${recLSN}`);
}

console.log('\n[3] Redo 阶段 (重复历史)');
console.log(`  重放 LSN: [${report.redone.join(', ')}]`);

console.log('\n[4] Undo 阶段 (逆序补偿)');
console.log(`  写入 ${report.clrLSNs.length} 条 CLR: [${report.clrLSNs.join(', ')}]`);
console.log(`  回滚事务: [${[...report.ended].sort((a, b) => a - b).map((t) => `T${t}`).join(', ')}]`);

console.log('\n[5] 恢复后最终状态 (已落盘)');
console.log(`  P1[5]=${recovered.readSlot(1, 5)}  (期望 150, T2 已提交)`);
console.log(`  P2[7]=${recovered.readSlot(2, 7)}  (期望 200, T1 已提交)`);
console.log(`  P2[9]=${recovered.readSlot(2, 9)}    (期望 0, T5 已回滚)`);
console.log(`  P3[1]=${recovered.readSlot(3, 1)}    (期望 0, T3 已回滚)`);
console.log(`  P3[2]=${recovered.readSlot(3, 2)}    (期望 0, T5 已回滚)`);

const stats = recovered.stats;
console.log('\n[6] 无泄漏检查');
console.log(`  事务表=${stats.txTableSize}, 脏页表=${stats.dptSize}, WAL缓冲=${stats.walBuffered}, 脏页=${stats.dirtyPages}`);
await recovered.shutdown();
console.log(`  关闭后内存记账=${recovered.stats.accountedBytes} 字节`);

console.log('\n=== 稀疏数据内存紧凑优化演示 ===');
const accountant = new MemoryAccountant();
const vector = new SparseVector({ accountant });
for (let i = 0; i < 200_000; i++) vector.set((i * 499) % 100_000_000, i + 1);
console.log(`20 万元素散布于 1 亿索引空间: mode=${vector.mode}, 占用=${accountant.bytes} 字节`);
console.log(`  (等效稠密数组约需 ${(8 * 100_000_000 / 1024 / 1024).toFixed(0)} MB)`);
for (let i = 0; i < 10_000; i++) vector.set(i, 7);
console.log(`追加 1 万连续元素后: mode=${vector.mode}, 序列化=${vector.serialize().length} 字节 (RLE 压缩)`);
vector.clear();
console.log(`清空后内存记账=${accountant.bytes} 字节 (无泄漏)`);

const walLog = await WAL.readAll(recovered.wal.path);
console.log(`\nWAL 共 ${walLog.length} 条记录, 其中 CLR ${walLog.filter((r) => r.kind === 'clr').length} 条`);
await rm(dir, { recursive: true, force: true });
console.log('演示完成。');
