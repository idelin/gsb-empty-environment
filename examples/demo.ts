/**
 * 运行样例：模拟“系统时间被回拨 1 小时”场景。
 *
 * 演示流程：
 *  1. 守护器以 100ms 间隔比对单调时钟与墙钟；
 *  2. 三个 worker 持有活跃租约；
 *  3. 第 1 秒时，模拟人为将系统时间回拨 1 小时；
 *  4. 守护器在下一次采样（下一瞬间）检测到双时钟撕裂，
 *     立即熔断：强制注销全部租约并降级为安全模式。
 *
 * 运行：npm run demo
 */
import { ManualClock } from "../src/clock.ts";
import { LeaseRegistry } from "../src/lease.ts";
import { ClockSkewGuardian, type SampleEvent, type TripEvent } from "../src/guardian.ts";

const HOUR_MS = 60 * 60 * 1000;
const INTERVAL_MS = 100;

// 演示用时钟：单调时钟始终稳定推进；墙钟在第 10 拍被回拨 1 小时。
const monotonic = new ManualClock(1_000_000);
const wall = new ManualClock(Date.now());
const registry = new LeaseRegistry();
const guardian = new ClockSkewGuardian(
  registry,
  { monotonic, wall },
  { intervalMs: INTERVAL_MS, maxSkewMs: 250 },
);

guardian.on("sample", (e: SampleEvent) => {
  console.log(
    `[sample] monoΔ=${e.monotonicDeltaMs}ms wallΔ=${e.wallDeltaMs}ms ` +
      `skew=${e.skewMs}ms cumulative=${e.cumulativeSkewMs}ms`,
  );
});

guardian.on("trip", (e: TripEvent) => {
  console.log("\n*** CIRCUIT BREAKER TRIPPED ***");
  console.log(`  reason        : ${e.reason}`);
  console.log(`  detail        : ${e.detail}`);
  console.log(`  revoked leases: ${e.revokedLeases.map((l) => `${l.id}(${l.holder})`).join(", ")}`);
  console.log(`  guardian state: ${guardian.state}`);
  console.log(`  registry mode : ${registry.isSafeMode ? "SAFE_MODE" : "NORMAL"}`);
});

// 三个 worker 持有活跃租约
for (const holder of ["worker-1", "worker-2", "worker-3"]) {
  const lease = registry.acquire(holder, wall.now(), 30_000);
  console.log(`[lease] acquired ${lease.id} by ${holder}`);
}
console.log(`[lease] active leases: ${registry.activeCount}\n`);

let tickCount = 0;
const timer = setInterval(() => {
  tickCount++;
  monotonic.advance(INTERVAL_MS);

  if (tickCount === 10) {
    console.log("\n>>> 模拟人为修改系统时间：墙钟回拨 1 小时 <<<\n");
    wall.advance(INTERVAL_MS - HOUR_MS);
  } else {
    wall.advance(INTERVAL_MS);
  }

  guardian.sample();

  if (guardian.tripped) {
    clearInterval(timer);
    try {
      registry.acquire("worker-4", wall.now());
    } catch (err) {
      console.log(`\n[lease] new acquire rejected: ${(err as Error).message}`);
    }
    console.log("[demo] 守护器已锁定在安全模式，等待人工确认时钟恢复后 reset()。");
  }
}, 60);
