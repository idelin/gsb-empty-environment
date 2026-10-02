import { test } from "node:test";
import assert from "node:assert/strict";
import { PhiAccrualDetector } from "../src/phiAccrualDetector.ts";
import { generateArrivals } from "../src/simulatedNetwork.ts";

const THRESHOLD = 8;

test("预热期（样本不足）不判定失效，φ 恒为 0", () => {
  let now = 0;
  const det = new PhiAccrualDetector({ now: () => now, warmupSamples: 10 });
  for (let i = 0; i < 9; i++) {
    now += 1000;
    det.heartbeat();
  }
  now += 60_000; // 一分钟没有心跳，但样本不足
  assert.equal(det.phi(), 0);
  assert.equal(det.isAlive(THRESHOLD), true);
});

test("φ 随等待时间单调增长", () => {
  let now = 0;
  const det = new PhiAccrualDetector({ now: () => now });
  // 950/1050 交替，形成 σ≈50ms 的分布
  for (let i = 0; i < 200; i++) {
    now += i % 2 === 0 ? 950 : 1050;
    det.heartbeat();
  }
  const phis = [900, 1000, 1050, 1100, 1200, 1300].map((wait) =>
    det.phi(now + wait),
  );
  for (let i = 1; i < phis.length; i++) {
    assert.ok(phis[i] > phis[i - 1], `φ 应单调递增: ${phis}`);
  }
});

test("常态抖动 + 偶发尖峰（正常方差内）不触发误警", () => {
  // 基线 1000ms，抖动 σ=150ms，每 25 拍叠加 600ms 尖峰
  const arrivals = generateArrivals({
    baseIntervalMs: 1000,
    jitterStdDevMs: 150,
    spikeEvery: 25,
    spikeDelayMs: 600,
    seed: 7,
  });
  const det = new PhiAccrualDetector();
  let maxPhi = 0;
  for (let i = 0; i < arrivals.length; i++) {
    det.heartbeat(arrivals[i]);
    if (i + 1 < arrivals.length) {
      // 在下次心跳到达前 1ms 检查（最坏时刻）
      const phi = det.phi(arrivals[i + 1] - 1);
      maxPhi = Math.max(maxPhi, phi);
      assert.ok(
        phi < THRESHOLD,
        `第 ${i} 拍 φ=${phi.toFixed(2)} 不应超过阈值 ${THRESHOLD}`,
      );
    }
  }
  assert.ok(maxPhi > 0, "应当真的计算过 φ");
  console.log(`    抖动期间最大 φ = ${maxPhi.toFixed(2)}（阈值 ${THRESHOLD}）`);
});

test("真正断网时 φ 果断越过阈值", () => {
  const arrivals = generateArrivals({
    baseIntervalMs: 1000,
    jitterStdDevMs: 150,
    spikeEvery: 25,
    spikeDelayMs: 600,
    partitionAfterBeats: 150,
    seed: 7,
  });
  const det = new PhiAccrualDetector();
  for (const at of arrivals) det.heartbeat(at);

  const lastAt = arrivals[arrivals.length - 1];
  // 刚断网不久：仍在正常方差内，不误判
  assert.ok(det.phi(lastAt + 1200) < THRESHOLD);
  // 持续失联：φ 必须越过阈值
  const phiLater = det.phi(lastAt + 5000);
  assert.ok(phiLater >= THRESHOLD, `断网 5s 后 φ=${phiLater.toFixed(2)} 应 >= ${THRESHOLD}`);
  assert.equal(det.isAlive(THRESHOLD, lastAt + 5000), false);

  // 找到越界时刻，验证检测延迟有限（远小于固定超时的保守配置）
  let detectedAt = -1;
  for (let t = lastAt; t <= lastAt + 10_000; t += 10) {
    if (det.phi(t) >= THRESHOLD) {
      detectedAt = t;
      break;
    }
  }
  assert.ok(detectedAt > 0, "应当能检测到失效");
  console.log(`    断网后 ${detectedAt - lastAt}ms 触发失效判定`);
});

test("minStdDeviation 防止零方差时 φ 爆炸", () => {
  let now = 0;
  const det = new PhiAccrualDetector({ now: () => now, minStdDeviationMs: 10 });
  for (let i = 0; i < 50; i++) {
    now += 1000; // 完全等间隔，方差为 0
    det.heartbeat();
  }
  const phi = det.phi(now + 1010);
  assert.ok(Number.isFinite(phi), "方差为 0 时 φ 仍应有限");
});
