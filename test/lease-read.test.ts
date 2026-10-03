import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock } from "../src/clock.ts";
import { LeaseReadOptimizer, NotPrimaryError } from "../src/primary.ts";
import { FollowerReplica } from "../src/follower.ts";

const LEASE = { ttlMs: 1000, safeWindowMs: 100, clockSkewBoundMs: 50 };

function makeCluster(followerCount = 5) {
  const clock = new ManualClock(1_000_000);
  const followers = Array.from({ length: followerCount }, (_, i) => new FollowerReplica(`f${i}`, 0));
  const primary = new LeaseReadOptimizer(LEASE, followers, clock);
  return { clock, followers, primary };
}

test("安全窗口内：本地读零网络开销", async () => {
  const { followers, primary } = makeCluster();
  primary.write("k1", "v1");
  await primary.acquireLease();
  const heartbeatsAfterGrant = followers.map((f) => f.getHeartbeatCount());

  const result = await primary.read("k1");

  assert.equal(result.path, "fast");
  assert.equal(result.value, "v1");
  assert.deepEqual(
    followers.map((f) => f.getHeartbeatCount()),
    heartbeatsAfterGrant,
    "安全窗口内读取不得产生任何网络心跳",
  );
  assert.equal(primary.getStats().networkRounds, 1, "仅租约获取产生一轮网络协商");
});

test("1 万次并发读：全部走快速路径，线性一致返回最新值", async () => {
  const { followers, primary } = makeCluster();
  primary.write("hot", "latest");
  await primary.acquireLease();
  const heartbeatsAfterGrant = followers.map((f) => f.getHeartbeatCount());

  const results = await Promise.all(Array.from({ length: 10_000 }, () => primary.read("hot")));

  assert.equal(results.length, 10_000);
  for (const r of results) {
    assert.equal(r.path, "fast");
    assert.equal(r.value, "latest");
  }
  assert.equal(primary.getStats().fastPathReads, 10_000);
  assert.equal(primary.getStats().networkRounds, 1);
  assert.deepEqual(
    followers.map((f) => f.getHeartbeatCount()),
    heartbeatsAfterGrant,
    "1 万次并发读零网络开销",
  );
});

test("剩余租约不足 100ms：自动降级为全量心跳确认", async () => {
  const { clock, followers, primary } = makeCluster();
  primary.write("k", "v");
  await primary.acquireLease();
  // 推进到剩余约 40ms（< 100ms 安全窗口下界）
  clock.advance(LEASE.ttlMs - LEASE.clockSkewBoundMs - 40);
  assert.ok(primary.leaseRemainingMs() < 100);

  const before = followers.map((f) => f.getHeartbeatCount());
  const result = await primary.read("k");

  assert.equal(result.path, "degraded");
  assert.equal(result.value, "v");
  assert.ok(
    followers.every((f, i) => f.getHeartbeatCount() > before[i]),
    "降级路径必须向全部从节点发起心跳确认",
  );
  assert.equal(primary.getStats().degradedReads, 1);
});

test("降级确认续租成功后：后续读恢复快速路径", async () => {
  const { clock, primary } = makeCluster();
  await primary.acquireLease();
  clock.advance(LEASE.ttlMs - LEASE.clockSkewBoundMs - 10);

  const degraded = await primary.read("k");
  assert.equal(degraded.path, "degraded");

  const fast = await primary.read("k");
  assert.equal(fast.path, "fast", "续租成功回到绝对安全窗口");
});

test("时钟漂移：本地有效期按 ε 收缩", async () => {
  const { clock, primary } = makeCluster();
  await primary.acquireLease();
  assert.equal(primary.leaseRemainingMs(), LEASE.ttlMs - LEASE.clockSkewBoundMs);
  // 推进到剩余 101ms：仍在安全窗口
  clock.advance(LEASE.ttlMs - LEASE.clockSkewBoundMs - 101);
  const fast = await primary.read("k");
  assert.equal(fast.path, "fast");
  // 再推进 2ms：剩余 99ms < 100ms，必须降级
  clock.advance(2);
  const degraded = await primary.read("k");
  assert.equal(degraded.path, "degraded");
});

test("法定多数丢失：拒绝读，绝不返回过期数据", async () => {
  const { clock, followers, primary } = makeCluster();
  primary.write("k", "stale-danger");
  await primary.acquireLease();
  clock.advance(LEASE.ttlMs - LEASE.clockSkewBoundMs - 10);
  followers[0].partition();
  followers[1].partition();
  followers[2].partition();

  await assert.rejects(() => primary.read("k"), NotPrimaryError);
});

test("少数派分区：多数确认仍成立，降级读正常返回", async () => {
  const { clock, followers, primary } = makeCluster();
  primary.write("k", "v");
  await primary.acquireLease();
  clock.advance(LEASE.ttlMs - LEASE.clockSkewBoundMs - 10);
  followers[0].partition();
  followers[1].partition();

  const result = await primary.read("k");
  assert.equal(result.path, "degraded");
  assert.equal(result.value, "v");
});
