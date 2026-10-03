import { buildScenario, mulberry32, sampleKeys } from './scenario.ts';
import { LsmDiagnosticsMonitor } from './monitor.ts';
import type { Alert } from './monitor.ts';

type Snapshot = ReturnType<LsmDiagnosticsMonitor['snapshot']>;

function printSnapshot(label: string, snap: Snapshot, phaseAvg: number): void {
  console.log(
    `  [${label}] 全阶段平均读放大=${phaseAvg.toFixed(2)}  ` +
      `监控窗口(最近${snap.samples}次): 读放大=${snap.avgReadAmplification.toFixed(2)}  ` +
      `Bloom误判率=${(snap.falsePositiveRate * 100).toFixed(2)}%`,
  );
}

function printAlert(alert: Alert): void {
  console.log(`  ⚠️  [${alert.level.toUpperCase()}] ${alert.code}: ${alert.message}`);
  console.log(`      👉 ${alert.suggestion}`);
}

const QUERIES_PER_PHASE = 3000;

console.log('=== LSM 读放大 / Bloom Filter 误判率监控演示 ===\n');

const { engine, monitor, allKeys } = buildScenario();
console.log(`写入 ${allKeys.length} 个随机散列 Key，形成 ${engine.tableCount} 张 L0 SSTable（全部启用 Bloom Filter）\n`);

// ---- 阶段 A：Bloom Filter 全部启用 ----
console.log(`阶段 A：Bloom Filter 全部启用，执行 ${QUERIES_PER_PHASE} 次随机 Get …`);
const rngA = mulberry32(1);
let readsA = 0;
for (const key of sampleKeys(allKeys, QUERIES_PER_PHASE, rngA)) {
  readsA += engine.getWithTrace(key).trace.sstablesRead;
  for (const a of engine.drainAlerts()) printAlert(a);
}
printSnapshot('阶段A', monitor.snapshot(), readsA / QUERIES_PER_PHASE);

// ---- 故障注入：故意关闭部分 Bloom Filter ----
const disabled = engine.getTables().slice(2); // 保留最新 2 张，关闭最旧 7 张
for (const t of disabled) t.disableBloomFilter();
console.log(`\n>>> 故障注入：故意关闭 ${disabled.length}/${engine.tableCount} 张 SSTable 的 Bloom Filter\n`);

// ---- 阶段 B：部分 Bloom Filter 被关闭 ----
console.log(`阶段 B：同样负载再执行 ${QUERIES_PER_PHASE} 次随机 Get …`);
const rngB = mulberry32(2);
let readsB = 0;
let alertAtQuery = -1;
for (let i = 0; i < QUERIES_PER_PHASE; i++) {
  readsB += engine.getWithTrace(allKeys[Math.floor(rngB() * allKeys.length)]).trace.sstablesRead;
  for (const a of engine.drainAlerts()) {
    if (alertAtQuery < 0) alertAtQuery = i + 1;
    printAlert(a);
  }
}
if (alertAtQuery > 0) console.log(`  （监控在阶段 B 第 ${alertAtQuery} 次查询时即时捕捉到指标跃升）`);
printSnapshot('阶段B', monitor.snapshot(), readsB / QUERIES_PER_PHASE);

// ---- 处置：按告警建议提高 Compaction 权重（执行全量合并）并重建 Filter ----
console.log('\n>>> 处置：按建议执行 Compaction 合并 SSTable，并重建 Bloom Filter …\n');
engine.compactAll();
console.log(`  Compaction 完成：SSTable 数量=${engine.tableCount}，被关闭的 Filter=${engine.disabledFilterCount}`);

console.log(`\n阶段 C：恢复后再执行 ${QUERIES_PER_PHASE} 次随机 Get …`);
const rngC = mulberry32(3);
let readsC = 0;
for (const key of sampleKeys(allKeys, QUERIES_PER_PHASE, rngC)) {
  readsC += engine.getWithTrace(key).trace.sstablesRead;
  engine.drainAlerts();
}
printSnapshot('阶段C', monitor.snapshot(), readsC / QUERIES_PER_PHASE);

console.log('\n=== 演示结束 ===');
