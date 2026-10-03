import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Graph } from '../src/graph.ts';
import { DynamicCliqueTracker } from '../src/tracker.ts';
import { buildCliqueTree, maximumCardinalitySearch } from '../src/cliqueTree.ts';

const build = (edges: [number, number][]): Graph => {
  const g = new Graph();
  for (const [u, v] of edges) g.addEdge(u, v);
  return g;
};

test('C4 非弦图，MCS 判定失败', () => {
  const g = build([[1, 2], [2, 3], [3, 4], [4, 1]]);
  assert.equal(maximumCardinalitySearch(g).chordal, false);
  assert.equal(buildCliqueTree(g).chordal, false);
});

test('弦图：两个共享边的三角形生成正确团树', () => {
  // 极大团 {0,1,2} 与 {1,2,3}，分隔集 {1,2}
  const g = build([[0, 1], [0, 2], [1, 2], [1, 3], [2, 3]]);
  const tracker = new DynamicCliqueTracker([[0, 1], [0, 2], [1, 2], [1, 3], [2, 3]]);
  const tree = buildCliqueTree(g, tracker.maximalCliques());
  assert.equal(tree.chordal, true);
  assert.equal(tree.nodes.length, 2);
  assert.equal(tree.edges.length, 1);
  assert.deepEqual(tree.edges[0].separator, [1, 2]);
});

test('团树满足运行交性质（running intersection）', () => {
  // 链式三角形：{0,1,2}-{2,3,4}-{4,5,6}，弦图
  const edges: [number, number][] = [
    [0, 1], [0, 2], [1, 2],
    [2, 3], [2, 4], [3, 4],
    [4, 5], [4, 6], [5, 6],
  ];
  const g = build(edges);
  const tree = buildCliqueTree(g);
  assert.equal(tree.chordal, true);
  assert.equal(tree.nodes.length, 3);

  // 对每个顶点，包含它的袋在树中构成连通子树
  const adj = new Map<number, number[]>();
  for (const e of tree.edges) {
    (adj.get(e.from) ?? adj.set(e.from, []).get(e.from)!).push(e.to);
    (adj.get(e.to) ?? adj.set(e.to, []).get(e.to)!).push(e.from);
  }
  const vertices = new Set(tree.nodes.flatMap((n) => n.bag));
  for (const v of vertices) {
    const holders = tree.nodes.filter((n) => n.bag.includes(v)).map((n) => n.id);
    const seen = new Set<number>([holders[0]]);
    const stack = [holders[0]];
    while (stack.length) {
      const cur = stack.pop()!;
      for (const nb of adj.get(cur) ?? []) {
        if (!seen.has(nb) && holders.includes(nb)) {
          seen.add(nb);
          stack.push(nb);
        }
      }
    }
    assert.equal(seen.size, holders.length, `顶点 ${v} 的袋不连通`);
  }
});
