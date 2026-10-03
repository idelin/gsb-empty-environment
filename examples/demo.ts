/**
 * 运行样例：
 *  1) 5 节点 Multi-Paxos 集群：选举 -> 租约直提 -> 网络分区 -> 新 Leader 补洞 -> 愈合收敛
 *  2) 海量稀疏数据：自适应容器 + 压缩的内存对比
 */
import { Cluster } from "../src/paxos/cluster.ts";
import { SparseVector } from "../src/sparse/sparse-vector.ts";
import { CompactArena } from "../src/sparse/arena.ts";

function line(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// ---------- Part 1: Multi-Paxos ----------
line("1. 选举与租约直提（绕过 Phase 1）");
const c = new Cluster(5, 1000);
c.elect(0);
console.log("leader:", c.leader()?.describe());
for (const v of ["set a=1", "set b=2", "set c=3"]) c.propose(0, v);
console.log("committed:", c.convergedCommittedValues());

line("2. 网络分区 {0,1} | {2,3,4}，旧 Leader 持有脏日志");
c.network.partition([[0, 1], [2, 3, 4]]);
c.nodes[0].propose("set d=4 (dirty)"); // accept 只能到达 node1，无法提交
c.network.flush();
console.log("node0 slot4:", c.nodes[0].log.get(4)?.value, "committedUpTo:", c.nodes[0].committedUpTo);

line("3. 多数派选出新 Leader 并继续提交");
c.advanceTime(1500);
c.elect(2);
c.propose(2, "set d=4 (new)");
c.propose(2, "set e=5");
console.log("old leader (partitioned):", c.nodes[0].describe());
console.log("new leader (majority):  ", c.nodes[2].describe());

line("4. 分区愈合：脏日志被安全覆写，全集群收敛无空洞");
c.network.healAll();
c.nodes[2].heartbeat();
c.network.flush();
console.log("converged:", c.convergedCommittedValues());
for (const n of c.nodes) console.log(" ", n.describe());

// ---------- Part 2: 稀疏数据优化器 ----------
line("5. 海量稀疏数据：稀疏 vs 稠密 vs 压缩 内存对比");
const v = new SparseVector({ autoCompactAfter: 0 });
let seed = 7;
const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
for (let i = 0; i < 200_000; i++) v.set(Math.floor(rand() * 50_000_000), i % 977);
console.log(`entries=${v.size} mode=${v.mode} sparse≈${(v.estimateBytes() / 1024).toFixed(0)} KiB (dense 平铺需 ${(50_000_000 * 8 / 1024 / 1024).toFixed(0)} MiB)`);
const { before, after } = v.compact();
console.log(`compact: ${(before / 1024).toFixed(0)} KiB -> ${(after / 1024).toFixed(0)} KiB (mode=${v.mode})`);

line("6. CompactArena：分配/压实/无泄漏");
const arena = new CompactArena(1 << 20);
const handles: number[] = [];
for (let i = 0; i < 100; i++) handles.push(arena.alloc(256));
for (let i = 1; i < 100; i += 2) arena.free(handles[i]);
arena.defrag();
console.log(`live=${arena.liveCount} used=${arena.usedBytes}B free=${arena.freeBytes}B moved=${arena.movedBytes}B`);
for (let i = 0; i < 100; i += 2) arena.free(handles[i]);
arena.assertNoLeaks();
console.log("assertNoLeaks: OK");
