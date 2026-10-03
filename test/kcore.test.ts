import { test } from "node:test";
import assert from "node:assert/strict";
import { Graph } from "../src/graph.ts";
import { KCoreDecomposition } from "../src/kcore.ts";
import {
  buildAcceptanceGraph,
  EXPECTED_CORES,
} from "../examples/acceptance-graph.ts";

function assertCoresEqual(
  actual: ReadonlyMap<number, number>,
  expected: ReadonlyMap<number, number>,
): void {
  const a = [...actual.entries()].sort((x, y) => x[0] - y[0]);
  const e = [...expected.entries()].sort((x, y) => x[0] - y[0]);
  assert.deepEqual(a, e);
}

function snapshot(kc: KCoreDecomposition): Map<number, number> {
  return new Map(kc.coreNumbers());
}

test("全量分解：密集子网 + 悬挂链的核度正确", () => {
  const kc = new KCoreDecomposition(buildAcceptanceGraph());
  assertCoresEqual(kc.coreNumbers(), EXPECTED_CORES);
  assert.equal(kc.maxCore(), 4);
});

test("分层输出：1-core 到 4-core 子图逐层嵌套且满足最小度约束", () => {
  const kc = new KCoreDecomposition(buildAcceptanceGraph());

  const layers = kc.layers();
  assert.deepEqual(layers.get(4), [0, 1, 2, 3, 4]);
  assert.deepEqual(layers.get(3), [5, 6, 7, 8]);
  assert.deepEqual(layers.get(2), [9, 10, 11, 12]);
  assert.deepEqual(layers.get(1), [13, 14, 15]);

  const sizes = [1, 2, 3, 4].map((k) => kc.kCore(k).nodes.length);
  assert.deepEqual(sizes, [16, 13, 9, 5]); // 逐层嵌套收缩

  // 每个 k-core 子图内，每个节点的子图内度数 >= k
  for (let k = 1; k <= 4; k++) {
    const { nodes, edges } = kc.kCore(k);
    const deg = new Map<number, number>(nodes.map((v) => [v, 0]));
    for (const [u, v] of edges) {
      deg.set(u, deg.get(u)! + 1);
      deg.set(v, deg.get(v)! + 1);
    }
    for (const v of nodes) {
      assert.ok(deg.get(v)! >= k, `节点 ${v} 在 ${k}-core 中度数不足`);
    }
  }
});

test("增量加边：桥接边 (9,10) 使局部核度 2 -> 3，开销远小于全量重算", () => {
  const kc = new KCoreDecomposition(buildAcceptanceGraph());
  const full = kc.lastFullDecomposeStats;

  const stats = kc.addEdge(9, 10);
  assert.equal(stats.kind, "insert");
  assert.equal(stats.k, 2);
  assert.deepEqual([9, 10, 11, 12].map((v) => kc.coreNumber(v)), [3, 3, 3, 3]);

  // 局部性：只访问缺边 gadget 的 4 个节点，远小于全图 16 个节点
  assert.equal(stats.visited, 4);
  assert.equal(stats.changed, 4);
  assert.ok(stats.visited * 3 <= full.vertices, "局部更新开销应远小于全量重算");

  // 与全量重算结果一致
  const inc = snapshot(kc);
  kc.recompute();
  assertCoresEqual(kc.coreNumbers(), inc);
});

test("增量删边：删除 K5 内边使局部核度 4 -> 3，且与全量重算一致", () => {
  const kc = new KCoreDecomposition(buildAcceptanceGraph());
  const stats = kc.removeEdge(0, 1);
  assert.equal(stats.kind, "remove");
  assert.equal(stats.k, 4);
  assert.equal(stats.visited, 5); // 只触及 K5 的 5 个节点
  assert.equal(stats.changed, 5);
  assert.deepEqual([0, 1, 2, 3, 4].map((v) => kc.coreNumber(v)), [3, 3, 3, 3, 3]);
  assert.equal(kc.coreNumber(5), 3); // K4 子网不受影响

  const inc = snapshot(kc);
  kc.recompute();
  assertCoresEqual(kc.coreNumbers(), inc);
});

test("边界：重复边、不存在的边、新节点均为幂等/安全操作", () => {
  const g = new Graph();
  const kc = new KCoreDecomposition(g);

  assert.equal(kc.addEdge(100, 200).kind, "insert"); // 新节点
  assert.equal(kc.coreNumber(100), 1);
  assert.equal(kc.coreNumber(200), 1);

  assert.equal(kc.addEdge(100, 200).kind, "noop"); // 重复边
  assert.equal(kc.removeEdge(100, 201).kind, "noop"); // 不存在的边

  kc.addEdge(200, 300);
  kc.addEdge(100, 300); // 三角形，三者核度 1 -> 2
  assert.deepEqual([100, 200, 300].map((v) => kc.coreNumber(v)), [2, 2, 2]);

  const inc = snapshot(kc);
  kc.recompute();
  assertCoresEqual(kc.coreNumbers(), inc);
});

test("差分测试：随机增删边后，增量维护结果始终等于全量重算", (t) => {
  // 确定性伪随机数（mulberry32）
  let seed = 0xC0FFEE;
  const rand = () => {
    seed |= 0;
    seed = (seed + 0x6D2B79F5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };

  // 模块化稀疏图：若干稠密簇 + 簇间桥边 + 悬挂树，贴近真实大图结构
  const N = 60;
  const g = new Graph();
  for (let i = 0; i < N; i++) g.addNode(i);
  const clusterSize = 6;
  const clusterCount = 7;
  for (let c = 0; c < clusterCount; c++) {
    const base = c * clusterSize;
    for (let i = 0; i < clusterSize; i++) {
      for (let j = i + 1; j < clusterSize; j++) {
        if (rand() < 0.6) g.addEdge(base + i, base + j);
      }
    }
    if (c > 0) g.addEdge(base - 1, base); // 簇间桥边
  }
  for (let t = clusterCount * clusterSize; t < N; t++) {
    g.addEdge(t, Math.floor(rand() * t)); // 悬挂树节点
  }
  const kc = new KCoreDecomposition(g);

  let totalVisited = 0;
  let ops = 0;
  for (let step = 0; step < 400; step++) {
    const u = Math.floor(rand() * N);
    const v = Math.floor(rand() * N);
    if (u === v) continue;
    const stats = g.hasEdge(u, v) ? kc.removeEdge(u, v) : kc.addEdge(u, v);
    if (stats.kind === "noop") continue;
    ops++;
    totalVisited += stats.visited;

    const inc = snapshot(kc);
    kc.recompute();
    assertCoresEqual(kc.coreNumbers(), inc);
  }
  assert.ok(ops > 300, `应执行足够多有效操作，实际 ${ops}`);
  // 局部性：平均每次增量更新访问的节点数应显著小于全量重算（每次 N 个节点）
  const avgVisited = totalVisited / ops;
  t.diagnostic(
    `有效操作 ${ops} 次，平均局部区域 ${avgVisited.toFixed(2)} / 全图 ${N} 节点`,
  );
  assert.ok(
    totalVisited < ops * N,
    `增量更新总开销应小于逐次全量重算: ${totalVisited} vs ${ops * N}`,
  );
});
