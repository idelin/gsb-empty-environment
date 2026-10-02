/**
 * 运行样例：构造崩溃场景 -> 从 WAL 文件执行 ARIES 三阶段恢复 -> 打印结果。
 * 运行：npm run demo
 */

import { WriteAheadLog } from './log.ts';
import { ARIESRecovery } from './recovery.ts';
import { buildCrashScenario, EXPECTED_FINAL } from './scenario.ts';
import { BufferPool } from './storage.ts';
import { AllocationTracker, SparsePage } from './sparse.ts';

const WAL_PATH = '/tmp/aries-demo.wal';

const scenario = buildCrashScenario(WAL_PATH);
console.log(`=== 崩溃前 ===`);
console.log(`WAL 已写入 ${scenario.wal.length} 条记录 -> ${WAL_PATH}`);
console.log(`已提交事务: T${scenario.committed.t1}, T${scenario.committed.t2}`);
console.log(`未提交事务: T${scenario.losers.t3}, T${scenario.losers.t4}, T${scenario.losers.t5}（另 T${scenario.aborting.t6} abort 半途）`);
console.log(`崩溃！缓冲池全部丢失。\n`);

// 从 WAL 文件重启恢复
const wal = WriteAheadLog.loadFromFile(WAL_PATH);
const pool = new BufferPool(scenario.disk);
const recovery = new ARIESRecovery(wal, pool);
const stats = recovery.recover();

console.log(`=== Analysis ===`);
console.log(`事务表: ${JSON.stringify([...stats.analysis.txnTable.values()])}`);
console.log(`脏页表: ${JSON.stringify([...stats.analysis.dirtyPageTable.entries()])}`);
console.log(`Redo 起点 LSN: ${stats.analysis.redoLSN}`);

console.log(`\n=== Redo ===`);
console.log(`历史重复，重放 ${stats.redoCount} 条记录（含崩溃时丢失的已提交写）`);

console.log(`\n=== Undo ===`);
console.log(`败者事务: [${stats.loserTxns.map((t) => 'T' + t).join(', ')}]`);
console.log(`写入 ${stats.clrCount} 条 CLR 补偿记录`);

console.log(`\n=== 恢复后磁盘最终值 ===`);
let ok = true;
for (const [pageId, offset, expected] of EXPECTED_FINAL) {
  const image = scenario.disk.read(pageId)!;
  const page = SparsePage.fromRLE(pageId, image.blocks, image.pageLSN);
  const actual = page.read(offset);
  page.dispose();
  const pass = actual === expected;
  ok &&= pass;
  console.log(`  p${pageId}[${offset}] = ${actual}（期望 ${expected}）${pass ? 'OK' : 'FAIL'}`);
}
console.log(`\n恢复结果: ${ok ? '全部正确' : '存在错误'}`);
console.log(`WAL 恢复后共 ${wal.length} 条记录（新增 ${wal.length - 18} 条 CLR/END）`);
pool.shutdown();
console.log(`泄漏检查: 存活页对象 = ${AllocationTracker.live}`);
