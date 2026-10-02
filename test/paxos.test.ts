/**
 * Multi-Paxos 共识日志引擎测试。
 * 运行：node --experimental-strip-types --test test/
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/paxos/cluster.ts";
import { NOOP } from "../src/paxos/types.ts";

test("三节点：选举与基本提交", () => {
  const c = new Cluster(3, 1000);
  const leader = c.elect("N0");
  assert.equal(leader.isLeader, true);
  leader.propose("set x=1");
  leader.propose("set y=2");
  assert.deepEqual(leader.committedEntries(), ["set x=1", "set y=2"]);
  for (const id of c.ids) {
    assert.deepEqual(c.node(id).committedEntries(), ["set x=1", "set y=2"]);
  }
  c.assertCommittedConsistent();
});

test("租约快速路径：租约内跳过 Phase 1，仅 Accept/Accepted", () => {
  const c = new Cluster(5, 1000);
  const leader = c.elect("N1");
  const preparesAfterElection = leader.preparesSent;
  for (let i = 0; i < 10; i++) leader.propose(`cmd-${i}`);
  // 租约有效期内：没有额外的 Prepare，每条提案仅一次 Phase 2 广播
  assert.equal(leader.preparesSent, preparesAfterElection);
  assert.equal(leader.acceptsSent, 10);
  assert.equal(leader.committedEntries().length, 10);
});

test("租约过期后重新执行 Phase 1", () => {
  const c = new Cluster(3, 100);
  const leader = c.elect("N0");
  leader.propose("a");
  const prepares = leader.preparesSent;
  c.tick(200); // 租约过期
  leader.propose("b");
  assert.equal(leader.preparesSent, prepares + 1);
  assert.deepEqual(leader.committedEntries(), ["a", "b"]);
});

test("少数派分区：旧 Leader 无法提交（无多数派）", () => {
  const c = new Cluster(5, 1000);
  const old = c.elect("N0");
  old.propose("v0");
  c.partition(["N0", "N1"], ["N2", "N3", "N4"]);
  old.propose("dirty"); // 只有 2 票，达不到 quorum=3
  assert.equal(old.committedEntries().length, 1);
  assert.equal(c.node("N1").committedEntries().length, 1);
});

test("验收场景：5 节点分区，新 Leader Phase 1 补全并安全覆写少数派脏日志", () => {
  const c = new Cluster(5, 1000);
  const oldLeader = c.elect("N0");
  oldLeader.propose("v0"); // 已提交到多数派
  assert.equal(oldLeader.committedEntries().length, 1);

  // 网络分区：旧 Leader 被隔离在少数派
  c.partition(["N0", "N1"], ["N2", "N3", "N4"]);
  oldLeader.propose("dirty"); // 少数派脏日志，未提交
  assert.equal(oldLeader.committedEntries().length, 1);

  // 多数派选出新 Leader（旧 Leader 租约已过期）
  c.tick(2000);
  const newLeader = c.elect("N2");
  newLeader.propose("v1"); // Phase 1 补全 + Phase 2 提交
  assert.deepEqual(newLeader.committedEntries(), ["v0", "v1"]);

  // 分区愈合：少数派脏日志被安全覆写，已提交条目保留
  c.heal();
  newLeader.propose("v2"); // 触发提交广播与一致性同步
  for (const id of c.ids) {
    assert.deepEqual(
      c.node(id).committedEntries(),
      ["v0", "v1", "v2"],
      `${id} 应收敛到多数派日志`,
    );
  }
  // 旧 Leader 已退位
  assert.equal(oldLeader.isLeader, false);
  c.assertCommittedConsistent();
});

test("旧 Leader 愈合后提案被多数派拒绝并退位", () => {
  const c = new Cluster(5, 1000);
  const oldLeader = c.elect("N0");
  oldLeader.propose("v0");
  c.partition(["N0", "N1"], ["N2", "N3", "N4"]);
  c.tick(2000);
  const newLeader = c.elect("N2");
  newLeader.propose("v1");
  c.heal();
  // 旧 Leader 租约已过期，重新竞选会被已承诺更高 ballot 的多数派拒绝
  const ok = oldLeader.propose("stale");
  assert.equal(ok, false);
  assert.equal(oldLeader.isLeader, false);
  // 新 Leader 日志不受污染
  assert.deepEqual(newLeader.committedEntries(), ["v0", "v1"]);
  c.assertCommittedConsistent();
});

test("空洞修补：新 Leader 用 NOOP 填充空洞槽位，提交日志无空洞", () => {
  const c = new Cluster(5, 1000);
  const old = c.elect("N0");
  old.propose("v0"); // index 0，全体提交

  // 少数派分区中制造空洞：N1 只收到 index 2，缺 index 1
  c.partition(["N0", "N1"], ["N2", "N3", "N4"]);
  c.net.dropFilter = (_f, to, msg) =>
    msg.kind === "accept" && msg.index === 1 && to === "N1";
  old.propose("a"); // index 1：仅 N0 接受（对 N1 丢包，N2..N4 分区不可达）
  old.propose("b"); // index 2：N0、N1 接受
  c.net.dropFilter = null;
  assert.equal(old.committedEntries().length, 1); // 均未提交

  // 重新分区：N0（唯一持有 index 1 的节点）被隔离
  c.heal();
  c.partition(["N0"], ["N1", "N2", "N3", "N4"]);
  c.tick(2000);
  const leader = c.elect("N1"); // Phase 1 看到 index 2 但缺 index 1
  // 修补：index 1 填 NOOP，index 2 重新提案 'b'
  assert.deepEqual(leader.committedEntries(), ["v0", NOOP, "b"]);
  // 提交前缀连续（无空洞不变量）
  assert.equal(leader.committedPrefix, 2);

  // 愈合后 N0 的脏日志（含独有的 'a'@1）被覆写为 NOOP
  c.heal();
  leader.propose("c");
  for (const id of c.ids) {
    assert.deepEqual(c.node(id).committedEntries(), ["v0", NOOP, "b", "c"]);
  }
  c.assertCommittedConsistent();
});

test("双 Leader 竞选：高 ballot 胜出，已提交值不丢失", () => {
  const c = new Cluster(3, 1000);
  const l0 = c.elect("N0");
  l0.propose("committed");
  c.partition(["N0"], ["N1", "N2"]);
  c.tick(2000);
  const l1 = c.elect("N1");
  l1.propose("new-value");
  c.heal();
  // 旧 Leader 重新竞选：ballot 被压制
  assert.equal(l0.propose("stale"), false);
  l1.propose("after-heal");
  for (const id of c.ids) {
    assert.deepEqual(
      c.node(id).committedEntries(),
      ["committed", "new-value", "after-heal"],
    );
  }
  c.assertCommittedConsistent();
});

test("无空洞提交不变量：commitIndex 始终为连续前缀", () => {
  const c = new Cluster(5, 1000);
  const leader = c.elect("N2");
  for (let i = 0; i < 50; i++) {
    leader.propose(i);
    for (const id of c.ids) {
      const n = c.node(id);
      // committedEntries 长度必须恰好等于 commitIndex+1（连续）
      assert.equal(n.committedEntries().length, n.committedPrefix + 1);
    }
  }
  c.assertCommittedConsistent();
});

test("高 ballot Prepare 使在位 Leader 退位", () => {
  const c = new Cluster(3, 100000); // 租约很长
  const l0 = c.elect("N0");
  l0.propose("x");
  // N1 直接发起更高 ballot 的竞选
  const n1 = c.node("N1");
  n1.campaign();
  assert.equal(n1.isLeader, true);
  assert.equal(l0.isLeader, false); // 收到更高 ballot 的 Prepare 后退位
  c.assertCommittedConsistent();
});
