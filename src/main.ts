import { Node } from "./node";
import { Simulator } from "./simulator";
import { Topo, canonicalLsdb, canonicalTopo, deriveTopology } from "./topology";
import { Lsa, NodeId, compareVersions, formatVersion } from "./types";

let failures = 0;
function check(name: string, cond: boolean, detail = ""): void {
  if (!cond) failures++;
  console.log(`  [${cond ? "PASS" : "FAIL"}] ${name}${detail ? ` — ${detail}` : ""}`);
}

function lsa(origin: NodeId, generation: number, sequence: number): Lsa {
  return { origin, version: { generation, sequence }, links: {}, withdrawn: false };
}

function unitTests(): void {
  console.log("== 单元测试 ==");

  // T1: 版本比较为 (代次, 序号) 字典序
  check(
    "T1 版本比较按 (代次,序号) 字典序",
    compareVersions({ generation: 0, sequence: 9 }, { generation: 1, sequence: 0 }) < 0 &&
      compareVersions({ generation: 1, sequence: 0 }, { generation: 1, sequence: 0 }) === 0 &&
      compareVersions({ generation: 2, sequence: 0 }, { generation: 1, sequence: 7 }) > 0,
  );

  // T2: 跳过被同源新版本覆盖的旧更新
  const z = new Node("Z");
  z.handleUpdate("W", [lsa("P", 0, 5)]);
  z.handleUpdate("W", [lsa("P", 0, 3)]); // 旧序号 -> 跳过
  z.handleUpdate("W", [lsa("P", 0, 5)]); // 重复 -> 跳过
  z.handleUpdate("W", [lsa("P", 1, 0)]); // 新代次 -> 接受
  const stored = z.getLsa("P");
  check(
    "T2 旧/重复更新被跳过，仅更新版本被安装",
    z.stats.installed === 2 &&
      z.stats.skippedStale === 2 &&
      !!stored &&
      stored.version.generation === 1 &&
      stored.version.sequence === 0,
    `installed=${z.stats.installed} skipped=${z.stats.skippedStale}`,
  );

  // T3: 删除后重建必须使用新代次
  const y = new Node("Y");
  y.refreshSelfLsa();
  y.refreshSelfLsa();
  const withdrawn = y.withdraw();
  const reborn = y.refreshSelfLsa();
  check(
    "T3 删除后重建自动提升代次（代次+1、序号归零）",
    withdrawn.version.generation === 0 &&
      withdrawn.withdrawn &&
      reborn.version.generation === 1 &&
      reborn.version.sequence === 0,
    `withdraw=${formatVersion(withdrawn.version)} reborn=${formatVersion(reborn.version)}`,
  );

  // T4: 摘要交换按来源分别跟踪进度，而非全局最大版本
  const x = new Node("X");
  x.handleUpdate("Y", [lsa("p", 0, 1), lsa("q", 0, 1)]);
  x.handleSummary("Y", {
    p: { generation: 0, sequence: 1 },
    q: { generation: 0, sequence: 5 },
    r: { generation: 0, sequence: 2 },
  });
  const requests = x.drainOutbox().filter((m) => m.kind === "request");
  const requested =
    requests.length === 1 && requests[0].kind === "request" ? requests[0].origins : [];
  check(
    "T4 缺失补齐按来源精确请求 [q,r]，而非按全局最大版本回拉",
    JSON.stringify(requested) === JSON.stringify(["q", "r"]),
    `requested=[${requested.join(",")}]`,
  );
}

function scenario(): void {
  const sim = new Simulator();
  for (const id of ["A", "B", "C", "D", "E", "F"]) sim.addNode(id);
  // 两个三角形集群 + 桥接链路 C-D
  sim.addLink("A", "B");
  sim.addLink("A", "C");
  sim.addLink("B", "C");
  sim.addLink("D", "E");
  sim.addLink("D", "F");
  sim.addLink("E", "F");
  sim.addLink("C", "D");
  sim.refreshAll();
  sim.converge();

  console.log("\n== 阶段1: 初始收敛 ==");
  const base = canonicalLsdb(sim.nodes.get("A")!.lsdbEntries());
  let allEqual = sim.nodes.get("A")!.lsdbEntries().length === 6;
  for (const node of sim.nodes.values()) {
    if (canonicalLsdb(node.lsdbEntries()) !== base) allEqual = false;
  }
  check("6 个节点 LSDB 完全一致（各 6 条 LSA）", allEqual);

  // 留存 A 的旧 LSA，稍后注入验证旧更新被跳过
  const staleA = sim.nodes.get("B")!.getLsa("A")!;

  console.log("\n== 阶段2: 分区（断开桥接链路 C-D）==");
  sim.setLink("C", "D", false);
  sim.converge();
  const side1 = canonicalLsdb(sim.nodes.get("C")!.lsdbEntries());
  const side2 = canonicalLsdb(sim.nodes.get("D")!.lsdbEntries());
  check("分区后两侧 LSDB 出现分歧", side1 !== side2);

  console.log("\n== 阶段3: 分区内变更 ==");
  sim.setLinkCost("A", "B", 5); // 分区1内：A-B 开销 1 -> 5
  sim.converge();
  const skippedBefore = sim.nodes.get("B")!.stats.skippedStale;
  sim.inject({ kind: "update", from: "A", to: "B", lsas: [staleA] }); // 注入被覆盖的旧版本
  sim.deliverAll();
  const afterInject = sim.nodes.get("B")!;
  check(
    "被同源新版本覆盖的旧更新被跳过",
    afterInject.stats.skippedStale === skippedBefore + 1 &&
      afterInject.getLsa("A")!.version.sequence === 1,
    `A@${formatVersion(afterInject.getLsa("A")!.version)} 保持不变`,
  );

  sim.withdrawNode("D"); // 分区2内：D 删除（撤销）自己的 LSA
  sim.converge();
  check("删除（撤销）在分区2内传播", sim.nodes.get("E")!.getLsa("D")!.withdrawn);
  sim.restoreNode("D"); // D 删除后重建
  sim.converge();
  const dAtE = sim.nodes.get("E")!.getLsa("D")!;
  check(
    "删除后重建使用新代次",
    dAtE.version.generation === 1 && dAtE.version.sequence === 0 && !dAtE.withdrawn,
    `D@${formatVersion(dAtE.version)}`,
  );

  console.log("\n== 阶段4: 重连（摘要交换 + 缺失补齐）==");
  const fills: string[] = [];
  sim.onDeliver = (msg) => {
    if (msg.kind === "request") fills.push(`    ${msg.from} -> ${msg.to} 请求 [${msg.origins.join(", ")}]`);
  };
  sim.setLink("C", "D", true);
  sim.converge();
  sim.onDeliver = null;
  for (const line of fills) console.log(line);
  check("重连后按来源精确补齐缺失", fills.length > 0);

  console.log("\n== 阶段5: 一致性核对 ==");
  const refLsdb = canonicalLsdb(sim.nodes.get("A")!.lsdbEntries());
  const refTopo = canonicalTopo(deriveTopology(sim.nodes.get("A")!.lsdbEntries()));
  let lsdbOk = true;
  let topoOk = true;
  for (const [id, node] of sim.nodes) {
    const lsdbCanon = canonicalLsdb(node.lsdbEntries());
    const topoCanon = canonicalTopo(deriveTopology(node.lsdbEntries()));
    if (lsdbCanon !== refLsdb) lsdbOk = false;
    if (topoCanon !== refTopo) topoOk = false;
    console.log(`    ${id}: ${topoCanon}`);
  }
  check("所有节点 LSDB 一致", lsdbOk);
  check("所有节点拓扑一致", topoOk);

  const expected: Topo = {};
  const addEdge = (a: NodeId, b: NodeId, cost: number): void => {
    expected[a] = expected[a] ?? {};
    expected[b] = expected[b] ?? {};
    expected[a][b] = cost;
    expected[b][a] = cost;
  };
  addEdge("A", "B", 5);
  addEdge("A", "C", 1);
  addEdge("B", "C", 1);
  addEdge("C", "D", 1);
  addEdge("D", "E", 1);
  addEdge("D", "F", 1);
  addEdge("E", "F", 1);
  check(
    "拓扑与期望一致（含分区期间 A-B 新开销 5、D 重建）",
    refTopo === canonicalTopo(expected),
  );

  const dFinal = sim.nodes.get("F")!.getLsa("D")!;
  check("D 的 LSA 全网为新代次", dFinal.version.generation === 1);

  const vector = sim.nodes.get("A")!.versionVector();
  const vectorStr = Object.keys(vector)
    .sort()
    .map((o) => `${o}@${formatVersion(vector[o])}`)
    .join(" ");
  console.log(`\n  最终逐来源版本向量（A 视角）: ${vectorStr}`);
  let totalSkipped = 0;
  for (const node of sim.nodes.values()) totalSkipped += node.stats.skippedStale;
  console.log(
    `  消息统计: summary=${sim.stats.delivered.summary} request=${sim.stats.delivered.request} ` +
      `update=${sim.stats.delivered.update} dropped=${sim.stats.dropped} 跳过的旧更新=${totalSkipped}`,
  );
}

console.log("链路状态同步模拟器\n");
unitTests();
scenario();
console.log(failures === 0 ? "\n全部检查通过 ✅" : `\n${failures} 项检查失败 ❌`);
process.exit(failures === 0 ? 0 : 1);
