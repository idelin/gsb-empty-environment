import { TableStore } from './store.ts';
import { ChecksumIndex } from './checksumIndex.ts';
import { Inspector } from './inspector.ts';
import type { Row } from './rowHash.ts';

/** 可复现的伪随机数生成器。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rand = mulberry32(20261002);
const INITIAL_ROWS = 200_000;
const CHANGE_BATCHES = 20;
const BATCH_SIZE = 5_000; // 20 批 × 5000 = 10 万条持续变更

// ── 搭建源库 / 目标库 / 校验和索引 / 巡检器 ─────────────────────
const sourceStore = new TableStore('source-db');
const targetStore = new TableStore('target-db');
const sourceIndex = new ChecksumIndex('source', { partitionSize: 4096, bucketSize: 256 });
const targetIndex = new ChecksumIndex('target', { partitionSize: 4096, bucketSize: 256 });
sourceIndex.track(sourceStore);
targetIndex.track(targetStore);
const inspector = new Inspector(sourceIndex, targetIndex);

function makeRow(id: number): Row {
  return {
    id,
    name: `user_${id}`,
    balance: Math.floor(rand() * 1_000_000) / 100,
    email: `user_${id}@example.com`,
    updatedAt: 1_790_000_000_000 + Math.floor(rand() * 1_000_000),
  };
}

// ── 阶段 1：初始全量同步 20 万行 ────────────────────────────────
console.log(`[1] 初始全量同步 ${INITIAL_ROWS.toLocaleString()} 行 ...`);
let t0 = performance.now();
for (let id = 1; id <= INITIAL_ROWS; id++) {
  const row = makeRow(id);
  sourceStore.upsert(row);
  targetStore.upsert({ ...row }); // 模拟同步链路复制到目标库
}
console.log(`    完成，耗时 ${(performance.now() - t0).toFixed(1)} ms`);

let report = inspector.patrol();
console.log(`    巡检 #${report.cycle}: ${report.consistent ? '✅ 两端一致' : '❌ 不一致'}`
  + `（比对 ${report.partitionsChecked} 个分区，耗时 ${report.durationMs.toFixed(2)} ms）`);

// ── 阶段 2：变更持续流动，巡检器逐周期比对 ──────────────────────
console.log(`\n[2] 持续变更流：${CHANGE_BATCHES} 批 × ${BATCH_SIZE.toLocaleString()} 条`
  + `（共 ${(CHANGE_BATCHES * BATCH_SIZE).toLocaleString()} 条变更），每批后巡检一次`);

const tamperId = 137_777;   // 将被悄悄篡改的目标行主键
const tamperAtBatch = 12;   // 在第 12 批变更期间动手脚

for (let batch = 1; batch <= CHANGE_BATCHES; batch++) {
  for (let i = 0; i < BATCH_SIZE; i++) {
    const id = 1 + Math.floor(rand() * INITIAL_ROWS);
    const row = makeRow(id);
    sourceStore.upsert(row);
    targetStore.upsert({ ...row }); // 正常复制
    if (rand() < 0.02) {          // 少量删除
      sourceStore.delete(id);
      targetStore.delete(id);
    }
  }

  // ── 人为篡改：绕过同步链路，直接悄悄修改目标库某行单个字段 ──
  if (batch === tamperAtBatch) {
    const victim = targetStore.get(tamperId)!;
    targetStore.upsert({ ...victim, balance: (victim.balance as number) + 0.01 });
    console.log(`\n    😈 [第 ${batch} 批] 已悄悄篡改目标库 id=${tamperId} 的 balance 字段（+0.01）`);
  }

  report = inspector.patrol();
  if (report.consistent) {
    console.log(`    巡检 #${report.cycle} (批次 ${batch}): ✅ 一致`
      + `（${report.partitionsChecked} 分区，${report.durationMs.toFixed(2)} ms）`);
  } else {
    console.log(`    巡检 #${report.cycle} (批次 ${batch}): 🚨 发现不一致！`
      + `耗时 ${report.durationMs.toFixed(2)} ms`);
    console.log(`      失配分区: [${report.mismatchedPartitions.join(', ')}]`
      + `（下钻仅逐行比对 ${report.rowsScanned} 行 / 全量 ${sourceIndex.rowCount.toLocaleString()} 行）`);
    for (const d of report.diffs) {
      const kindText = { tampered: '数据被篡改', missing_in_target: '目标库丢数', extra_in_target: '目标库多出数据' }[d.kind];
      console.log(`      定位主键 id=${d.id}  类型=${kindText}`);
      console.log(`        source 行校验和: ${d.sourceChecksum ?? '(无此行)'}`);
      console.log(`        target 行校验和: ${d.targetChecksum ?? '(无此行)'}`);
      console.log(`        源库 balance=${sourceStore.get(d.id)?.balance}  目标库 balance=${targetStore.get(d.id)?.balance}`);
    }
  }
}

// ── 阶段 3：修复后复巡 ──────────────────────────────────────────
console.log(`\n[3] 将源库正确数据重新同步到目标库，复巡验证`);
const fixed = sourceStore.get(tamperId)!;
targetStore.upsert({ ...fixed });
report = inspector.patrol();
console.log(`    巡检 #${report.cycle}: ${report.consistent ? '✅ 两端恢复一致' : '❌ 仍不一致'}`
  + `（耗时 ${report.durationMs.toFixed(2)} ms）`);
