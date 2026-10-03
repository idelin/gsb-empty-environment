import { BottleneckMonitor } from '../src/monitor.ts';
import { buildHybridNetwork } from '../src/topology.ts';

const { graph, gateway, leaves, meshNodes } = buildHybridNetwork();

console.log('=== 网络瓶颈度与介数中心性近似器 · 运行样例 ===\n');
console.log(
  `混合拓扑：网关 ${gateway} + 骨干[${meshNodes.join(', ')}] + 叶子[${leaves.join(', ')}]` +
    `（${graph.nodeCount} 节点 / ${graph.edgeCount} 边）\n`,
);

const monitor = new BottleneckMonitor(graph, { topK: 5 });

console.log('--- 1. 稳态 Top-5 瓶颈节点 ---');
for (const e of monitor.topKNodes()) {
  console.log(`  #${e.rank} ${e.node}  介数=${e.score.toFixed(2)}`);
}

console.log('\n--- 2. 稳态 Top-5 瓶颈边 ---');
for (const e of monitor.topKEdges()) {
  console.log(`  #${e.rank} ${e.edge[0]}-${e.edge[1]}  介数=${e.score.toFixed(2)}`);
}

console.log('\n--- 3. 边权动态变动：GW-A 骨干链路劣化 (1 -> 4) ---');
const updated = monitor.updateEdgeWeight('GW', 'A', 4);
for (const e of updated) {
  console.log(`  #${e.rank} ${e.node}  介数=${e.score.toFixed(2)}`);
}
monitor.updateEdgeWeight('GW', 'A', 1); // 恢复

console.log('\n--- 4. 模拟网关局部故障（关联链路劣化 x10）---');
const degradeReport = monitor.simulateNodeFailure(gateway, { mode: 'degrade', degradeFactor: 10 });
for (const line of degradeReport.summary) console.log(`  ${line}`);

console.log('\n--- 5. 模拟网关完全故障（节点下线）· 瓶颈转移报告 ---');
const report = monitor.simulateNodeFailure(gateway, { mode: 'remove' });
for (const line of report.summary) console.log(`  ${line}`);

console.log('\n  分流建议明细（改道路径承接 Top-5）：');
for (const d of report.diversions.slice(0, 5)) {
  console.log(`    ${d.node}: 承接 ${d.absorbedPaths} 条改道路径（${(d.share * 100).toFixed(1)}%）`);
}
console.log('\n  故障后 Top-5 瓶颈节点：');
for (const e of report.after) {
  console.log(`    #${e.rank} ${e.node}  介数=${e.score.toFixed(2)}`);
}
