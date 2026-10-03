import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Graph } from '../src/graph.ts';
import { maximalCliques, cliqueKey } from '../src/bronKerbosch.ts';
import { DynamicCliqueTracker } from '../src/dynamicCliques.ts';
import { decomposeCliqueTree, satisfiesRunningIntersection } from '../src/cliqueTree.ts';

function sortedKeys(cliques: number[][]): string[] {
  return cliques.map(cliqueKey).sort();
}

test('Bron-Kerbosch 主元剪枝：教科书样例图', () => {
  const g = new Graph();
  const edges: Array<[number, number]> = [
    [1, 2], [1, 5], [2, 3], [2, 5], [3, 4], [4, 5], [4, 6],
  ];
  for (const [u, v] of edges) g.addEdge(u, v);
  assert.deepEqual(sortedKeys(maximalCliques(g)), [
    '1,2,5', '2,3', '3,4', '4,5', '4,6',
  ]);
});

test('与暴力枚举对照：随机图上的极大团完全一致', () => {
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let trial = 0; trial < 30; trial++) {
    const n = 8;
    const g = new Graph();
    for (let i = 0; i < n; i++) g.addNode(i);
    for (let i = 0; i < n; i++)
      for (let j = i + 1; j < n; j++)
        if (rand() < 0.4) g.addEdge(i, j);

    // 暴力：枚举全部子集，筛出极大团
    const expected: string[] = [];
    for (let mask = 1; mask < (1 << n); mask++) {
      const subset: number[] = [];
      for (let i = 0; i < n; i++) if (mask & (1 << i)) subset.push(i);
      const isClique = subset.every((a) =>
        subset.every((b) => a === b || g.hasEdge(a, b)));
      if (!isClique) continue;
      const isMaximal = g.nodes().every((v) =>
        subset.includes(v) || !subset.every((w) => g.hasEdge(v, w)));
      if (isMaximal) expected.push(cliqueKey(subset));
    }
    assert.deepEqual(sortedKeys(maximalCliques(g)), expected.sort());
  }
});

test('社交网络紧密圈子发现：两个共享成员的圈子', () => {
  const tracker = new DynamicCliqueTracker([
    [1, 2], [1, 3], [1, 4], [2, 3], [2, 4], [3, 4], // 圈子 A: K4
    [4, 5], [4, 6], [5, 6],                          // 圈子 B: K3（节点4为桥梁）
  ]);
  assert.deepEqual(sortedKeys(tracker.getMaximalCliques()), ['1,2,3,4', '4,5,6']);
  // 包含节点 4 的全部极大团
  assert.deepEqual(sortedKeys(tracker.cliquesContaining([4])), ['1,2,3,4', '4,5,6']);
  // 同时包含 4 和 5 的只有圈子 B
  assert.deepEqual(sortedKeys(tracker.cliquesContaining([4, 5])), ['4,5,6']);
});

test('加入一条边促成 5 节点完全子图：新极大团诞生、被包含子团消解', () => {
  const tracker = new DynamicCliqueTracker([
    [1, 2], [1, 3], [1, 4], [2, 3], [2, 4], [3, 4], // K4
    [1, 5], [2, 5], [3, 5],                          // 节点5与1,2,3相连（缺 4-5）
  ]);
  assert.deepEqual(sortedKeys(tracker.getMaximalCliques()), ['1,2,3,4', '1,2,3,5']);

  const delta = tracker.addEdge(4, 5);
  assert.deepEqual(sortedKeys(delta.born), ['1,2,3,4,5']);
  assert.deepEqual(sortedKeys(delta.dissolved), ['1,2,3,4', '1,2,3,5']);
  assert.deepEqual(sortedKeys(tracker.getMaximalCliques()), ['1,2,3,4,5']);
  assert.deepEqual(sortedKeys(tracker.cliquesContaining([1, 5])), ['1,2,3,4,5']);
});

test('删除边后正确退化：K5 退回两个极大团，再删边继续分裂', () => {
  const tracker = new DynamicCliqueTracker([
    [1, 2], [1, 3], [1, 4], [1, 5], [2, 3], [2, 4],
    [2, 5], [3, 4], [3, 5], [4, 5], // K5
  ]);
  assert.deepEqual(sortedKeys(tracker.getMaximalCliques()), ['1,2,3,4,5']);

  // 删除 4-5：K5 消解，退化为 {1,2,3,4} 与 {1,2,3,5}
  const d1 = tracker.removeEdge(4, 5);
  assert.deepEqual(sortedKeys(d1.dissolved), ['1,2,3,4,5']);
  assert.deepEqual(sortedKeys(d1.born), ['1,2,3,4', '1,2,3,5']);
  assert.deepEqual(sortedKeys(tracker.getMaximalCliques()), ['1,2,3,4', '1,2,3,5']);

  // 再删 2-3：{1,2,3,4} 退化为 {1,2,4}+{1,3,4}，{1,2,3,5} 退化为 {1,2,5}+{1,3,5}
  const d2 = tracker.removeEdge(2, 3);
  assert.deepEqual(sortedKeys(d2.dissolved), ['1,2,3,4', '1,2,3,5']);
  assert.deepEqual(sortedKeys(d2.born), ['1,2,4', '1,2,5', '1,3,4', '1,3,5']);
  assert.deepEqual(sortedKeys(tracker.getMaximalCliques()),
    ['1,2,4', '1,2,5', '1,3,4', '1,3,5']);
});

test('团树分解：最大生成树边权与运行交性质', () => {
  const cliques = [[1, 2, 3], [2, 3, 4], [3, 4, 5]];
  const tree = decomposeCliqueTree(cliques);
  assert.equal(tree.nodes.length, 3);
  assert.equal(tree.edges.length, 2);
  assert.deepEqual(tree.edges.map((e) => e.weight).sort(), [2, 2]);
  assert.ok(satisfiesRunningIntersection(tree));
});

test('团树分解：动态图演化后仍满足运行交性质', () => {
  const tracker = new DynamicCliqueTracker([
    [1, 2], [1, 3], [2, 3], // {1,2,3}
    [3, 4], [3, 5], [4, 5], // {3,4,5}
    [5, 6], [5, 7], [6, 7], // {5,6,7}
  ]);
  tracker.addEdge(2, 4); // 连接两个圈子
  const cliques = tracker.getMaximalCliques();
  const tree = decomposeCliqueTree(cliques);
  assert.equal(tree.edges.length, cliques.length - 1);
  assert.ok(satisfiesRunningIntersection(tree));
  // 分离集边权非负且不超过任一端团的大小
  for (const e of tree.edges) {
    assert.ok(e.weight > 0);
    assert.ok(e.weight <= Math.min(tree.nodes[e.a].length, tree.nodes[e.b].length));
  }
});
