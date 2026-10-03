import { Node } from "./node.ts";

/**
 * 3 节点（A/B/C）矩阵时钟 + 分布式垃圾回收演示。
 *
 * 场景：A 持续产生事件（LSN 1..12），消息在集群中传播数轮后，
 * A 的矩阵时钟显示「每个节点都已确认知悉 A 的事件直到 LSN=10」，
 * 于是 A 安全地对 LSN<=10 的历史日志执行物理截断。
 */

const cluster = ["A", "B", "C"];
const A = new Node("A", cluster);
const B = new Node("B", cluster);
const C = new Node("C", cluster);

function deliver(from: Node, to: Node): void {
  to.receive(from.send());
  console.log(`  ${from.id} -> ${to.id}  (matrix + log replicated)`);
}

function showMatrix(node: Node): void {
  console.log(`  matrix @ ${node.id} (rows = knowledge of, cols = events from)`);
  console.log(node.clock.format());
}

function showLog(node: Node): void {
  const own = node.log.filter((e) => e.origin === node.id);
  const desc = own.length
    ? own.map((e) => `LSN=${e.lsn}`).join(", ")
    : "(empty)";
  console.log(`  ${node.id} physical log [own entries]: ${desc}`);
}

console.log("=== Round 1: A emits LSN 1-5, replicates to B and C ===");
for (let i = 0; i < 5; i++) A.localEvent(`tx-${i + 1}`);
deliver(A, B);
deliver(A, C);

console.log("\n=== Round 2: A emits LSN 6-10, replicates; B/C have local events ===");
for (let i = 5; i < 10; i++) A.localEvent(`tx-${i + 1}`);
deliver(A, B);
deliver(A, C);
B.localEvent("b-local-1");
C.localEvent("c-local-1");
deliver(B, C);
deliver(C, B);

console.log("\n--- Checkpoint 1: A has NOT heard back from B/C yet ---");
showMatrix(A);
console.log(`  A gcWatermark(A) = ${A.gcWatermark("A")}  -> nothing is safe to truncate`);
const early = A.collectGarbage();
console.log(`  A.collectGarbage() removed ${early.removed.length} entries (expected 0)`);

console.log("\n=== Round 3: B and C report their clocks back to A ===");
deliver(B, A);
deliver(C, A);
showMatrix(A);
console.log(
  `  A gcWatermark(A) = ${A.gcWatermark("A")}  -> every node acknowledged A's events up to LSN=10`,
);

console.log("\n=== A emits LSN 11-12 (not yet disseminated) ===");
A.localEvent("tx-11");
A.localEvent("tx-12");
console.log("  before truncation:");
showLog(A);

console.log("\n=== A triggers physical log truncation (LSN <= 10) ===");
const gc = A.collectGarbage();
console.log(`  truncated ${gc.removed.length} entries in total:`);
for (const origin of cluster) {
  const upTo = gc.truncatedUpTo.get(origin) ?? 0;
  const n = gc.removed.filter((e) => e.origin === origin).length;
  console.log(`    origin ${origin}: watermark=${upTo}, reclaimed ${n} entries`);
}
console.log("  after truncation:");
showLog(A);

console.log(
  "\nDone: A's events with LSN<=10 are acknowledged by every node and reclaimed; LSN 11-12 retained.",
);
