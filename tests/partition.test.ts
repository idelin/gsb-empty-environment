/**
 * 验收场景：5 节点集群网络分区。
 *  - 旧 Leader（node0）与 node1 被隔离为少数派，且持有未提交的脏日志
 *  - 多数派（node2,3,4）选出新 Leader，通过 Phase 1 补全未提交槽位
 *  - 分区愈合后，少数派脏日志被安全覆写，已提交日志零丢失，全集群收敛无空洞
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/paxos/cluster.ts";
import { NO_OP } from "../src/paxos/types.ts";

function assertHoleFree(c: Cluster, upTo: number): void {
  for (const n of c.nodes) {
    for (let i = 1; i <= upTo; i++) {
      const e = n.log.get(i);
      assert.ok(e, `node${n.id} missing entry ${i}`);
      assert.ok(e!.committed, `node${n.id} entry ${i} not committed`);
    }
  }
}

test("网络分区：旧 Leader 被多数派新 Leader 取代，脏日志被安全覆写", () => {
  const c = new Cluster(5, 1000);

  // 1) node0 当选，提交 3 条日志
  assert.equal(c.elect(0), true);
  c.propose(0, "tx-1");
  c.propose(0, "tx-2");
  c.propose(0, "tx-3");
  assert.deepEqual(c.convergedCommittedValues(), ["tx-1", "tx-2", "tx-3"]);

  // 2) 分区 {0,1} | {2,3,4} 后 node0 提案 tx-4：accept 只能到达 node1
  c.network.partition([[0, 1], [2, 3, 4]]);
  c.nodes[0].propose("tx-4");
  c.network.flush();
  assert.equal(c.nodes[0].log.get(4)?.value, "tx-4");
  assert.equal(c.nodes[1].log.get(4)?.value, "tx-4");
  assert.equal(c.nodes[0].committedUpTo, 3); // 少数派无法提交
  assert.equal(c.nodes[2].log.has(4), false);

  // 3) 租约过期，多数派侧 node2 发起 Phase 1 成为新 Leader
  c.advanceTime(1500);
  assert.equal(c.nodes[0].leaseValid, false);
  assert.equal(c.elect(2), true);
  assert.equal(c.nodes[2].committedUpTo, 3); // 继承已提交前缀

  // 4) 新 Leader 在多数派内继续提交新日志（覆写槽位 4 的语义）
  c.propose(2, "tx-4-new");
  c.propose(2, "tx-5");
  assert.equal(c.nodes[2].committedUpTo, 5);
  assert.equal(c.nodes[4].log.get(4)?.value, "tx-4-new");

  // 5) 旧 Leader 在少数派内徒劳重试：无法获得多数派，不能提交
  const retried = c.nodes[0].propose("tx-4-stale");
  assert.equal(retried, -1); // 租约过期 -> 缓冲并重新竞选
  c.network.flush();
  assert.equal(c.nodes[0].isLeader, false); // 分区中竞选失败
  assert.equal(c.nodes[0].committedUpTo, 3);

  // 6) 分区愈合：新 Leader 心跳/提案触发少数派 sync，脏日志被安全覆写
  c.network.healAll();
  c.nodes[2].heartbeat();
  c.network.flush();
  c.propose(2, "tx-6");
  c.network.flush();

  // 7) 严格断言：全集群收敛、无空洞、已提交零丢失、脏值被覆写
  assert.deepEqual(c.convergedCommittedValues(), ["tx-1", "tx-2", "tx-3", "tx-4-new", "tx-5", "tx-6"]);
  assertHoleFree(c, 6);
  for (const id of [0, 1]) {
    assert.equal(c.nodes[id].log.get(4)?.value, "tx-4-new", `node${id} dirty entry not overwritten`);
    assert.equal(c.nodes[id].committedUpTo, 6);
  }
  // 已提交条目绝未被改写
  for (const n of c.nodes) {
    assert.deepEqual(
      n.committedEntries().slice(0, 3).map((e) => e.value),
      ["tx-1", "tx-2", "tx-3"],
    );
  }
});

test("新 Leader 通过 Phase 1 用 NO_OP 补全空洞槽位", () => {
  const c = new Cluster(5, 1000);
  c.elect(0);
  c.propose(0, "a");
  c.propose(0, "b");

  // 手工制造空洞：先分区 {0,1} | {2,3,4}，旧 Leader 提案 c/d 的 accept
  // 无法到达多数派；再单独把 index=4 的 accept 重放给 node2，
  // 使 node2 持有槽位 4 的未提交条目而槽位 3 缺失（故障现场）。
  c.network.partition([[0, 1], [2, 3, 4]]);
  c.nodes[0].propose("c"); // index 3，accept 被分区丢弃
  c.nodes[0].propose("d"); // index 4，accept 被分区丢弃
  c.network.flush();
  // 重放一条"迟到"的 accept 给 node2（临时单链路可达，模拟故障现场）
  c.network.setLink(0, 2, true);
  c.network.send(
    { type: "accept", from: 0, to: 2, ballot: { term: 1, node: 0 }, index: 4, value: "d" },
    0,
  );
  c.network.flush();
  c.network.setLink(0, 2, false);
  assert.equal(c.nodes[2].log.get(4)?.value, "d");
  assert.equal(c.nodes[2].log.has(3), false); // 空洞存在

  // 多数派选新 Leader：Phase 1 发现槽位 4 有未提交条目、槽位 3 为空洞
  c.advanceTime(1500);
  assert.equal(c.elect(2), true);
  // 补洞：槽位 3 填 NO_OP，槽位 4 采纳最高 ballot 的值 "d"，全部提交
  assert.equal(c.nodes[2].log.get(3)?.value, NO_OP);
  assert.equal(c.nodes[2].log.get(4)?.value, "d");
  assert.equal(c.nodes[2].committedUpTo, 4);
  c.network.flush();
  for (const id of [2, 3, 4]) {
    assert.equal(c.nodes[id].log.get(3)?.value, NO_OP);
    assert.equal(c.nodes[id].log.get(3)?.committed, true);
    assert.equal(c.nodes[id].log.get(4)?.value, "d");
    assert.equal(c.nodes[id].committedUpTo, 4);
  }

  // 愈合后少数派同步到无空洞日志
  c.network.healAll();
  c.nodes[2].heartbeat();
  c.network.flush();
  for (const n of c.nodes) {
    assert.equal(n.committedUpTo, 4, `node${n.id} not caught up`);
    assert.equal(n.log.get(3)?.value, NO_OP);
    assert.equal(n.log.get(4)?.value, "d");
  }
});

test("分区期间双 Leader 不能同时提交冲突值（脑裂安全性）", () => {
  const c = new Cluster(5, 100000); // 长租约：旧 Leader 自认为仍有效
  c.elect(0);
  c.propose(0, "base");
  c.network.partition([[0, 1], [2, 3, 4]]);

  // 旧 Leader 在少数派内提案：无法达到多数派
  c.nodes[0].propose("minority-val");
  c.network.flush();
  assert.equal(c.nodes[0].committedUpTo, 1);

  // 多数派新 Leader 提交同槽位不同值
  c.advanceTime(1); // 推进时钟但不影响长租约
  c.elect(2);
  c.propose(2, "majority-val");
  assert.equal(c.nodes[2].committedUpTo, 2);

  // 愈合后：少数派的未提交值被覆写，多数派值胜出
  c.network.healAll();
  c.nodes[2].heartbeat();
  c.network.flush();
  for (const n of c.nodes) {
    assert.equal(n.log.get(2)?.value, "majority-val", `node${n.id} diverged`);
  }
  assert.deepEqual(c.convergedCommittedValues(), ["base", "majority-val"]);
});
