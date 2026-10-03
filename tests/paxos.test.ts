import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/paxos/cluster.ts";
import { NO_OP } from "../src/paxos/types.ts";

test("选举：候选者获得多数派 promise 后成为领导者", () => {
  const c = new Cluster(5);
  assert.equal(c.elect(0), true);
  assert.equal(c.nodes[0].isLeader, true);
  assert.equal(c.nodes[0].leaseValid, true);
});

test("选举：无法获得多数派时不能成为领导者（少数分区）", () => {
  const c = new Cluster(5);
  c.network.partition([[0, 1], [2, 3, 4]]);
  assert.equal(c.elect(0), false);
  assert.equal(c.nodes[0].isLeader, false);
  assert.equal(c.elect(2), true);
});

test("租约有效时绕过 Phase 1 直接提交连续日志（无空洞）", () => {
  const c = new Cluster(3);
  c.elect(0);
  const before = c.network.sentCount;
  const i1 = c.propose(0, "a");
  const i2 = c.propose(0, "b");
  const i3 = c.propose(0, "c");
  assert.deepEqual([i1, i2, i3], [1, 2, 3]); // 连续下标
  // 每次提案仅 Phase 2：3 条 accept + 2 条 accepted + 2 条 commit（自票不计网络）
  const perProposal = c.network.sentCount - before;
  assert.equal(perProposal, 3 * (2 + 2 + 2));
  for (const n of c.nodes) {
    assert.deepEqual(n.committedEntries().map((e) => e.value), ["a", "b", "c"]);
    assert.equal(n.committedUpTo, 3);
  }
});

test("租约过期后提案会重新触发 Phase 1 并缓冲补交", () => {
  const c = new Cluster(3, 500);
  c.elect(0);
  c.propose(0, "a");
  c.advanceTime(600); // 租约过期
  assert.equal(c.nodes[0].leaseValid, false);
  const idx = c.propose(0, "b"); // 触发重新竞选，值被缓冲
  assert.equal(idx, -1);
  c.network.flush();
  assert.equal(c.nodes[0].isLeader, true);
  for (const n of c.nodes) {
    assert.deepEqual(n.committedEntries().map((e) => e.value), ["a", "b"]);
  }
});

test("非领导者提案返回 -1", () => {
  const c = new Cluster(3);
  c.elect(0);
  assert.equal(c.nodes[1].propose("x"), -1);
});

test("旧 ballot 的 accept 被拒绝（已承诺更高 ballot）", () => {
  const c = new Cluster(5);
  c.elect(0);
  c.propose(0, "a");
  // node0 下位：node1 以更高 term 当选
  c.elect(1);
  assert.equal(c.nodes[1].isLeader, true);
  // node0 的过期提案不会生效
  assert.equal(c.nodes[0].propose("stale"), -1);
  c.propose(1, "b");
  for (const n of c.nodes) {
    assert.deepEqual(n.committedEntries().map((e) => e.value), ["a", "b"]);
  }
});

test("心跳维持跟随者租约并同步提交水位", () => {
  const c = new Cluster(3, 300);
  c.elect(0);
  c.propose(0, "a");
  c.advanceTime(200);
  c.nodes[0].heartbeat();
  c.network.flush();
  // 注意：heartbeat 要求领导者租约有效；200 < 300 仍有效
  assert.equal(c.nodes[1].followerLeaseValid, true);
  assert.equal(c.nodes[1].committedUpTo, 1);
});

test("单机集群：自票即多数派，立即提交", () => {
  const c = new Cluster(1);
  assert.equal(c.elect(0), true);
  const idx = c.propose(0, "solo");
  assert.equal(idx, 1);
  assert.equal(c.nodes[0].committedUpTo, 1);
  assert.deepEqual(c.nodes[0].committedEntries().map((e) => e.value), ["solo"]);
});

test("丢包网络下多数派仍可提交，少数派通过 sync 补齐", () => {
  const c = new Cluster(5);
  c.elect(0);
  // 断开 node4 的入向链路，使其收不到 accept/commit
  for (const id of [0, 1, 2, 3]) c.network.setLink(id, 4, false);
  c.propose(0, "a");
  c.propose(0, "b");
  assert.equal(c.nodes[0].committedUpTo, 2);
  assert.equal(c.nodes[4].committedUpTo, 0);
  // 恢复链路，心跳触发 sync 补齐
  for (const id of [0, 1, 2, 3]) c.network.setLink(id, 4, true);
  c.nodes[0].heartbeat();
  c.network.flush();
  assert.equal(c.nodes[4].committedUpTo, 2);
  assert.deepEqual(c.nodes[4].committedEntries().map((e) => e.value), ["a", "b"]);
});

test("已提交日志在多次领导者更替后保持一致（安全性）", () => {
  const c = new Cluster(5);
  c.elect(0);
  c.propose(0, "v1");
  c.propose(0, "v2");
  c.elect(2); // 更高 term 的领导者
  c.propose(2, "v3");
  c.elect(4);
  c.propose(4, "v4");
  const values = c.convergedCommittedValues();
  assert.deepEqual(values, ["v1", "v2", "v3", "v4"]);
  for (const n of c.nodes) {
    const committed = n.committedEntries();
    for (let i = 1; i <= n.committedUpTo; i++) {
      assert.ok(n.log.get(i)?.committed, `node${n.id} hole at ${i}`);
    }
    assert.equal(committed.length, n.committedUpTo);
  }
  assert.notEqual(values.includes(NO_OP), true);
});
