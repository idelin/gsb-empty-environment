import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Graph } from '../src/graph.ts';
import { listMaximalCliques } from '../src/bronKerbosch.ts';

const build = (edges: [number, number][]): Graph => {
  const g = new Graph();
  for (const [u, v] of edges) g.addEdge(u, v);
  return g;
};

const asSets = (cliques: number[][]): Set<string> =>
  new Set(cliques.map((c) => [...c].sort((a, b) => a - b).join(',')));

test('空图没有极大团', () => {
  assert.deepEqual(listMaximalCliques(new Graph()), []);
});

test('三角形加悬挂边：极大团为三角形与边', () => {
  const g = build([[1, 2], [2, 3], [1, 3], [3, 4]]);
  assert.deepEqual(
    asSets(listMaximalCliques(g)),
    new Set(['1,2,3', '3,4']),
  );
});

test('K4 只有一个极大团', () => {
  const g = build([[1, 2], [1, 3], [1, 4], [2, 3], [2, 4], [3, 4]]);
  assert.deepEqual(listMaximalCliques(g), [[1, 2, 3, 4]]);
});

test('两个共享顶点的三角形', () => {
  const g = build([[1, 2], [2, 3], [1, 3], [3, 4], [4, 5], [3, 5]]);
  assert.deepEqual(
    asSets(listMaximalCliques(g)),
    new Set(['1,2,3', '3,4,5']),
  );
});

test('限制候选顶点集合时只枚举诱导子图', () => {
  const g = build([[1, 2], [2, 3], [1, 3], [3, 4]]);
  assert.deepEqual(asSets(listMaximalCliques(g, [1, 2])), new Set(['1,2']));
});

test('与暴力法对拍：随机小图的极大团一致', () => {
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) % 2 ** 31) / 2 ** 31;
  for (let trial = 0; trial < 50; trial++) {
    const n = 2 + Math.floor(rand() * 7);
    const g = new Graph();
    for (let v = 0; v < n; v++) g.addVertex(v);
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        if (rand() < 0.5) g.addEdge(i, j);
      }
    }
    // 暴力：枚举所有子集，找出“是团且不被任何团真包含”的
    const subsets: number[][] = [];
    for (let mask = 1; mask < 1 << n; mask++) {
      const s: number[] = [];
      for (let v = 0; v < n; v++) if (mask & (1 << v)) s.push(v);
      if (g.isClique(s)) subsets.push(s);
    }
    const brute = subsets.filter(
      (s) => !subsets.some((t) => t.length > s.length && s.every((x) => t.includes(x))),
    );
    assert.deepEqual(asSets(listMaximalCliques(g)), asSets(brute));
  }
});
