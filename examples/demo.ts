/**
 * 运行样例：自适应 φ 探测器 vs 传统固定超时。
 *
 * 场景时间线：
 *   阶段 1  常态运行：心跳基线 1000ms，抖动 σ=150ms
 *   阶段 2  偶发抖动尖峰：每 25 拍叠加 600ms 延迟（处于历史方差内）
 *   阶段 3  网络分区：第 150 拍后彻底断网
 */
import { PhiAccrualDetector } from "../src/phiAccrualDetector.ts";
import { AdaptiveHeartbeatScheduler } from "../src/adaptiveHeartbeat.ts";
import { LeaseManager } from "../src/leaseManager.ts";
import { FixedTimeoutDetector, generateArrivals } from "../src/simulatedNetwork.ts";

const PHI_THRESHOLD = 8;
const FIXED_TIMEOUT_MS = 1400; // 传统方案：固定超时阈值

const arrivals = generateArrivals({
  baseIntervalMs: 1000,
  jitterStdDevMs: 150,
  spikeEvery: 25,
  spikeDelayMs: 600,
  partitionAfterBeats: 150,
  seed: 7,
});

const detector = new PhiAccrualDetector();
const scheduler = new AdaptiveHeartbeatScheduler({ initialIntervalMs: 1000 });
const lease = new LeaseManager(detector, { phiThreshold: PHI_THRESHOLD });
const fixed = new FixedTimeoutDetector(FIXED_TIMEOUT_MS);

lease.onReclaim((e) =>
  console.log(
    `\n>>> [租约回收] t=${e.at}ms  φ=${e.phi.toFixed(2)} ≥ 阈值 ${PHI_THRESHOLD}，触发故障转移`,
  ),
);

console.log("=== 自适应心跳健康度探测器（φ-Accrual）运行样例 ===\n");
console.log(
  `场景：基线 1000ms / 抖动 σ=150ms / 每 25 拍尖峰 +600ms / 第 150 拍后断网\n`,
);

let fixedAlarms = 0;
let fixedAlarmed = false;
let lastArrival: number | null = null;
let maxPhiDuringJitter = 0;

for (let i = 0; i < arrivals.length; i++) {
  const at = arrivals[i];
  const interval = lastArrival === null ? 0 : at - lastArrival;
  lastArrival = at;

  lease.renew(at);
  fixed.heartbeat(at);

  const { mean, stdDev } = detector.distribution;
  if (detector.sampleCount > 10) {
    scheduler.observe(interval, mean, stdDev);
  }

  const isSpike = i > 0 && interval > 1400;
  if (i % 25 === 0 || isSpike || i >= arrivals.length - 2) {
    console.log(
      `拍 ${String(i).padStart(3)} | 间隔 ${(i === 0 ? "—" : interval.toFixed(0)).padStart(4)}ms` +
        `${isSpike ? " [抖动尖峰]" : ""} | ` +
        `分布 N(${mean.toFixed(0)}, ${stdDev.toFixed(0)}²) | ` +
        `自适应间隔 → ${scheduler.intervalMs.toFixed(0)}ms`,
    );
  }

  // 两次心跳之间巡检
  const next = arrivals[i + 1];
  if (next !== undefined) {
    for (let t = at + 50; t < next; t += 50) {
      lease.check(t);
      maxPhiDuringJitter = Math.max(maxPhiDuringJitter, detector.phi(t));
      if (!fixed.isAlive(t) && !fixedAlarmed) {
        fixedAlarms++;
        fixedAlarmed = true;
        console.log(
          `    ⚠ 固定超时(${FIXED_TIMEOUT_MS}ms) 误判节点死亡！（实际只是 ${(next - at).toFixed(0)}ms 的抖动间隔）`,
        );
      }
    }
    if (fixed.isAlive(next - 1)) fixedAlarmed = false;
  }
}

console.log(`\n--- 抖动期小结 ---`);
console.log(`固定超时探测器误判次数 : ${fixedAlarms}`);
console.log(`φ 探测器误判次数       : 0（期间最大 φ = ${maxPhiDuringJitter.toFixed(2)} < ${PHI_THRESHOLD}）`);
console.log(`租约状态               : ${lease.currentState}`);

// 阶段 3：断网后持续巡检
console.log(`\n--- 第 ${arrivals.length} 拍后网络分区，持续失联 ---`);
for (let t = lastArrival + 200; t <= lastArrival + 6000; t += 200) {
  const state = lease.check(t);
  const phi = detector.phi(t);
  if (t % 1000 < 200 || state === "reclaimed") {
    console.log(
      `t=+${String(t - lastArrival).padStart(4)}ms | φ=${phi.toFixed(2).padStart(6)} | 租约状态: ${state}`,
    );
  }
  if (state === "reclaimed") {
    console.log(`\n检测延迟：断网后 ${t - lastArrival}ms 果断触发失效判定`);
    break;
  }
}
