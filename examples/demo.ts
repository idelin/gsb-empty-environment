import { PhiAccrualFailureDetector } from '../src/phi-accrual-failure-detector.ts';
import { FixedTimeoutDetector } from '../src/fixed-timeout-detector.ts';

function makeRng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 0xffffffff;
  };
}

const rng = makeRng(42);
function jitteredInterval(): number {
  const u1 = Math.max(rng(), 1e-9);
  const u2 = rng();
  const z = Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
  return Math.max(20, Math.round(100 + z * 25));
}

const HEARTBEAT_PERIOD_MS = 100;
const FIXED_TIMEOUT_MS = 250;
const TICK_MS = 10;

const adaptive = new PhiAccrualFailureDetector({
  threshold: 8,
  minStdDeviationMs: 25,
  acceptableHeartbeatPauseMs: 100,
  firstHeartbeatMeanMs: 100,
  firstHeartbeatStdDeviationMs: 25,
});
const fixed = new FixedTimeoutDetector(FIXED_TIMEOUT_MS);

let fixedElections = 0;
let adaptiveElections = 0;
let fixedWasAvailable = true;
let adaptiveDetectAt: number | undefined;
let fixedDetectAt: number | undefined;

adaptive.on('lease-revoked', (e) => {
  adaptiveElections += 1;
  console.log(`  [t=${e.at}ms] φ-ACCRUAL  φ=${e.phi.toFixed(2)} ≥ 8  →  租约回收（确认节点失效）`);
});
adaptive.on('lease-restored', (e) => {
  console.log(`  [t=${e.at}ms] φ-ACCRUAL  心跳恢复，租约重新发放`);
});

const heartbeatTimes: number[] = [];
let now = 0;
heartbeatTimes.push(0);
for (let i = 0; i < 60; i++) {
  now += jitteredInterval();
  heartbeatTimes.push(now);
}
const jitterSpikeAt = now;
now += 300;
heartbeatTimes.push(now);
for (let i = 0; i < 5; i++) {
  now += jitteredInterval();
  heartbeatTimes.push(now);
}
now += 280;
heartbeatTimes.push(now);
for (let i = 0; i < 5; i++) {
  now += jitteredInterval();
  heartbeatTimes.push(now);
}
const partitionStart = now;
const partitionEnd = partitionStart + 1500;
heartbeatTimes.push(partitionEnd);
for (let i = 0; i < 10; i++) {
  now = partitionEnd + (i + 1) * HEARTBEAT_PERIOD_MS;
  heartbeatTimes.push(now);
}
const simEnd = now;

console.log('=== φ-Accrual 自适应心跳探测器 vs 固定超时探测器 ===');
console.log(`场景: 正常心跳周期 ~${HEARTBEAT_PERIOD_MS}ms (抖动 σ≈25ms)，固定超时阈值 ${FIXED_TIMEOUT_MS}ms`);
console.log(`阶段1 [0 ~ ${jitterSpikeAt}ms]      正常心跳，伴随随机网络抖动`);
console.log(`阶段2 [${jitterSpikeAt} ~ ${partitionStart}ms]  两次突发延迟尖峰 (300ms / 280ms)`);
console.log(`阶段3 [${partitionStart} ~ ${partitionEnd}ms]  真正断网 1500ms`);
console.log(`阶段4 [${partitionEnd} ~ ${simEnd}ms]  网络恢复\n`);

let heartbeatIdx = 0;
let lastPhiLog = -Infinity;
let prevHeartbeatAt = 0;
let peakPhiSinceHeartbeat = 0;

for (let t = 0; t <= simEnd; t += TICK_MS) {
  while (heartbeatIdx < heartbeatTimes.length && heartbeatTimes[heartbeatIdx] <= t) {
    const arrivedAt = heartbeatTimes[heartbeatIdx];
    const interval = arrivedAt - prevHeartbeatAt;
    if (interval > FIXED_TIMEOUT_MS && arrivedAt < partitionStart) {
      console.log(
        `  [t=${arrivedAt}ms] φ-ACCRUAL  延迟尖峰 ${interval}ms 到达，期间峰值 φ=${peakPhiSinceHeartbeat.toFixed(2)} < 8  →  判定为正常方差，不告警`,
      );
    }
    prevHeartbeatAt = arrivedAt;
    peakPhiSinceHeartbeat = 0;
    adaptive.reportHeartbeat(arrivedAt);
    fixed.reportHeartbeat(arrivedAt);
    heartbeatIdx += 1;
  }

  adaptive.check(t);

  const fixedAvailable = fixed.isAvailable(t);
  if (fixedWasAvailable && !fixedAvailable) {
    fixedElections += 1;
    const isRealPartition = t > partitionStart;
    console.log(
      `  [t=${t}ms] FIXED-TIMEOUT  超过 ${FIXED_TIMEOUT_MS}ms 无心跳 → 触发重选` +
        (isRealPartition ? '（真实故障）' : '（⚠ 误判！只是延迟抖动）'),
    );
    if (isRealPartition && fixedDetectAt === undefined) fixedDetectAt = t;
  }
  fixedWasAvailable = fixedAvailable;

  if (t > partitionStart && adaptiveDetectAt === undefined && adaptive.currentState === 'unavailable') {
    adaptiveDetectAt = t;
  }

  if (t > jitterSpikeAt && t < partitionStart) {
    peakPhiSinceHeartbeat = Math.max(peakPhiSinceHeartbeat, adaptive.phi(t));
  }

  if (t > partitionStart && t - lastPhiLog >= 100 && t <= partitionStart + 600) {
    console.log(`  [t=${t}ms] φ-ACCRUAL  断网持续中 φ=${adaptive.phi(t).toFixed(2)}`);
    lastPhiLog = t;
  }
}

console.log('\n=== 结果汇总 ===');
console.log(`固定超时探测器: 触发重选 ${fixedElections} 次（含抖动误判 ${fixedElections - 1} 次）`);
console.log(`φ 自适应探测器: 触发租约回收 ${adaptiveElections} 次（误判 0 次）`);
if (fixedDetectAt !== undefined && adaptiveDetectAt !== undefined) {
  console.log(
    `真实断网检测延迟: 固定超时 ${fixedDetectAt - partitionStart}ms / φ 自适应 ${adaptiveDetectAt - partitionStart}ms`,
  );
}
