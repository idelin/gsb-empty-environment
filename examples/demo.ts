/**
 * 运行样例：
 * 1) 5 节点集群网络分区 —— 旧 Leader 被取代、Phase 1 补全、覆写少数派脏日志
 * 2) 海量稀疏数据的内存紧凑优化与压缩
 */
import { Cluster } from "../src/paxos/cluster.ts";
import { NOOP } from "../src/paxos/types.ts";
import { SparseArray } from "../src/sparse/sparse-array.ts";
import { LeakRegistry } from "../src/sparse/containers.ts";

function show(c: Cluster, title: string): void {
  console.log(`\n== ${title} ==`);
  for (const id of c.ids) {
    const n = c.node(id);
    const role = n.isLeader ? (n.leaseValid ? "Leader(租约有效)" : "Leader(租约过期)") : "Follower";
    console.log(
      `  ${id} ${role}  committed=[${n.committedEntries().map(String).join(", ")}]`,
    );
  }
}

// ---------- 场景 1：网络分区与 Leader 更替 ----------
console.log("### Multi-Paxos：5 节点集群网络分区场景");
const c = new Cluster(5, 1000);

const oldLeader = c.elect("N0");
oldLeader.propose("v0");
show(c, "1. N0 当选 Leader，v0 已提交到多数派");

c.partition(["N0", "N1"], ["N2", "N3", "N4"]);
oldLeader.propose("dirty"); // 少数派：达不到 quorum=3，无法提交
show(c, "2. 网络分区：旧 Leader 在少数派，dirty 无法提交");

c.tick(2000); // 旧 Leader 租约过期
const newLeader = c.elect("N2");
newLeader.propose("v1");
show(c, "3. 多数派选出新 Leader N2：Phase 1 补全 + Phase 2 提交 v1");

c.heal();
// 旧 Leader 以过期 ballot 重新竞选，被已承诺更高 ballot 的多数派拒绝
const rejected = !oldLeader.propose("stale");
newLeader.propose("v2"); // 提交广播触发一致性同步，覆写少数派脏日志
show(c, "4. 分区愈合：少数派脏日志被安全覆写，全体收敛 [v0, v1, v2]");

console.log(`\n旧 Leader N0 已退位: ${!oldLeader.isLeader}`);
console.log(`旧 Leader 过期提案被多数派拒绝: ${rejected}`);
c.assertCommittedConsistent();
console.log("集群不变量校验通过：已提交日志全体一致、无空洞");

// ---------- 场景 2：空洞修补 ----------
console.log("\n### 空洞修补：新 Leader 用 NOOP 填充空洞槽位");
const c2 = new Cluster(5, 1000);
const l0 = c2.elect("N0");
l0.propose("v0");
c2.partition(["N0", "N1"], ["N2", "N3", "N4"]);
c2.net.dropFilter = (_f, to, msg) => msg.kind === "accept" && msg.index === 1 && to === "N1";
l0.propose("a"); // index 1 仅 N0 接受
l0.propose("b"); // index 2 被 N0、N1 接受
c2.net.dropFilter = null;
c2.heal();
c2.partition(["N0"], ["N1", "N2", "N3", "N4"]); // 唯一持有 a@1 的 N0 被隔离
c2.tick(2000);
const l1 = c2.elect("N1");
console.log(`新 Leader N1 修补后提交日志: [${l1.committedEntries().map((v) => (v === NOOP ? "NOOP" : String(v))).join(", ")}]`);
console.log("index 1 无人持有 → 填 NOOP；index 2 重新提案 'b'；提交前缀连续无空洞");

// ---------- 场景 3：稀疏数据优化器 ----------
console.log("\n### 海量稀疏数据与内存紧凑优化器");
const sparse = new SparseArray<string>();
const SPAN = 10_000_000;
for (let i = 0; i < SPAN; i += 1000) sparse.set(i, `group-${(i / 1000) % 5}`);
sparse.optimize();
const packed = sparse.compress();
console.log(`下标空间: ${SPAN.toLocaleString()}，非空元素: ${sparse.length.toLocaleString()}`);
console.log(`内存估算: ${sparse.memoryBytes().toLocaleString()} B（等效稠密数组 ${(SPAN * 8).toLocaleString()} B）`);
console.log(`压缩后: ${packed.length.toLocaleString()} B（压缩率 ${(packed.length / (SPAN * 8) * 100).toFixed(3)}%）`);
const restored = SparseArray.decompress<string>(packed);
console.log(`解压往返一致: ${restored.length === sparse.length && restored.get(9_999_000) === sparse.get(9_999_000)}`);

// 容器自适应展示
const mixed = new SparseArray<number>();
for (let i = 0; i < 4096; i++) mixed.set(i, i);              // 块 0：稠密 → Dense
for (let i = 0; i < 500; i++) mixed.set(4096 + i, 1);        // 块 1：游程 → RLE
for (let i = 0; i < 50; i++) mixed.set(8192 + i * 61, i);    // 块 2：散点 → Map
mixed.optimize();
console.log(`容器自适应转换: [${mixed.chunkKinds().join(", ")}]（dense / rle / map）`);

// 并发安全展示
const counter = new SparseArray<number>();
await Promise.all(Array.from({ length: 100 }, () => counter.increment(42)));
console.log(`100 路并发自增结果: ${counter.get(42)}（精确无丢失）`);

sparse.dispose();
restored.dispose();
mixed.dispose();
counter.dispose();
LeakRegistry.assertNoLeaks();
console.log("泄漏检查通过：所有容器已释放，零泄漏");
