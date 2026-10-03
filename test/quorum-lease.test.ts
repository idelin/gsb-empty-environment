import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";
import { NotLeaderError } from "../src/lease-arbiter.ts";

const NODES = ["n1", "n2", "n3", "n4", "n5"];
const LEASE_MS = 1000;

function newCluster(): Cluster {
  return new Cluster(NODES, { leaseDurationMs: LEASE_MS });
}

test("获得多数派（Quorum）租约授权才能成为 Leader", () => {
  const c = newCluster();
  assert.equal(c.quorumSize(), 3); // 5 节点集群 Quorum = 3

  assert.equal(c.node("n1").campaign(LEASE_MS), true);
  assert.equal(c.node("n1").isLeader, true);
  assert.deepEqual(c.activeLeaders(), ["n1"]);

  // Leader 可正常服务写请求
  c.node("n1").write("x=1");
  assert.deepEqual(c.node("n1").log, ["x=1"]);
});

test("少数派分区无法凑齐 Quorum，竞选失败", () => {
  const c = newCluster();
  c.partitionInto([
    ["n1", "n2"],
    ["n3", "n4", "n5"],
  ]);
  // 2 节点分区中的候选者最多拿到 2 票 < Quorum(3)
  assert.equal(c.node("n1").campaign(LEASE_MS), false);
  assert.equal(c.node("n1").isLeader, false);
  // 3 节点分区可以选出 Leader
  assert.equal(c.node("n3").campaign(LEASE_MS), true);
  assert.deepEqual(c.activeLeaders(), ["n3"]);
});

test("验收边界：5 节点划分为 2+3 分区，多数派续租服务、少数派租约过期自动剥离", () => {
  const c = newCluster();

  // 1. 全连通时 n1 当选 Leader 并服务写请求
  assert.equal(c.node("n1").campaign(LEASE_MS), true);
  c.node("n1").write("before-partition");

  // 2. 对称分区：{n1,n2} | {n3,n4,n5}，Leader 位于少数派
  c.partitionInto([
    ["n1", "n2"],
    ["n3", "n4", "n5"],
  ]);

  // 3. 租约未过期前 n1 仍是 Leader，但已无法凑齐 Quorum 续租
  assert.equal(c.node("n1").renewLease(LEASE_MS), false);
  c.advance(LEASE_MS - 1);
  assert.equal(c.node("n1").isLeader, true);

  // 4. 少数派分区在旧租约过期前无法选出新 Leader（仲裁者不会重复授权）
  assert.equal(c.node("n3").campaign(LEASE_MS), false);

  // 5. 租约过期：n1 自动剥离主权限，不再响应写请求
  c.advance(1);
  assert.equal(c.node("n1").isLeader, false);
  assert.throws(() => c.node("n1").write("stale-write"), NotLeaderError);
  assert.throws(() => c.node("n1").write("stale-write"), /not leader/);
  assert.deepEqual(c.node("n1").log, ["before-partition"]); // 过期后的写未提交

  // 6. 多数派分区续租/重新当选并继续服务
  assert.equal(c.node("n3").campaign(LEASE_MS), true);
  c.node("n3").write("after-partition-1");
  c.advance(LEASE_MS / 2);
  assert.equal(c.node("n3").renewLease(LEASE_MS), true); // 3 票凑齐 Quorum，续租成功
  c.advance(LEASE_MS / 2 + 1); // 超过原租约窗口仍有效
  c.node("n3").write("after-partition-2");
  assert.deepEqual(c.node("n3").log, ["after-partition-1", "after-partition-2"]);

  // 7. 全程不存在脑裂：任意时刻至多一个有效 Leader
  assert.deepEqual(c.activeLeaders(), ["n3"]);
});

test("非对称分区：Leader 只能发不能收（或反向），同样无法续租而丧失主权限", () => {
  const c = newCluster();
  assert.equal(c.node("n1").campaign(LEASE_MS), true);

  // 非对称分区：n1 -> 其他节点链路保留，其他节点 -> n1 全部切断
  // （请求能发出但响应回不来，等价于收不到授权）
  for (const peer of ["n2", "n3", "n4", "n5"]) {
    c.setLink(peer, "n1", false);
  }
  // 其余节点之间仍全互联
  assert.equal(c.node("n1").renewLease(LEASE_MS), false);

  // 反向非对称：n1 收得到但发不出
  const c2 = newCluster();
  assert.equal(c2.node("n1").campaign(LEASE_MS), true);
  for (const peer of ["n2", "n3", "n4", "n5"]) {
    c2.setLink("n1", peer, false);
  }
  assert.equal(c2.node("n1").renewLease(LEASE_MS), false);

  // 两种情况下租约过期后都自动卸任，多数派一侧可正常接管
  c.advance(LEASE_MS);
  assert.equal(c.node("n1").isLeader, false);
  assert.equal(c.node("n3").campaign(LEASE_MS), true);
  assert.deepEqual(c.activeLeaders(), ["n3"]);
});

test("租约过期前仲裁者不重复授权；过期后新候选者才能当选", () => {
  const c = newCluster();
  assert.equal(c.node("n1").campaign(LEASE_MS), true);

  // 租约未过期：即使更高任期的候选者也无法夺走授权
  assert.equal(c.node("n2").campaign(LEASE_MS), false);
  assert.equal(c.node("n2").campaign(LEASE_MS), false); // 再次尝试（任期递增）仍被拒
  assert.equal(c.node("n1").isLeader, true);
  assert.deepEqual(c.activeLeaders(), ["n1"]);

  // 租约过期后：新候选者可以凑齐 Quorum 当选，保证活性
  c.advance(LEASE_MS);
  assert.equal(c.node("n1").isLeader, false);
  assert.equal(c.node("n2").campaign(LEASE_MS), true);
  assert.deepEqual(c.activeLeaders(), ["n2"]);
});

test("租约过期无需任何网络消息即自动生效（本地时钟驱动）", () => {
  const c = newCluster();
  assert.equal(c.node("n1").campaign(LEASE_MS), true);
  // 完全断网
  c.partitionInto([["n1"], ["n2"], ["n3"], ["n4"], ["n5"]]);
  c.advance(LEASE_MS);
  assert.equal(c.node("n1").isLeader, false);
  assert.throws(() => c.node("n1").write("x"), NotLeaderError);
});
