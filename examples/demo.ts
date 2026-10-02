import { IncrementalTopoSort } from '../src/incremental-topo.ts';

const g = new IncrementalTopoSort<number>();

console.log('=== 初始依赖链 1->2->3->4 ===');
for (const [u, v] of [[1, 2], [2, 3], [3, 4]] as const) {
  g.addEdge(u, v);
}
console.log('拓扑序:', g.order());

console.log('\n=== 场景一：增加边 4->2（成环，应立即拒绝） ===');
const r1 = g.addEdge(4, 2);
if (!r1.ok) {
  console.log('拒绝！检测到因果死锁环路:', r1.cycle.join(' -> '));
}
console.log('拓扑序（应保持不变）:', g.order());

console.log('\n=== 场景二：增加边 1->4（前向边，快速接受） ===');
const r2 = g.addEdge(1, 4);
if (r2.ok) {
  console.log('接受。受影响重排节点:', r2.affected.length === 0 ? '无（快路径）' : r2.affected);
}
console.log('拓扑序（保持单调）:', g.order());

console.log('\n=== 场景三：后向无环边触发双向局部重染色 ===');
const h = new IncrementalTopoSort<string>();
h.addEdge('parse', 'compile');
h.addEdge('compile', 'link');
h.addNode('bench'); // 独立节点，序号在链尾之后
console.log('重排前:', h.order());
const r3 = h.addEdge('bench', 'compile'); // 后向边，无环
if (r3.ok) {
  console.log('接受。受影响重排节点:', r3.affected);
}
console.log('重排后:', h.order());

console.log('\n=== 场景四：并发批量提交（全有或全无） ===');
const batch = g.tryAddEdges([
  [2, 4],
  [4, 5],
  [5, 1], // 成环 -> 整批回滚
]);
if (!batch.ok) {
  console.log(`批次第 ${batch.failedIndex} 条边 ${batch.failedEdge.join('->')} 成环:`, batch.cycle.join(' -> '));
  console.log('整批已回滚，拓扑序:', g.order());
}
