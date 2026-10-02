import { DAGReachabilityIndex } from "../src/reachability-index.ts";

// ---------- 1. 构建层级 DAG ----------
const index = new DAGReachabilityIndex();
const edges: Array<[string, string]> = [
  // 层0 -> 层1
  ["a0", "b0"], ["a0", "b1"], ["a1", "b1"], ["a1", "b2"], ["a2", "b2"],
  // 层1 -> 层2
  ["b0", "c0"], ["b1", "c0"], ["b1", "c1"], ["b2", "c1"], ["b2", "c2"],
  // 层2 -> 层3
  ["c0", "d0"], ["c1", "d0"], ["c2", "d1"],
];
for (const [u, v] of edges) index.addEdge(u, v);
index.build();

console.log("=== 拓扑分层 ===");
index.layersSnapshot().forEach((nodes, level) => {
  console.log(`  层 ${level}: ${nodes.join(", ")}`);
});

// ---------- 2. 任意节点对 O(1) 查询 ----------
console.log("\n=== 可达性查询（跨层边插入前） ===");
const queries: Array<[string, string]> = [
  ["a0", "d0"], ["a2", "d1"], ["a0", "d1"], ["a2", "d0"], ["d0", "a0"],
];
for (const [u, v] of queries) {
  console.log(`  ${u} -> ${v}: ${index.isReachable(u, v)}`);
}

// ---------- 3. 插入跨层边，位运算增量更新下游 ----------
console.log("\n=== 插入跨层边 b0 -> d1（跳过两层） ===");
console.log(`  插入前 a0 可达集: {${index.reachableFrom("a0").join(", ")}}`);
index.addEdge("b0", "d1");
console.log(`  插入后 a0 可达集: {${index.reachableFrom("a0").join(", ")}}`);
console.log(`  a0 -> d1: ${index.isReachable("a0", "d1")}`);
console.log(`  a1 -> d1: ${index.isReachable("a1", "d1")}`);
console.log(`  c0 -> d1: ${index.isReachable("c0", "d1")}（不受影响）`);

// ---------- 4. 环检测 ----------
console.log("\n=== 环检测 ===");
try {
  index.addEdge("d1", "a0");
} catch (err) {
  console.log(`  拒绝回边 d1 -> a0: ${(err as Error).message}`);
}

// ---------- 5. 大规模性能与内存 ----------
console.log("\n=== 大规模：层级树状 DAG（7 层，扇出 3，3279 节点） ===");
const big = new DAGReachabilityIndex();
const FANOUT = 3;
const LAYERS = 7;
const widths: number[] = [];
let width = FANOUT;
for (let l = 0; l < LAYERS; l += 1) {
  widths.push(width);
  width *= FANOUT;
}
const label = (l: number, i: number) => `L${l}n${i}`;
let seed = 42;
const rand = () => {
  seed = (seed * 1103515245 + 12345) % 2147483648;
  return seed / 2147483648;
};
for (let l = 0; l < LAYERS; l += 1) {
  for (let i = 0; i < widths[l]; i += 1) big.addNode(label(l, i));
}
for (let l = 0; l < LAYERS - 1; l += 1) {
  for (let i = 0; i < widths[l]; i += 1) {
    for (let k = 0; k < FANOUT; k += 1) {
      big.addEdge(label(l, i), label(l + 1, i * FANOUT + k));
    }
  }
}

let t0 = performance.now();
big.build();
console.log(`  构建索引: ${(performance.now() - t0).toFixed(1)}ms`);

t0 = performance.now();
for (let k = 0; k < 100; k += 1) {
  const l = Math.floor(rand() * (LAYERS - 2));
  big.addEdge(
    label(l, Math.floor(rand() * widths[l])),
    label(l + 2, Math.floor(rand() * widths[l + 2])),
  );
}
console.log(`  100 次跨层边增量插入: ${(performance.now() - t0).toFixed(1)}ms`);

const QUERIES = 1_000_000;
// 预生成 id 池，热点循环内只做位图位测试
const idPool: number[][] = widths.map((w, l) =>
  Array.from({ length: w }, (_, i) => big.nodeId(label(l, i))!),
);
const totalNodes = widths.reduce((a, b) => a + b, 0);
const flatIds = idPool.flat();
const queryU = new Uint32Array(QUERIES);
const queryV = new Uint32Array(QUERIES);
for (let q = 0; q < QUERIES; q += 1) {
  queryU[q] = flatIds[Math.floor(rand() * totalNodes)];
  queryV[q] = flatIds[Math.floor(rand() * totalNodes)];
}
t0 = performance.now();
let hits = 0;
for (let q = 0; q < QUERIES; q += 1) {
  if (big.isReachableById(queryU[q], queryV[q])) hits += 1;
}
const elapsed = performance.now() - t0;
console.log(`  ${QUERIES.toLocaleString()} 次查询: ${elapsed.toFixed(1)}ms（${((elapsed * 1e6) / QUERIES).toFixed(0)}ns/次，命中 ${hits}）`);

const stats = big.stats();
console.log("\n=== 内存占用 ===");
console.log(`  节点 ${stats.nodes}，边 ${stats.edges}`);
console.log(`  压缩位图: ${(stats.bitmapBytes / 1024).toFixed(1)} KB`);
console.log(`  稠密矩阵: ${(stats.denseBytes / 1024).toFixed(1)} KB`);
console.log(`  压缩率:   ${stats.compressionRatio.toFixed(1)}x`);
