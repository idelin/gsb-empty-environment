import assert from "node:assert/strict";
import { test } from "node:test";
import { ManualClock, SystemClock } from "../src/clock.ts";
import { FollowerNode } from "../src/follower.ts";
import { SAFE_READ_THRESHOLD_MS } from "../src/lease.ts";
import { LeaseNotConfirmedError, LeaseReadOptimizer } from "../src/primary.ts";
import { SimulatedTransport } from "../src/transport.ts";

const FOLLOWERS = ["f1", "f2", "f3", "f4", "f5"];
const LEASE_MS = 10_000;
const SKEW_MS = 10;

function makeCluster(clock: { now(): number }, transportLatencyMs = 1) {
  const transport = new SimulatedTransport({ latencyMs: transportLatencyMs, clock });
  for (const id of FOLLOWERS) {
    const follower = new FollowerNode(id, clock);
    transport.register(id, (req) => follower.handleHeartbeat(req));
  }
  const primary = new LeaseReadOptimizer({
    nodeId: "primary",
    followerIds: FOLLOWERS,
    clock,
    transport,
    leaseDurationMs: LEASE_MS,
    maxClockSkewMs: SKEW_MS,
  });
  return { transport, primary };
}

test("安全窗口内：本地读，零网络开销", async () => {
  const clock = new ManualClock(1_000_000);
  const { transport, primary } = makeCluster(clock);
  primary.write("k", "v1");
  primary.lease.grant(LEASE_MS);

  const result = await primary.read("k");
  assert.equal(result.value, "v1");
  assert.equal(result.source, "lease-local");
  assert.equal(result.networkCalls, 0);
  assert.equal(transport.networkCallCount, 0, "快速路径不得产生任何网络调用");
});

test("验收：10,000 次并发读全部命中安全窗口，零网络开销", async () => {
  const clock = new SystemClock();
  const { transport, primary } = makeCluster(clock);
  for (let i = 0; i < 100; i++) primary.write(`key-${i}`, `value-${i}`);
  primary.lease.grant(LEASE_MS); // 10s 租约，远超 100ms 阈值
  assert.ok(primary.lease.isInAbsoluteSafeWindow());

  const N = 10_000;
  const start = performance.now();
  const results = await Promise.all(
    Array.from({ length: N }, (_, i) => primary.read(`key-${i % 100}`)),
  );
  const elapsedMs = performance.now() - start;

  for (let i = 0; i < N; i++) {
    assert.equal(results[i].value, `value-${i % 100}`);
    assert.equal(results[i].source, "lease-local");
    assert.equal(results[i].networkCalls, 0);
  }
  assert.equal(transport.networkCallCount, 0, "1 万次读不得产生任何网络调用");
  assert.equal(primary.stats.localReads, N);
  const qps = Math.round(N / (elapsedMs / 1000));
  console.log(
    `  [perf] ${N} 次并发读耗时 ${elapsedMs.toFixed(1)}ms，吞吐 ≈ ${qps.toLocaleString()} reads/s，网络调用 0 次`,
  );
});

test("降级：剩余租约不足 100ms 时自动触发全量心跳确认", async () => {
  const clock = new ManualClock(1_000_000);
  const { transport, primary } = makeCluster(clock);
  primary.write("k", "v2");
  // 剩余租约 = 阈值(100ms) + skew(10ms) - 1ms → 扣除 skew 后不足 100ms
  primary.lease.grant(SAFE_READ_THRESHOLD_MS + SKEW_MS - 1);
  assert.ok(!primary.lease.isInAbsoluteSafeWindow());

  const result = await primary.read("k");
  assert.equal(result.value, "v2");
  assert.equal(result.source, "quorum-confirmed");
  assert.equal(result.networkCalls, FOLLOWERS.length, "降级路径须向全部从节点发心跳");
  assert.equal(transport.networkCallCount, FOLLOWERS.length);
  assert.ok(primary.lease.isInAbsoluteSafeWindow(), "心跳确认后租约应被续期");
});

test("边界：扣除时钟偏移后恰好 >= 100ms 时不降级", async () => {
  const clock = new ManualClock(1_000_000);
  const { transport, primary } = makeCluster(clock);
  primary.lease.grant(SAFE_READ_THRESHOLD_MS + SKEW_MS); // 恰好处于安全窗口边界
  assert.ok(primary.lease.isInAbsoluteSafeWindow());

  const result = await primary.read("k");
  assert.equal(result.source, "lease-local");
  assert.equal(transport.networkCallCount, 0);
});

test("时钟偏移计入安全窗口：忽略 skew 看似充足但扣除后不足 100ms 时降级", async () => {
  const clock = new ManualClock(1_000_000);
  const { transport, primary } = makeCluster(clock);
  // 名义剩余 105ms > 100ms，但扣除 skew=10ms 后仅 95ms → 必须降级
  primary.lease.grant(105);
  assert.ok(!primary.lease.isInAbsoluteSafeWindow());

  const result = await primary.read("k");
  assert.equal(result.source, "quorum-confirmed");
  assert.equal(transport.networkCallCount, FOLLOWERS.length);
});

test("租约过期且多数派不可达：拒绝读，绝不违反线性一致性", async () => {
  const clock = new ManualClock(1_000_000);
  const { transport, primary } = makeCluster(clock);
  primary.lease.grant(50); // 已不足阈值 → 走降级路径
  // 隔离 3/5 个从节点，多数派（4 票）无法达成
  transport.partition("f3");
  transport.partition("f4");
  transport.partition("f5");

  await assert.rejects(() => primary.read("k"), LeaseNotConfirmedError);
});

test("线性一致性：读永远返回最新已提交写", async () => {
  const clock = new ManualClock(1_000_000);
  const { primary } = makeCluster(clock);
  primary.lease.grant(LEASE_MS);
  primary.write("a", 1);
  assert.equal((await primary.read("a")).value, 1);
  primary.write("a", 2);
  assert.equal((await primary.read("a")).value, 2);
  // 写后立刻降级读，同样读到最新值
  clock.advance(LEASE_MS);
  assert.equal((await primary.read("a")).value, 2);
});
