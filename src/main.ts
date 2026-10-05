import { directedMigrationTest } from "./migration-test.ts";
import { runScenario } from "./scenario.ts";
import type { ScenarioParams, ScenarioResult } from "./scenario.ts";
import { assert } from "./assert.ts";

const T_MAX = 200_000;

const scenarios: ScenarioParams[] = [
  // 基线：中等丢包与抖动，A、B 先后迁移
  { seed: 11, messages: 200, aMigrateAt: 600, bMigrateAt: 900, drainMs: 500, ackDelay: 25,
    path0: { latency: 50, jitter: 30, lossRate: 0.08 }, path1: { latency: 40, jitter: 20, lossRate: 0.05 }, tMax: T_MAX },
  // 高丢包，迫使大量 RTO 与快重传
  { seed: 22, messages: 200, aMigrateAt: 400, bMigrateAt: 1200, drainMs: 300, ackDelay: 25,
    path0: { latency: 60, jitter: 50, lossRate: 0.12 }, path1: { latency: 45, jitter: 30, lossRate: 0.10 }, tMax: T_MAX },
  // 同时迁移、长排空期、长延迟 ACK：旧链路迟到 ACK 最多
  { seed: 33, messages: 200, aMigrateAt: 1000, bMigrateAt: 1000, drainMs: 800, ackDelay: 40,
    path0: { latency: 50, jitter: 10, lossRate: 0.05 }, path1: { latency: 50, jitter: 10, lossRate: 0.05 }, tMax: T_MAX },
  // 新链路比旧链路更差（迁移到劣质路径）
  { seed: 44, messages: 200, aMigrateAt: 300, bMigrateAt: 500, drainMs: 400, ackDelay: 25,
    path0: { latency: 40, jitter: 20, lossRate: 0.05 }, path1: { latency: 90, jitter: 40, lossRate: 0.12 }, tMax: T_MAX },
  // 大抖动造成重度乱序，考验接收缓存与 SACK
  { seed: 55, messages: 200, aMigrateAt: 700, bMigrateAt: 1100, drainMs: 600, ackDelay: 60,
    path0: { latency: 30, jitter: 60, lossRate: 0.10 }, path1: { latency: 30, jitter: 60, lossRate: 0.08 }, tMax: T_MAX },
  // 更长消息流
  { seed: 66, messages: 500, aMigrateAt: 1500, bMigrateAt: 2200, drainMs: 500, ackDelay: 25,
    path0: { latency: 50, jitter: 30, lossRate: 0.08 }, path1: { latency: 40, jitter: 25, lossRate: 0.06 }, tMax: T_MAX },
];

function fmt(r: ScenarioResult): string {
  const s = (name: string, st: ScenarioResult["a"]): string =>
    `${name}[retx=${st.retx} rto=${st.rto} fast=${st.fastRetx} clamp=${st.clampedAcks} ` +
    `lateAck=${st.lateAcksAfterMigration} oldAdv=${st.oldPathAckAdvanced} ` +
    `hwSend=${st.highWaterSend} hwRecv=${st.highWaterRecv} dup=${st.dupData} drop=${st.recvDropped}]`;
  return `seed=${String(r.seed).padEnd(3)} t=${String(r.finishTime).padStart(6)}ms  ${s("A", r.a)}  ${s("B", r.b)}`;
}

function main(): void {
  directedMigrationTest();

  let totalLate = 0;
  let totalOldAdv = 0;
  let totalClamp = 0;
  for (const p of scenarios) {
    const r = runScenario(p);
    totalLate += r.a.lateAcksAfterMigration + r.b.lateAcksAfterMigration;
    totalOldAdv += r.a.oldPathAckAdvanced + r.b.oldPathAckAdvanced;
    totalClamp += r.a.clampedAcks + r.b.clampedAcks;
    console.log(`[scenario] ${fmt(r)}`);
  }

  // 迁移语义确实被触发：旧链路迟到 ACK 到达过、且实际推进过已发送前缀。
  assert(totalLate > 0, "no late old-path ACKs observed across scenarios");
  assert(totalOldAdv > 0, "late old-path ACKs never advanced the sent prefix");

  console.log(
    `\nALL CHECKS PASSED: ${scenarios.length} scenarios + directed test ` +
      `(lateOldPathAcks=${totalLate}, oldPathAdvanced=${totalOldAdv}, clampedAcks=${totalClamp})`,
  );
}

main();
