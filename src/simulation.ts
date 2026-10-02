import { ClusterNode, runProbeRound, isConverged } from "./node.ts";
import type { MemberRecord } from "./membership.ts";

export interface SimulationResult {
  roundsToConverge: number;
  converged: boolean;
  finalDigest: string;
  memberCountPerNode: number[];
  totalDigestExchanges: number;
  totalMismatches: number;
  totalIntervalPulls: number;
  totalRecordsTransferred: number;
  membersPerNode: MemberRecord[][];
}

// 场景：两个子集群 {A1,A2,A3} 与 {B1,B2,B3} 初始全网互联并收敛；
// 随后网络分区，两侧各自独立变更成员视图；分区愈合瞬间恢复链路，
// 通过周期探活交换 4 字节 CRC Digest，失配触发区间对账，要求在 maxRounds 内双向对齐。
export function simulatePartitionHeal(maxRounds = 5): SimulationResult {
  const clusterA = [new ClusterNode("A1"), new ClusterNode("A2"), new ClusterNode("A3")];
  const clusterB = [new ClusterNode("B1"), new ClusterNode("B2"), new ClusterNode("B3")];
  const all = [...clusterA, ...clusterB];

  // 初始：全互联，传播成员视图直至收敛。
  for (const n of all) for (const m of all) if (n !== m) n.connect(m);
  let guard = 0;
  while (!runProbeRound(all) && guard++ < 10) {}

  // 网络分区：割断两个子集群之间的所有链路。
  for (const a of clusterA) for (const b of clusterB) a.disconnect(b);

  // 分区期间两侧独立演化，制造视图分歧：
  clusterA[0].addMember("A4");            // A 侧新成员加入
  clusterA[1].markState("B1", "DEAD");    // A 侧误判 B1 死亡
  clusterB[0].addMember("B4");            // B 侧新成员加入
  clusterB[2].markState("A1", "SUSPECT"); // B 侧怀疑 A1
  clusterB[0].markState("B2", "SUSPECT"); // B 侧内部状态变更
  guard = 0;
  while (!runProbeRound(clusterA) && guard++ < 10) {}
  guard = 0;
  while (!runProbeRound(clusterB) && guard++ < 10) {}

  // 分区愈合瞬间：恢复跨集群链路。
  for (const a of clusterA) for (const b of clusterB) a.connect(b);

  // 周期探活：交换 4 字节 Digest -> 失配 -> 区间对账 -> 收敛。
  let rounds = 0;
  while (!isConverged(all) && rounds < maxRounds) {
    runProbeRound(all);
    rounds++;
  }

  const sum = (f: (n: ClusterNode) => number) => all.reduce((s, n) => s + f(n), 0);
  return {
    roundsToConverge: rounds,
    converged: isConverged(all),
    finalDigest: all[0].table.digestHex,
    memberCountPerNode: all.map((n) => n.table.size),
    totalDigestExchanges: sum((n) => n.stats.digestExchanges),
    totalMismatches: sum((n) => n.stats.digestMismatches),
    totalIntervalPulls: sum((n) => n.stats.intervalPulls),
    totalRecordsTransferred: sum((n) => n.stats.recordsTransferred),
    membersPerNode: all.map((n) =>
      n.table.all().sort((x, y) => x.id.localeCompare(y.id)),
    ),
  };
}

export function printReport(r: SimulationResult): void {
  console.log("=== 分区愈合后拓扑视图对账报告 ===");
  console.log(`收敛轮次:        ${r.roundsToConverge} (上限 5)`);
  console.log(`全网收敛:        ${r.converged ? "是" : "否"}`);
  console.log(`最终 Digest:     ${r.finalDigest} (4 字节 CRC-32)`);
  console.log(`各节点成员数:    [${r.memberCountPerNode.join(", ")}]`);
  console.log(`Digest 交换次数: ${r.totalDigestExchanges}`);
  console.log(`摘要失配次数:    ${r.totalMismatches}`);
  console.log(`区间对账拉取:    ${r.totalIntervalPulls}`);
  console.log(`传输记录总数:    ${r.totalRecordsTransferred}`);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  printReport(simulatePartitionHeal());
}
