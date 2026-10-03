/**
 * 运行样例：50 节点集群 + 3 个全新节点经单点 Seed 冷启动入网。
 * 运行：npm run demo
 */
import { performance } from 'node:perf_hooks';
import { ClusterNode } from '../src/node.ts';
import { InMemoryNetwork } from '../src/transport.ts';

const net = new InMemoryNetwork();

// 1. Seed 启动，新集群组网
const seed = new ClusterNode({ id: 'node-0', address: 'node-0', transport: net, isSeed: true, gossipIntervalMs: 50 });
await seed.start();
console.log(`[seed] ${seed.meta.id} 上线，新集群组网完成`);

// 2. 49 个节点经 Seed 冷启动入网，形成 50 节点集群
const cluster = [seed];
for (let i = 1; i < 50; i++) {
  cluster.push(new ClusterNode({ id: `node-${i}`, address: `node-${i}`, transport: net, seeds: ['node-0'], gossipIntervalMs: 50 }));
}
await Promise.all(cluster.slice(1).map((n) => n.start()));
console.log(`[cluster] 50 节点集群就绪，seed 已知成员数 = ${seed.membership().length}`);

// 3. 验收场景：3 个全新节点，仅配置单点 Seed 地址
const fresh = ['fresh-1', 'fresh-2', 'fresh-3'].map(
  (id) => new ClusterNode({ id, address: id, transport: net, seeds: ['node-0'], gossipIntervalMs: 50 }),
);

const t0 = performance.now();
await Promise.all(fresh.map((n) => n.start()));
const elapsed = performance.now() - t0;

console.log(`\n[bootstrap] 3 个全新节点完成 Bootstrap 握手，耗时 ${elapsed.toFixed(1)}ms（上限 1000ms）`);
for (const n of fresh) {
  console.log(
    `  ${n.meta.id}: 已知成员 ${n.membership().length} 个, 洗牌环规模 ${n.ringSize}, gossip=${n.gossipActive}`,
  );
}

// 4. 观察 Gossip 洗牌环传播：新节点被既有成员发现
await new Promise((r) => setTimeout(r, 500));
const knownBy = (id: string) => cluster.filter((v) => v.membership().some((m) => m.id === id)).length;
console.log(`\n[gossip] 500ms 后新节点在既有 50 节点中的传播情况：`);
for (const n of fresh) {
  console.log(`  ${n.meta.id} 已被 ${knownBy(n.meta.id)}/50 个既有节点发现`);
}

await Promise.all([...cluster, ...fresh].map((n) => n.stop()));
console.log('\n[demo] 全部节点已停止');
