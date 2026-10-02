import { TemporalKpiLedger } from "../src/temporal-kpi-ledger.ts";

const fmt = (n: number) => n.toFixed(4);

const ledger = new TemporalKpiLedger({
  cycleStartDay: 1,
  cycleEndDay: 91,
  signingSecret: "hr-strategy-2026-q1",
});

console.log("=== 初始战略版本（v1-v4，责任人 alice，第 1 天录入）===");
ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.6, validFrom: 1, validTo: 60, actor: "alice", reason: "Q1 初始战略：前半季侧重指标A", recordedAtDay: 1 });
ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.4, validFrom: 1, validTo: 60, actor: "alice", reason: "Q1 初始战略：前半季侧重指标A", recordedAtDay: 1 });
ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.4, validFrom: 60, validTo: 91, actor: "alice", reason: "Q1 初始战略：后半季侧重指标B", recordedAtDay: 1 });
ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.6, validFrom: 60, validTo: 91, actor: "alice", reason: "Q1 初始战略：后半季侧重指标B", recordedAtDay: 1 });

console.log("\n=== 录入季度实际绩效 ===");
for (let day = 1; day < 60; day++) {
  ledger.recordActual("A", day, 90);
  ledger.recordActual("B", day, 150);
}
for (let day = 60; day < 91; day++) {
  ledger.recordActual("A", day, 110);
  ledger.recordActual("B", day, 240);
}
console.log("A: [1,60) 日均 90 / [60,91) 日均 110（目标 100）");
console.log("B: [1,60) 日均 150 / [60,91) 日均 240（目标 200）");

const before = ledger.computeScore();
console.log(`\n=== 调整前（快照 v4）季度末加权得分: ${fmt(before.totalScore)} ===`);
for (const c of before.contributions) {
  console.log(
    `  ${c.kpiId} [${c.validFrom},${c.validTo}) 权重${c.weight} 达成率${fmt(c.attainment!)} 区间占比${fmt(c.segmentFraction)} 得分${fmt(c.points)} (来源v${c.sourceSeq})`,
  );
}

console.log("\n=== 第 45 天追溯调整（v5-v6，责任人 bob）：全季度 A=0.4 / B=0.6 ===");
const v5 = ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.4, validFrom: 1, validTo: 91, actor: "bob", reason: "战略重心转移至指标B，追溯覆盖全季度", recordedAtDay: 45 });
const v6 = ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.6, validFrom: 1, validTo: 91, actor: "bob", reason: "战略重心转移至指标B，追溯覆盖全季度", recordedAtDay: 45 });

const after = ledger.computeScore();
console.log(`\n=== 调整后（快照 v6）季度末加权得分: ${fmt(after.totalScore)} ===`);
for (const c of after.contributions) {
  console.log(
    `  ${c.kpiId} [${c.validFrom},${c.validTo}) 权重${c.weight} 达成率${fmt(c.attainment!)} 区间占比${fmt(c.segmentFraction)} 得分${fmt(c.points)} (来源v${c.sourceSeq})`,
  );
}

const diff = ledger.diffScores(4, 6);
console.log("\n=== 版本影响分析（v4 -> v6）===");
console.log(`调整前得分: ${fmt(diff.beforeScore)}`);
console.log(`调整后得分: ${fmt(diff.afterScore)}`);
console.log(`实际得分差: ${diff.delta >= 0 ? "+" : ""}${fmt(diff.delta)}`);
for (const k of diff.perKpi) {
  console.log(`  指标${k.kpiId}: ${fmt(k.before)} -> ${fmt(k.after)} (${k.delta >= 0 ? "+" : ""}${fmt(k.delta)})`);
}

console.log("\n=== 变更责任人版本签名 ===");
for (const v of diff.impactingVersions) {
  console.log(`  v${v.seq} ${v.kpiId} 权重${v.weight} [${v.validFrom},${v.validTo}) 责任人=${v.actor} 记录日=第${v.recordedAtDay}天`);
  console.log(`     签名=${v.signature}`);
  console.log(`     签名校验=${ledger.verifyVersion(v) ? "通过" : "失败"}`);
}

console.log("\n=== 历史快照推演：以 v2 视角重算（仅前半季初始战略可见）===");
const snapshotV2 = ledger.computeScore(2);
console.log(`v2 快照得分: ${fmt(snapshotV2.totalScore)}`);
for (const w of snapshotV2.warnings) console.log(`  警告: ${w}`);
