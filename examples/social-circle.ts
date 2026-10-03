/**
 * 运行样例：社交网络紧密圈子发现。
 *
 * 场景：5 位用户 0..4，除 (0,4) 外两两互为好友。
 * 初始存在两个紧密圈子（极大团）；当 0 与 4 成为好友（加边）后，
 * 5 人构成完全子图 K5 —— 新极大团诞生，两个旧圈子被消解；
 * 随后 0 与 4 解除好友（删边），网络正确退化回两个圈子。
 */
import { DynamicCliqueTracker } from '../src/tracker.ts';
import { buildCliqueTree } from '../src/cliqueTree.ts';

const fmt = (c: number[]) => `{${c.join(', ')}}`;
const fmtAll = (cs: number[][]) => cs.map(fmt).join('  ');

const edges: [number, number][] = [];
for (let i = 0; i < 5; i++) {
  for (let j = i + 1; j < 5; j++) {
    if (!(i === 0 && j === 4)) edges.push([i, j]);
  }
}

const tracker = new DynamicCliqueTracker(edges);
console.log('初始极大团:', fmtAll(tracker.maximalCliques()));
console.log('包含用户 1,2 的圈子:', fmtAll(tracker.maximalCliquesContaining([1, 2])));

console.log('\n--- 加边 (0, 4)：0 与 4 成为好友 ---');
const add = tracker.addEdge(0, 4);
console.log('新诞生的极大团:', fmtAll(add.born));
console.log('被消解的旧团  :', fmtAll(add.dissolved));
console.log('当前极大团    :', fmtAll(tracker.maximalCliques()));

const tree = buildCliqueTree(tracker.graph, tracker.maximalCliques());
console.log('团树分解      : 弦图 =', tree.chordal,
  '| 袋 =', fmtAll(tree.nodes.map((n) => n.bag)));

console.log('\n--- 删边 (0, 4)：0 与 4 解除好友 ---');
const del = tracker.removeEdge(0, 4);
console.log('新诞生的极大团:', fmtAll(del.born));
console.log('被消解的旧团  :', fmtAll(del.dissolved));
console.log('当前极大团    :', fmtAll(tracker.maximalCliques()));
