import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock } from "../src/clock.ts";
import { LeaseRegistry, SafeModeError } from "../src/lease.ts";
import { ClockSkewGuardian, type TripEvent } from "../src/guardian.ts";

const HOUR_MS = 60 * 60 * 1000;

function setup(options = {}) {
  const monotonic = new ManualClock(1_000_000);
  const wall = new ManualClock(1_700_000_000_000);
  const registry = new LeaseRegistry();
  const guardian = new ClockSkewGuardian(
    registry,
    { monotonic, wall },
    { intervalMs: 100, maxSkewMs: 250, maxCumulativeSkewMs: 1000, maxTickGapMs: 5000, ...options },
  );
  const trips: TripEvent[] = [];
  guardian.on("trip", (e: TripEvent) => trips.push(e));
  return { monotonic, wall, registry, guardian, trips };
}

/** 正常推进一个采样周期（两个时钟同步前进 100ms）。 */
function tick(ctx: ReturnType<typeof setup>, n = 1): boolean {
  let tripped = false;
  for (let i = 0; i < n; i++) {
    ctx.monotonic.advance(100);
    ctx.wall.advance(100);
    tripped = ctx.guardian.sample() || tripped;
  }
  return tripped;
}

test("正常步调：双时钟同步推进时不熔断，租约保持活跃", () => {
  const ctx = setup();
  ctx.registry.acquire("worker-1", ctx.wall.now());
  ctx.registry.acquire("worker-2", ctx.wall.now());

  assert.equal(tick(ctx, 50), false);
  assert.equal(ctx.guardian.state, "NORMAL");
  assert.equal(ctx.registry.activeCount, 2);
  assert.equal(ctx.trips.length, 0);
});

test("验收边界：系统时间突然回拨 1 小时，下一瞬间熔断所有租约并降级安全模式", () => {
  const ctx = setup();
  ctx.registry.acquire("worker-1", ctx.wall.now());
  ctx.registry.acquire("worker-2", ctx.wall.now());
  ctx.registry.acquire("worker-3", ctx.wall.now());
  tick(ctx, 10); // 稳定运行一段时间
  assert.equal(ctx.registry.activeCount, 3);

  // 人为将系统时间向前回拨 1 小时（单调时钟不受影响，仍正常推进）
  ctx.monotonic.advance(100);
  ctx.wall.advance(100 - HOUR_MS);
  const tripped = ctx.guardian.sample();

  assert.equal(tripped, true, "必须在下一次采样立即熔断");
  assert.equal(ctx.guardian.state, "SAFE_MODE");
  assert.equal(ctx.registry.isSafeMode, true);
  assert.equal(ctx.registry.activeCount, 0, "所有活跃租约必须被强制注销");

  assert.equal(ctx.trips.length, 1);
  const trip = ctx.trips[0];
  assert.equal(trip.reason, "WALL_CLOCK_JUMP");
  assert.ok(trip.skewMs <= -HOUR_MS + 200, `skew 应约等于 -1h，实际 ${trip.skewMs}`);
  assert.equal(trip.revokedLeases.length, 3);
  assert.deepEqual(
    trip.revokedLeases.map((l) => l.holder).sort(),
    ["worker-1", "worker-2", "worker-3"],
  );

  // 安全模式下拒绝新租约
  assert.throws(() => ctx.registry.acquire("worker-4", ctx.wall.now()), SafeModeError);
});

test("NTP 大幅向前阶跃 1 小时同样触发熔断", () => {
  const ctx = setup();
  ctx.registry.acquire("worker-1", ctx.wall.now());
  tick(ctx, 5);

  ctx.monotonic.advance(100);
  ctx.wall.advance(100 + HOUR_MS);
  assert.equal(ctx.guardian.sample(), true);

  assert.equal(ctx.trips[0].reason, "WALL_CLOCK_JUMP");
  assert.ok(ctx.trips[0].skewMs >= HOUR_MS - 200);
  assert.equal(ctx.registry.activeCount, 0);
  assert.equal(ctx.guardian.state, "SAFE_MODE");
});

test("虚拟机时钟挂起：采样间隔异常（单调时钟单次推进超限）触发熔断", () => {
  const ctx = setup();
  ctx.registry.acquire("worker-1", ctx.wall.now());
  tick(ctx, 5);

  // VM 被挂起 30 秒：恢复后两个时钟同时跳过 30s
  ctx.monotonic.advance(30_000);
  ctx.wall.advance(30_000);
  assert.equal(ctx.guardian.sample(), true);

  assert.equal(ctx.trips[0].reason, "CLOCK_SUSPEND");
  assert.equal(ctx.registry.activeCount, 0);
  assert.equal(ctx.guardian.state, "SAFE_MODE");
});

test("小幅 NTP  slew 微调（每次 50ms）不触发瞬时熔断", () => {
  const ctx = setup();
  for (let i = 0; i < 10; i++) {
    ctx.monotonic.advance(100);
    ctx.wall.advance(150); // 每次快 50ms，低于 250ms 瞬时阈值
    assert.equal(ctx.guardian.sample(), false, `第 ${i} 次采样不应熔断`);
  }
  assert.equal(ctx.guardian.state, "NORMAL");
});

test("缓慢漂移持续累积最终超限熔断", () => {
  const ctx = setup();
  let tripped = false;
  for (let i = 0; i < 30 && !tripped; i++) {
    ctx.monotonic.advance(100);
    ctx.wall.advance(150); // 每次 +50ms，累积 1000ms 后超限
    tripped = ctx.guardian.sample();
  }
  assert.equal(tripped, true);
  assert.equal(ctx.trips[0].reason, "DRIFT_ACCUMULATED");
  assert.equal(ctx.guardian.state, "SAFE_MODE");
});

test("熔断后持续采样保持安全模式，不会自动恢复", () => {
  const ctx = setup();
  tick(ctx, 5);
  ctx.monotonic.advance(100);
  ctx.wall.advance(100 - HOUR_MS);
  assert.equal(ctx.guardian.sample(), true);

  // 时钟恢复同步步调，但守护器必须保持安全模式
  assert.equal(tick(ctx, 20), true);
  assert.equal(ctx.guardian.state, "SAFE_MODE");
  assert.throws(() => ctx.registry.acquire("w", ctx.wall.now()), SafeModeError);
});

test("人工 reset 后退出安全模式，可重新申请租约", () => {
  const ctx = setup();
  tick(ctx, 5);
  ctx.monotonic.advance(100);
  ctx.wall.advance(100 - HOUR_MS);
  ctx.guardian.sample();
  assert.equal(ctx.guardian.state, "SAFE_MODE");

  ctx.guardian.reset();
  assert.equal(ctx.guardian.state, "NORMAL");
  assert.equal(ctx.registry.isSafeMode, false);

  const lease = ctx.registry.acquire("worker-9", ctx.wall.now());
  assert.equal(ctx.registry.activeCount, 1);
  assert.ok(lease.id);

  // 复位后双时钟步调一致，不再误报
  assert.equal(tick(ctx, 20), false);
  assert.equal(ctx.guardian.state, "NORMAL");
});

test("租约注册表：正常申请与释放", () => {
  const registry = new LeaseRegistry();
  const a = registry.acquire("holder-a", 1000, 5000);
  const b = registry.acquire("holder-b", 1000);
  assert.equal(registry.activeCount, 2);
  assert.equal(a.expiresAt, 6000);
  assert.equal(b.expiresAt, null);
  assert.equal(registry.release(a.id), true);
  assert.equal(registry.release("nonexistent"), false);
  assert.equal(registry.activeCount, 1);
});
