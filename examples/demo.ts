import { KCoreDecomposition } from "../src/kcore.ts";
import { buildAcceptanceGraph } from "./acceptance-graph.ts";

const kc = new KCoreDecomposition(buildAcceptanceGraph());

console.log("=== 验收图：密集子网 (K5, K4) + 缺边 gadget + 悬挂链 ===");
console.log(
  `节点数=${kc.coreNumbers().size}, 全量分解开销: ` +
    `vertices=${kc.lastFullDecomposeStats.vertices}, ` +
    `edgeRelaxations=${kc.lastFullDecomposeStats.edgeRelaxations}`,
);

console.log("\n--- 核度分层（Core-Number -> 节点） ---");
for (const [k, nodes] of kc.layers()) {
  console.log(`core=${k}: [${nodes.join(", ")}]`);
}

console.log("\n--- 1-core 到 4-core 分层子图 ---");
for (let k = 1; k <= 4; k++) {
  const sub = kc.kCore(k);
  console.log(
    `${k}-core: |V|=${sub.nodes.length}, |E|=${sub.edges.length}, ` +
      `nodes=[${sub.nodes.join(", ")}]`,
  );
}

console.log("\n=== 增量加边：桥接边 (9,10)，使缺边 gadget 成为 K4 ===");
console.log(
  `更新前 gadget 核度: [${[9, 10, 11, 12].map((v) => kc.coreNumber(v)).join(", ")}]`,
);
const ins = kc.addEdge(9, 10);
console.log(
  `更新后 gadget 核度: [${[9, 10, 11, 12].map((v) => kc.coreNumber(v)).join(", ")}]`,
);
console.log(
  `局部更新: visited=${ins.visited}, changed=${ins.changed}, ` +
    `edgeRelaxations=${ins.edgeRelaxations}`,
);
console.log(
  `对照全量重算需访问 vertices=${kc.lastFullDecomposeStats.vertices} ` +
    `（局部开销仅为其 ${((ins.visited / kc.lastFullDecomposeStats.vertices) * 100).toFixed(1)}%）`,
);

console.log("\n=== 增量删边：删除 K5 内边 (0,1) ===");
const del = kc.removeEdge(0, 1);
console.log(
  `K5 节点核度: [${[0, 1, 2, 3, 4].map((v) => kc.coreNumber(v)).join(", ")}]`,
);
console.log(
  `局部更新: visited=${del.visited}, changed=${del.changed}, ` +
    `edgeRelaxations=${del.edgeRelaxations}`,
);

console.log("\n--- 更新后分层 ---");
for (const [k, nodes] of kc.layers()) {
  console.log(`core=${k}: [${nodes.join(", ")}]`);
}

// 正确性自检：增量结果与全量重算一致
const before = JSON.stringify([...kc.coreNumbers()].sort((a, b) => a[0] - b[0]));
kc.recompute();
const after = JSON.stringify([...kc.coreNumbers()].sort((a, b) => a[0] - b[0]));
console.log(
  `\n增量维护结果与全量重算一致: ${before === after ? "YES" : "NO"}`,
);
