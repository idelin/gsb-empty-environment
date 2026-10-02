import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock, AdjustableClock } from "../src/clock.ts";
import { ClockDriftMonitor } from "../src/drift-monitor.ts";
import { LeaseManager, LeaseViolationError, FencingTokenIssuer } from "../src/lease-manager.ts";
import type { LeaseConfig } from "../src/lease-manager.ts";
import { FencedStore, StaleFencingTokenError } from "../src/fenced-store.ts";

function setup(overrides: Partial<LeaseConfig> = {}) {
  const monotonic = new ManualClock(0);
  const wall = new AdjustableClock(monotonic);
  const config: LeaseConfig = {
    leaseDurationMs: 10_000,
    maxClockDriftMs: 500,
    ...overrides,
  };
  const monitor = new ClockDriftMonitor(wall, monotonic, config.maxClockDriftMs);
  const lease = new LeaseManager(config, wall, monitor);
  return { monotonic, wall, monitor, lease, config };
}

test("安全写窗口 = TTL - 最大漂移 - RTT 余量", () => {
  const { lease } = setup();
  assert.equal(lease.safeWindowMs, 9_500);
  const withRtt = setup({ maxRttMs: 200 });
  assert.equal(withRtt.lease.safeWindowMs, 9_300);
});

test("主节点在 9.5 秒边界必须停止以主身份下发写操作", () => {
  const { monotonic, lease } = setup();
  const token = lease.acquire();
  assert.equal(token, 1);

  monotonic.advance(9_499);
  const receipt = lease.write("op-at-9499");
  assert.equal(receipt.fencingToken, 1);
  assert.equal(receipt.elapsedMs, 9_499);

  monotonic.advance(1);
  assert.equal(lease.canWrite(), false);
  assert.throws(() => lease.write("op-at-9500"), LeaseViolationError);

  const status = lease.status();
  assert.equal(status.role, "demoted");
  assert.equal(status.demotionReason, "safe-window-expired");
});

test("RTT 余量使主节点提前停止写入", () => {
  const { monotonic, lease } = setup({ maxRttMs: 200 });
  lease.acquire();

  monotonic.advance(9_299);
  assert.doesNotThrow(() => lease.write("op-at-9299"));

  monotonic.advance(1);
  assert.throws(() => lease.write("op-at-9300"), LeaseViolationError);
  assert.equal(lease.status().demotionReason, "safe-window-expired");
});

test("注入时钟前拨 1 秒突变：安全自检触发紧急提前降级", () => {
  const { monotonic, wall, lease } = setup();
  lease.acquire();

  monotonic.advance(8_000);
  assert.doesNotThrow(() => lease.write("op-before-jump"));
  assert.equal(lease.status().role, "primary");

  const demotions: string[] = [];
  lease.onDemote((reason) => demotions.push(reason));

  wall.adjust(1_000);

  assert.deepEqual(demotions, ["clock-drift-exceeded"]);
  const status = lease.status();
  assert.equal(status.role, "demoted");
  assert.equal(status.demotionReason, "clock-drift-exceeded");
  assert.ok(
    status.elapsedMs !== null && status.elapsedMs < lease.safeWindowMs + 1_000,
    "降级发生在名义安全期耗尽之前（提前降级）",
  );
  assert.throws(() => lease.write("op-after-jump"), LeaseViolationError);
});

test("漂移容忍内的小幅时钟调整不触发降级，但安全窗口相应收缩", () => {
  const { monotonic, wall, lease } = setup();
  lease.acquire();

  monotonic.advance(5_000);
  wall.adjust(300);
  assert.equal(lease.status().role, "primary");
  assert.doesNotThrow(() => lease.write("op-after-small-adjust"));

  monotonic.advance(4_199);
  assert.doesNotThrow(() => lease.write("op-at-9499-effective"));

  monotonic.advance(1);
  assert.throws(() => lease.write("op-at-9500-effective"), LeaseViolationError);
  assert.equal(lease.status().demotionReason, "safe-window-expired");
});

test("租约续期重置安全窗口，且只能在安全期内续期", () => {
  const { monotonic, lease } = setup();
  const token = lease.acquire();

  monotonic.advance(9_000);
  assert.equal(lease.renew(), token);

  monotonic.advance(9_400);
  assert.doesNotThrow(() => lease.write("op-after-renew"));

  monotonic.advance(100);
  assert.throws(() => lease.renew(), LeaseViolationError);
  assert.equal(lease.status().demotionReason, "safe-window-expired");
});

test("降级后重新获取租约会发放递增的 fencing token", () => {
  const { monotonic, wall, lease } = setup();
  assert.equal(lease.acquire(), 1);

  monotonic.advance(8_000);
  wall.adjust(1_000);
  assert.equal(lease.status().role, "demoted");
  assert.throws(() => lease.write("op-while-demoted"), LeaseViolationError);

  assert.equal(lease.acquire(), 2);
  assert.doesNotThrow(() => lease.write("op-with-new-epoch"));
});

test("fencing token 杜绝双主脑裂：旧主的写被存储层拒绝", () => {
  const issuer = new FencingTokenIssuer();
  const { monotonic, wall, lease, config } = setup({ tokenIssuer: issuer });
  const store = new FencedStore();

  const tokenA = lease.acquire();
  lease.write("A-op-1");
  store.write(tokenA, "k", "from-A");
  assert.equal(store.read("k"), "from-A");

  monotonic.advance(8_000);
  wall.adjust(1_000);
  assert.equal(lease.status().demotionReason, "clock-drift-exceeded");

  const wallB = new AdjustableClock(monotonic);
  const monitorB = new ClockDriftMonitor(wallB, monotonic, config.maxClockDriftMs);
  const leaseB = new LeaseManager(config, wallB, monitorB);
  const tokenB = leaseB.acquire();
  assert.ok(tokenB > tokenA);

  leaseB.write("B-op-1");
  store.write(tokenB, "k", "from-B");
  assert.equal(store.read("k"), "from-B");

  assert.throws(
    () => store.write(tokenA, "k", "stale-from-A"),
    StaleFencingTokenError,
  );
  assert.equal(store.read("k"), "from-B");
});
