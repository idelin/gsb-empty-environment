import { DynamicCliqueTracker } from '../src/dynamicCliques.ts';
import { decomposeCliqueTree, satisfiesRunningIntersection } from '../src/cliqueTree.ts';

const fmt = (c: number[]) => `{${c.join(', ')}}`;
const fmtAll = (cs: number[][]) => cs.map(fmt).join('  ') || '(无)';

// 场景：小型社交网络，节点 1-4 是一个紧密圈子(K4)，节点 5 与 1,2,3 相识
const tracker = new DynamicCliqueTracker([
  [1, 2], [1, 3], [1, 4], [2, 3], [2, 4], [3, 4],
  [1, 5], [2, 5], [3, 5],
]);

console.log('== 初始社交网络 ==');
console.log('极大团（紧密圈子）:', fmtAll(tracker.getMaximalCliques()));
console.log('包含节点 {1, 3} 的极大团:', fmtAll(tracker.cliquesContaining([1, 3])));
console.log('包含节点 {4, 5} 的极大团:', fmtAll(tracker.cliquesContaining([4, 5])));

console.log('\n== 加入边 (4, 5)：促成 5 节点完全子图 ==');
const d1 = tracker.addEdge(4, 5);
console.log('新诞生的极大团:', fmtAll(d1.born));
console.log('被消解的子团  :', fmtAll(d1.dissolved));
console.log('当前极大团    :', fmtAll(tracker.getMaximalCliques()));

console.log('\n== 删除边 (4, 5)：完全子图退化 ==');
const d2 = tracker.removeEdge(4, 5);
console.log('新诞生的极大团:', fmtAll(d2.born));
console.log('被消解的团    :', fmtAll(d2.dissolved));
console.log('当前极大团    :', fmtAll(tracker.getMaximalCliques()));

console.log('\n== 团树分解 ==');
tracker.addEdge(5, 6);
tracker.addEdge(5, 7);
tracker.addEdge(6, 7); // 新增圈子 {5,6,7}
const cliques = tracker.getMaximalCliques();
console.log('当前极大团    :', fmtAll(cliques));
const tree = decomposeCliqueTree(cliques);
for (const e of tree.edges) {
  console.log(`树边 ${fmt(tree.nodes[e.a])} -- ${fmt(tree.nodes[e.b])}  (分离集大小 ${e.weight})`);
}
console.log('运行交性质成立:', satisfiesRunningIntersection(tree));
