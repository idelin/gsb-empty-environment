import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CoreMaintenance,
  Graph,
  coreLayers,
  coreNumbers,
  kCoreSubgraph,
  maxCore,
} from "../src/index.ts";

/** 确定性伪随机数（mulberry32），保证差分测试可复现。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function coresOf(cm: CoreMaintenance, nodes: number[]): number[] {
  return nodes.map((v) => cm.coreOf(v));
}

/** 验收场景图：K5 团 + K4 团 + K4-e 部件 + 悬挂链。 */
function acceptanceEdges(): [number, number][] {
  const edges: [number, number][] = [];
  for (let i = 0; i < 5; i += 1) {
    for (let j = i + 1; j < 5; j += 1) edges.push([i, j]); // 0-4: K5
  }
  edges.push([5, 6], [5, 7], [5, 8], [6, 7], [6, 8], [7, 8], [4, 5]); // 5-8: K4 挂到团上
  edges.push([9, 10], [9, 11], [9, 12], [10, 11], [10, 12]); // 9-12: K4 - e(11,12)
  edges.push([0, 13], [13, 14], [14, 15], [15, 16]); // 13-16: 悬挂链
  return edges;
}

test("批量分解：经典图形的核度", () => {
  // 路径图：全部 core 1
  const path = CoreMaintenance.fromEdges([[0, 1], [1, 2], [2, 3]]);
  assert.deepEqual([...path.coreNumbers()], [1, 1, 1, 1]);

  // 环：全部 core 2
  const cycle = CoreMaintenance.fromEdges([[0, 1], [1, 2], [2, 3], [3, 4], [4, 0]]);
  assert.deepEqual([...cycle.coreNumbers()], [2, 2, 2, 2, 2]);

  // K5 团：全部 core 4
  const clique: [number, number][] = [];
  for (let i = 0; i < 5; i += 1) {
    for (let j = i + 1; j < 5; j += 1) clique.push([i, j]);
  }
  const k5 = CoreMaintenance.fromEdges(clique);
  assert.deepEqual([...k5.coreNumbers()], [4, 4, 4, 4, 4]);

  // 星形图：全部 core 1
  const star = CoreMaintenance.fromEdges([[0, 1], [0, 2], [0, 3], [0, 4]]);
  assert.deepEqual([...star.coreNumbers()], [1, 1, 1, 1, 1]);

  // 孤立节点：core 0
  const g = new Graph();
  g.addNode(0);
  g.addNode(1);
  g.addEdge(0, 1);
  g.addNode(2);
  assert.deepEqual([...coreNumbers(g)], [1, 1, 0]);

  // K4 缺一条边：全部 core 2；补上后全部 core 3
  const k4e = CoreMaintenance.fromEdges([[0, 1], [0, 2], [0, 3], [1, 2], [1, 3]]);
  assert.deepEqual([...k4e.coreNumbers()], [2, 2, 2, 2]);
  k4e.insertEdge(2, 3);
  assert.deepEqual([...k4e.coreNumbers()], [3, 3, 3, 3]);
});

test("验收图：1-core 到 4-core 分层子图", () => {
  const cm = CoreMaintenance.fromEdges(acceptanceEdges());
  const core = cm.coreNumbers();
  assert.equal(maxCore(core), 4);

  const layers = coreLayers(core);
  assert.deepEqual(layers.get(1), [13, 14, 15, 16]); // 悬挂链
  assert.deepEqual(layers.get(2), [9, 10, 11, 12]); // K4-e 部件
  assert.deepEqual(layers.get(3), [5, 6, 7, 8]); // K4 团
  assert.deepEqual(layers.get(4), [0, 1, 2, 3, 4]); // K5 团

  const expect: Record<number, { nodes: number[]; edges: number }> = {
    1: { nodes: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16], edges: 26 },
    2: { nodes: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12], edges: 22 },
    3: { nodes: [0, 1, 2, 3, 4, 5, 6, 7, 8], edges: 17 },
    4: { nodes: [0, 1, 2, 3, 4], edges: 10 },
  };
  for (let k = 1; k <= 4; k += 1) {
    const sub = kCoreSubgraph(cm.graph, core, k);
    assert.deepEqual(sub.nodes, expect[k].nodes, `${k}-core 节点集`);
    assert.equal(sub.edges.length, expect[k].edges, `${k}-core 边数`);
  }
});

test("增量插边：桥接边使局部核度从 2 提升到 3", () => {
  const cm = CoreMaintenance.fromEdges(acceptanceEdges());
  const before = cm.coreNumbers();

  const stats = cm.insertEdge(11, 12);

  // 只有 K4-e 部件的 4 个节点核度变化：2 -> 3
  assert.deepEqual(stats.changed, [9, 10, 11, 12]);
  assert.deepEqual(coresOf(cm, [9, 10, 11, 12]), [3, 3, 3, 3]);
  assert.equal(stats.K, 2);

  // 其余节点核度不变
  for (let v = 0; v < before.length; v += 1) {
    if (v >= 9 && v <= 12) continue;
    assert.equal(cm.coreOf(v), before[v], `节点 ${v} 核度不应变化`);
  }

  // 局部更新只访问部件邻域，远小于全图
  assert.ok(stats.visited <= 4, `visited=${stats.visited} 应 <= 4`);
  assert.ok(stats.visited < cm.graph.nodeCount);

  // 与全局重算结果一致
  assert.deepEqual([...cm.coreNumbers()], [...coreNumbers(cm.graph)]);
});

test("增量删边：移除桥接边后核度回落到 2", () => {
  const cm = CoreMaintenance.fromEdges(acceptanceEdges());
  cm.insertEdge(11, 12);
  assert.deepEqual(coresOf(cm, [9, 10, 11, 12]), [3, 3, 3, 3]);

  const stats = cm.removeEdge(11, 12);
  assert.deepEqual(stats.changed, [9, 10, 11, 12]);
  assert.deepEqual(coresOf(cm, [9, 10, 11, 12]), [2, 2, 2, 2]);
  assert.deepEqual([...cm.coreNumbers()], [...coreNumbers(cm.graph)]);
});

test("增量删边：团内删边级联降级", () => {
  const clique: [number, number][] = [];
  for (let i = 0; i < 5; i += 1) {
    for (let j = i + 1; j < 5; j += 1) clique.push([i, j]);
  }
  const cm = CoreMaintenance.fromEdges(clique);
  assert.deepEqual([...cm.coreNumbers()], [4, 4, 4, 4, 4]);

  // K5 删一条边 -> K5-e，所有节点核度降为 3
  const stats = cm.removeEdge(0, 1);
  assert.deepEqual(stats.changed, [0, 1, 2, 3, 4]);
  assert.deepEqual([...cm.coreNumbers()], [3, 3, 3, 3, 3]);
  assert.deepEqual([...cm.coreNumbers()], [...coreNumbers(cm.graph)]);
});

test("幂等操作：重边与删除不存在的边不改变核度", () => {
  const cm = CoreMaintenance.fromEdges(acceptanceEdges());
  const before = cm.coreNumbers();

  const dup = cm.insertEdge(0, 1); // 已存在
  assert.equal(dup.updated, 0);
  const missing = cm.removeEdge(3, 10); // 不存在
  assert.equal(missing.updated, 0);

  assert.deepEqual([...cm.coreNumbers()], [...before]);
});

test("随机差分：增量维护与全量重算逐步一致", () => {
  for (const seed of [7, 42, 2024]) {
    const rand = mulberry32(seed);
    const n = 60;
    const cm = new CoreMaintenance();
    for (let i = 0; i < n; i += 1) cm.graph.addNode(i);
    cm.recomputeAll();

    for (let step = 0; step < 400; step += 1) {
      const u = Math.floor(rand() * n);
      let v = Math.floor(rand() * n);
      if (v === u) v = (v + 1) % n;
      if (rand() < 0.55) cm.insertEdge(u, v);
      else cm.removeEdge(u, v);

      const expected = coreNumbers(cm.graph);
      assert.deepEqual(
        [...cm.coreNumbers()],
        [...expected],
        `seed=${seed} step=${step} 增量结果与全量重算不一致`,
      );
      assert.equal(cm.maxCore, maxCore(expected));
    }
  }
});

test("大规模图：局部更新开销远小于全局重算", () => {
  const edges: [number, number][] = [];
  let next = 0;
  for (let c = 0; c < 200; c += 1) {
    const base = next;
    next += 8;
    for (let i = 0; i < 8; i += 1) {
      for (let j = i + 1; j < 8; j += 1) edges.push([base + i, base + j]);
    }
  }
  for (let c = 0; c < 100; c += 1) {
    edges.push([c * 8, next]);
    for (let i = 0; i < 99; i += 1) edges.push([next + i, next + i + 1]);
    next += 100;
  }
  const g = next;
  edges.push([g, g + 1], [g, g + 2], [g, g + 3], [g + 1, g + 2], [g + 1, g + 3]);

  const cm = CoreMaintenance.fromEdges(edges);
  const totalNodes = cm.graph.nodeCount;
  assert.equal(totalNodes, 11604);
  assert.deepEqual(coresOf(cm, [g, g + 1, g + 2, g + 3]), [2, 2, 2, 2]);

  // 桥接边：局部核度 2 -> 3
  const stats = cm.insertEdge(g + 2, g + 3);
  assert.deepEqual(stats.changed, [g, g + 1, g + 2, g + 3]);
  assert.deepEqual(coresOf(cm, [g, g + 1, g + 2, g + 3]), [3, 3, 3, 3]);

  // 局部开销：访问节点数仅为部件邻域，与图规模无关
  assert.ok(stats.visited <= 8, `visited=${stats.visited}`);
  assert.ok(
    stats.visited * 100 < totalNodes,
    `局部开销 ${stats.visited} 应远小于全局 ${totalNodes}`,
  );
  assert.deepEqual([...cm.coreNumbers()], [...coreNumbers(cm.graph)]);

  // 删边同样局部化
  const del = cm.removeEdge(g + 2, g + 3);
  assert.deepEqual(del.changed, [g, g + 1, g + 2, g + 3]);
  assert.ok(del.visited <= 8, `visited=${del.visited}`);
  assert.deepEqual([...cm.coreNumbers()], [...coreNumbers(cm.graph)]);
});
