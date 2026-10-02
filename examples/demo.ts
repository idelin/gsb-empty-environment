import { DagReachabilityIndex } from "../src/reachability-index.ts";

const index = new DagReachabilityIndex();

const layerEdges: Array<[string, string]> = [
  ["api", "auth"],
  ["api", "orders"],
  ["auth", "user-store"],
  ["orders", "inventory"],
  ["orders", "payment"],
  ["inventory", "warehouse-db"],
  ["payment", "ledger-db"],
  ["user-store", "ledger-db"],
];
for (const [u, v] of layerEdges) index.addEdge(u, v);

console.log("== 拓扑层级 ==");
index.layers().forEach((nodes, level) => {
  console.log(`  level ${level}: ${nodes.join(", ")}`);
});

console.log("\n== 可达性查询（O(1) 位测试）==");
const queries: Array<[string, string]> = [
  ["api", "ledger-db"],
  ["api", "warehouse-db"],
  ["auth", "payment"],
  ["payment", "api"],
];
for (const [u, v] of queries) {
  console.log(`  reachable(${u}, ${v}) = ${index.reachable(u, v)}`);
}

console.log("\n== 插入跨层边 api -> payment ==");
index.addEdge("api", "payment");
console.log(`  reachable(api, ledger-db)   = ${index.reachable("api", "ledger-db")}`);
console.log(`  reachable(orders, payment)  = ${index.reachable("orders", "payment")}`);
console.log(`  level(payment)              = ${index.levelOf("payment")}`);
console.log(`  reachableSet(api)           = ${index.reachableSet("api").join(", ")}`);

console.log("\n== 内存占用（压缩位图 vs 稠密位矩阵）==");
const sparse = new DagReachabilityIndex();
for (let c = 0; c < 128; c++) {
  for (let i = 0; i < 7; i++) sparse.addEdge(`svc${c}-${i}`, `svc${c}-${i + 1}`);
}
console.log(`  nodes:      ${sparse.nodeCount()}`);
console.log(`  compressed: ${sparse.memoryBytes()} bytes`);
console.log(`  dense:      ${sparse.denseMatrixBytes()} bytes`);
console.log(`  ratio:      ${(sparse.denseMatrixBytes() / sparse.memoryBytes()).toFixed(1)}x`);

console.log("\n== 成环边被拒绝 ==");
try {
  index.addEdge("ledger-db", "api");
} catch (err) {
  console.log(`  ${(err as Error).message}`);
}
