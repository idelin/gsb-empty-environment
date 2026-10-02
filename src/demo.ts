import { createHash } from 'node:crypto';
import { LsmEngine } from './lsmEngine.ts';
import { LsmDiagnosticsMonitor } from './monitor.ts';

/** 生成随机散列 Key（模拟真实负载中的均匀散列键） */
function randomHashKey(i: number): string {
  return createHash('sha256').update(`key-${i}-${Math.random()}`).digest('hex').slice(0, 32);
}

function line(title: string): void {
  console.log(`\n=== ${title} ===`);
}

const engine = new LsmEngine({
  memtableFlushThreshold: 64,
  compactionTrigger: 100, // 演示期间不自动 compaction，便于观察读放大
  compactionFanIn: 2,
});
const monitor = new LsmDiagnosticsMonitor({
  windowSize: 100,
  readAmpThreshold: 2.0,
  bloomFpThreshold: 0.05,
  minSamples: 20,
});
monitor.attach(engine);
monitor.onAlert((a) => {
  console.log(`  [ALERT:${a.severity}] ${a.message}`);
  console.log(`  [建议] ${a.suggestion}`);
});

// 1. 写入 512 个随机散列 Key -> flush 出 8 张 SSTable
line('写入 512 个随机散列 Key（8 张 SSTable）');
const keys: string[] = [];
for (let i = 0; i < 512; i++) {
  const k = randomHashKey(i);
  keys.push(k);
  engine.put(k, `value-${i}`);
}
console.log(`  SSTable 数量: ${engine.sstableCount}`);

// 2. 基线：Bloom Filter 全部开启
line('阶段一：Bloom Filter 开启，执行 200 次 Get');
for (let i = 0; i < 200; i++) engine.get(keys[Math.floor(Math.random() * keys.length)]);
let s = monitor.stats();
console.log(`  读放大 = ${s.readAmplification.toFixed(2)}，Bloom 误判率 = ${(s.bloomFpRate * 100).toFixed(2)}%`);

// 3. 故障注入：故意关闭部分 Bloom Filter
line('阶段二：故意关闭部分 SSTable 的 Bloom Filter');
const disabled = engine.disableBloomOnSome((_, idx) => idx <= 6); // 仅保留最旧一张
console.log(`  已关闭 ${disabled}/${engine.sstableCount} 张表的 Bloom Filter`);
monitor.reset();
for (let i = 0; i < 200; i++) engine.get(keys[Math.floor(Math.random() * keys.length)]);
s = monitor.stats();
console.log(`  读放大 = ${s.readAmplification.toFixed(2)}，Bloom 误判率 = ${(s.bloomFpRate * 100).toFixed(2)}%`);
console.log(`  监控告警数 = ${monitor.recentAlerts.length}`);

// 4. 采纳建议：重建 Filter + 提高 Compaction 权重
line('阶段三：采纳建议 —— 重建 Filter 并提高 Compaction 权重');
engine.rebuildAllFilters();
engine.compactionFanIn = 8;
while (engine.sstableCount > 1) engine.compact();
console.log(`  Compaction 后 SSTable 数量: ${engine.sstableCount}`);
monitor.reset();
for (let i = 0; i < 200; i++) engine.get(keys[Math.floor(Math.random() * keys.length)]);
s = monitor.stats();
console.log(`  读放大回落至 ${s.readAmplification.toFixed(2)}，告警数 = ${monitor.recentAlerts.length}`);
