import {
  CoreMaintenance,
  coreLayers,
  coreNumbers,
  kCoreSubgraph,
  maxCore,
  type BatchStats,
} from "../src/index.ts";

function fmt(nodes: number[]): string {
  return `{${nodes.join(", ")}}`;
}

function sameCore(a: Uint32Array, b: Uint32Array): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

// ================================================================
// 验收场景：密集子网 + 悬挂链
//   节点 0-4   : K5 团（稠密子网）                -> 期望 core 4
//   节点 5-8   : K4 团，经边 4-5 挂在 K5 上        -> 期望 core 3
//   节点 9-12  : K4 缺少边 (11,12) 的部件          -> 期望 core 2
//   节点 13-16 : 悬挂链，经边 0-13 挂在团上        -> 期望 core 1
// ================================================================
const edges: [number, number][] = [];
for (let i = 0; i < 5; i += 1) {
  for (let j = i + 1; j < 5; j += 1) edges.push([i, j]); // K5
}
edges.push([5, 6], [5, 7], [5, 8], [6, 7], [6, 8], [7, 8], [4, 5]); // K4 + 挂接边
edges.push([9, 10], [9, 11], [9, 12], [10, 11], [10, 12]); // K4 - e(11,12)
edges.push([0, 13], [13, 14], [14, 15], [15, 16]); // 悬挂链

const cm = CoreMaintenance.fromEdges(edges);
const graph = cm.graph;

console.log("=== 1. 批量核度分解（密集子网 + 悬挂链） ===");
console.log(`图规模: |V|=${graph.nodeCount}, |E|=${graph.numEdges}, 最大核度=${cm.maxCore}`);
for (const [k, nodes] of coreLayers(cm.coreNumbers())) {
  console.log(`  core=${k} 层 (${nodes.length} 个节点): ${fmt(nodes)}`);
}

console.log("\n=== 2. 1-core 到 4-core 分层子图 ===");
for (let k = 1; k <= 4; k += 1) {
  const sub = kCoreSubgraph(graph, cm.coreNumbers(), k);
  console.log(
    `  ${k}-core: |V|=${sub.nodes.length}, |E|=${sub.edges.length}, 节点=${fmt(sub.nodes)}`,
  );
}

console.log("\n=== 3. 加入桥接边 (11,12)：局部核度 2 -> 3 ===");
console.log(`  插入前部件核度: ${[9, 10, 11, 12].map((v) => cm.coreOf(v)).join(", ")}`);
const ins = cm.insertEdge(11, 12);
console.log(
  `  增量更新: K=${ins.K}, 访问节点=${ins.visited}, 核度变化节点=${fmt(ins.changed)}`,
);
console.log(`  插入后部件核度: ${[9, 10, 11, 12].map((v) => cm.coreOf(v)).join(", ")}`);

const batchStats1: BatchStats = { nodesProcessed: 0, neighborScans: 0 };
const batchCore1 = coreNumbers(graph, batchStats1);
console.log(
  `  全局重算  : 处理节点=${batchStats1.nodesProcessed}, 邻接扫描=${batchStats1.neighborScans}`,
);
console.log(`  增量扫描  : 邻接扫描=${ins.neighborScans}`);
console.log(`  与全局重算结果一致: ${sameCore(cm.coreNumbers(), batchCore1)}`);

console.log("\n=== 4. 删除桥接边 (11,12)：核度回落 3 -> 2 ===");
const del = cm.removeEdge(11, 12);
console.log(
  `  增量更新: K=${del.K}, 访问节点=${del.visited}, 核度变化节点=${fmt(del.changed)}`,
);
console.log(`  删除后部件核度: ${[9, 10, 11, 12].map((v) => cm.coreOf(v)).join(", ")}`);
const batchCore2 = coreNumbers(graph);
console.log(`  与全局重算结果一致: ${sameCore(cm.coreNumbers(), batchCore2)}`);

// ================================================================
// 大规模稀疏图：局部更新开销 vs 全局重算
//   200 个 K8 团 + 100 条长度为 100 的悬挂链 + 1 个 K4-e 部件
// ================================================================
console.log("\n=== 5. 大规模图上的局部更新开销对比 ===");
const bigEdges: [number, number][] = [];
let next = 0;
for (let c = 0; c < 200; c += 1) {
  const base = next;
  next += 8;
  for (let i = 0; i < 8; i += 1) {
    for (let j = i + 1; j < 8; j += 1) bigEdges.push([base + i, base + j]);
  }
}
for (let c = 0; c < 100; c += 1) {
  const anchor = c * 8; // 挂在第 c 个团的 0 号节点上
  bigEdges.push([anchor, next]);
  for (let i = 0; i < 99; i += 1) bigEdges.push([next + i, next + i + 1]);
  next += 100;
}
const g = next; // K4-e 部件：g, g+1, g+2, g+3，缺边 (g+2, g+3)
bigEdges.push([g, g + 1], [g, g + 2], [g, g + 3], [g + 1, g + 2], [g + 1, g + 3]);

const big = CoreMaintenance.fromEdges(bigEdges);
console.log(
  `图规模: |V|=${big.graph.nodeCount}, |E|=${big.graph.numEdges}, 最大核度=${big.maxCore}`,
);

let t0 = performance.now();
const bigIns = big.insertEdge(g + 2, g + 3);
const incMs = performance.now() - t0;

t0 = performance.now();
const bigBatchStats: BatchStats = { nodesProcessed: 0, neighborScans: 0 };
const bigBatchCore = coreNumbers(big.graph, bigBatchStats);
const batchMs = performance.now() - t0;

console.log(
  `  增量更新: 访问节点=${bigIns.visited}, 核度变化=${bigIns.updated}, ` +
    `邻接扫描=${bigIns.neighborScans}, 耗时=${incMs.toFixed(3)}ms`,
);
console.log(
  `  全局重算: 处理节点=${bigBatchStats.nodesProcessed}, ` +
    `邻接扫描=${bigBatchStats.neighborScans}, 耗时=${batchMs.toFixed(3)}ms`,
);
console.log(
  `  局部/全局节点开销比: ${(bigIns.visited / bigBatchStats.nodesProcessed).toExponential(2)}`,
);
console.log(`  与全局重算结果一致: ${sameCore(big.coreNumbers(), bigBatchCore)}`);
console.log(`  部件核度: ${[g, g + 1, g + 2, g + 3].map((v) => big.coreOf(v)).join(", ")}`);

if (maxCore(big.coreNumbers()) !== big.maxCore) {
  throw new Error("demo invariant violated");
}
