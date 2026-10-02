import { test } from "node:test";
import assert from "node:assert/strict";
import { PhiAccrualDetector } from "../src/phiAccrualDetector.ts";
import { LeaseManager } from "../src/leaseManager.ts";
import type { LeaseEvent } from "../src/leaseManager.ts";
import { FixedTimeoutDetector, generateArrivals } from "../src/simulatedNetwork.ts";

const THRESHOLD = 8;

test("验收场景：短时抖动不误判，真正断网果断回收租约", () => {
  const arrivals = generateArrivals({
    baseIntervalMs: 1000,
    jitterStdDevMs: 150,
    spikeEvery: 25,
    spikeDelayMs: 600,
    partitionAfterBeats: 150,
    seed: 7,
  });

  const lease = new LeaseManager(new PhiAccrualDetector(), {
    phiThreshold: THRESHOLD,
  });
  const fixed = new FixedTimeoutDetector(1400); // 对照组：固定超时
  const reclaimEvents: LeaseEvent[] = [];
  lease.onReclaim((e) => reclaimEvents.push(e));

  let fixedFalseAlarms = 0;

  // 阶段一：抖动期（含尖峰），逐步推进时间
  for (let i = 0; i < arrivals.length; i++) {
    const at = arrivals[i];
    lease.renew(at);
    fixed.heartbeat(at);
    if (i + 1 < arrivals.length) {
      const nextArrival = arrivals[i + 1];
      // 在两次心跳之间持续检查
      for (let t = at + 50; t < nextArrival; t += 50) {
        assert.equal(
          lease.check(t),
          "held",
          `t=${t} 抖动期内不应回收租约（间隔 ${nextArrival - at}ms）`,
        );
        if (!fixed.isAlive(t)) fixedFalseAlarms++;
      }
    }
  }
  assert.equal(lease.currentState, "held", "抖动期结束后租约仍应持有");
  assert.ok(
    fixedFalseAlarms > 0,
    "对照组（固定超时）应在尖峰抖动时产生误判，以证明场景有效",
  );
  console.log(`    抖动期：固定超时误判 ${fixedFalseAlarms} 次，φ 探测器误判 0 次`);

  // 阶段二：断网（分区），租约必须被回收，且回调只触发一次
  const lastAt = arrivals[arrivals.length - 1];
  let reclaimedAt = -1;
  for (let t = lastAt + 50; t <= lastAt + 10_000; t += 50) {
    if (lease.check(t) === "reclaimed") {
      reclaimedAt = t;
      break;
    }
  }
  assert.ok(reclaimedAt > 0, "断网后租约应被回收");
  assert.equal(reclaimEvents.length, 1, "回收回调应恰好触发一次");
  assert.ok(reclaimEvents[0].phi >= THRESHOLD);
  console.log(`    断网后 ${reclaimedAt - lastAt}ms 回收租约（φ=${reclaimEvents[0].phi.toFixed(2)}）`);

  // 回收后状态幂等
  assert.equal(lease.check(reclaimedAt + 60_000), "reclaimed");
  assert.equal(reclaimEvents.length, 1);
});
