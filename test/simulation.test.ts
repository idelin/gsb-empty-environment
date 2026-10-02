import { test } from "node:test";
import assert from "node:assert/strict";
import { ClusterNode, runProbeRound, isConverged } from "../src/node.ts";
import { simulatePartitionHeal } from "../src/simulation.ts";

test("验收：分区愈合后 5 轮内完成全量双向对齐", () => {
  const r = simulatePartitionHeal(5);
  assert.ok(r.converged, "全网视图应收敛");
  assert.ok(r.roundsToConverge <= 5, `应在 5 轮内收敛，实际 ${r.roundsToConverge} 轮`);
  // 双向对齐：6 个节点都持有全量 8 个成员（A1-A4, B1-B4）
  assert.deepEqual(r.memberCountPerNode, [8, 8, 8, 8, 8, 8]);
  assert.match(r.finalDigest, /^[0-9a-f]{8}$/);
});

test("验收：通过 4 字节摘要失配察觉不一致并触发差异拉取", () => {
  const r = simulatePartitionHeal(5);
  assert.ok(r.totalMismatches > 0, "应检测到摘要失配");
  assert.ok(r.totalIntervalPulls > 0, "失配后应触发区间对账拉取");
  assert.ok(r.totalRecordsTransferred > 0);
});

test("快速路径：视图一致时仅交换 4 字节摘要，不拉取记录", () => {
  const a = new ClusterNode("n1");
  const b = new ClusterNode("n2");
  a.connect(b);
  runProbeRound([a, b]); // 先收敛
  const before = a.stats.recordsTransferred + b.stats.recordsTransferred;
  runProbeRound([a, b]); // 已一致：纯摘要比对
  assert.equal(a.stats.recordsTransferred + b.stats.recordsTransferred, before);
  assert.ok(a.stats.digestExchanges > 0);
});

test("双向对齐后各节点成员记录逐条一致", () => {
  const r = simulatePartitionHeal(5);
  assert.ok(r.converged);
  const ref = r.membersPerNode[0];
  assert.equal(ref.length, 8);
  for (const members of r.membersPerNode) {
    assert.deepEqual(members, ref); // 每个节点的成员列表（含状态与版本）完全相同
  }
  // 分区期间制造的关键分歧均被正确合并
  const byId = new Map(ref.map((m) => [m.id, m]));
  assert.equal(byId.get("A4")!.state, "ALIVE");  // A 侧新增成员已同步到全网
  assert.equal(byId.get("B4")!.state, "ALIVE");  // B 侧新增成员已同步到全网
  assert.equal(byId.get("B1")!.state, "DEAD");   // A 侧的 DEAD 判定胜出并传播
  assert.equal(byId.get("A1")!.state, "SUSPECT"); // B 侧的 SUSPECT 判定胜出并传播
});

test("分区期间两子集群视图确实分歧", () => {
  const a1 = new ClusterNode("A1");
  const a2 = new ClusterNode("A2");
  const b1 = new ClusterNode("B1");
  a1.connect(a2); // 仅 A 侧互联，B1 被分区隔离
  runProbeRound([a1, a2]);
  a1.addMember("A9");
  runProbeRound([a1, a2]);
  assert.notEqual(a1.table.digest, b1.table.digest); // 分区两侧摘要不同
  assert.ok(!isConverged([a1, a2, b1]));
  // 愈合：B1 接入，探活后收敛
  a1.connect(b1);
  let rounds = 0;
  while (!isConverged([a1, a2, b1]) && rounds < 5) {
    runProbeRound([a1, a2, b1]);
    rounds++;
  }
  assert.ok(isConverged([a1, a2, b1]));
  assert.ok(rounds <= 5);
  assert.ok(b1.table.get("A9"), "B1 应通过差异拉取获得 A9");
});
