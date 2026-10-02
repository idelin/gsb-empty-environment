import { test } from "node:test";
import assert from "node:assert/strict";
import { DAGReachabilityIndex, CycleError } from "../src/reachability-index.ts";
import { CompressedBitmap } from "../src/compressed-bitmap.ts";

/** 参考实现：BFS 逐点求可达，用于对照 */
function referenceReachable(nodes: string[], edges: Array<[string, string]>): Set<string> {
  const adj = new Map<string, string[]>();
  for (const n of nodes) adj.set(n, []);
  for (const [u, v] of edges) adj.get(u)!.push(v);
  const pairs = new Set<string>();
  for (const src of nodes) {
    const seen = new Set([src]);
    const stack = [src];
    while (stack.length > 0) {
      const x = stack.pop()!;
      pairs.add(`${src}->${x}`);
      for (const y of adj.get(x)!) {
        if (!seen.has(y)) {
          seen.add(y);
          stack.push(y);
        }
      }
    }
  }
  return pairs;
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("压缩位图：基本位运算与稀疏存储", () => {
  const a = CompressedBitmap.from([0, 5, 64, 130]);
  assert.ok(a.test(0) && a.test(5) && a.test(64) && a.test(130));
  assert.ok(!a.test(1) && !a.test(63) && !a.test(129));
  assert.equal(a.cardinality(), 4);
  assert.equal(a.chunkCount, 3); // 0..63 / 64..127 / 128..191 三块

  const b = CompressedBitmap.from([5, 200]);
  assert.ok(a.orInPlace(b));
  assert.ok(a.test(200));
  assert.ok(!a.orInPlace(b)); // 重复合并不再变化

  const diff = a.subtract(CompressedBitmap.from([0, 5, 200]));
  assert.deepEqual([...diff.indices()], [64, 130]);
});

test("构建层级 DAG：拓扑分层正确", () => {
  const idx = new DAGReachabilityIndex();
  // 四层结构
  const edges: Array<[string, string]> = [
    ["a0", "b0"], ["a0", "b1"], ["a1", "b1"], ["a1", "b2"], ["a2", "b2"],
    ["b0", "c0"], ["b1", "c0"], ["b1", "c1"], ["b2", "c1"], ["b2", "c2"],
    ["c0", "d0"], ["c1", "d0"], ["c2", "d1"],
  ];
  for (const [u, v] of edges) idx.addEdge(u, v);
  idx.build();

  assert.equal(idx.levelOf("a0"), 0);
  assert.equal(idx.levelOf("b1"), 1);
  assert.equal(idx.levelOf("c0"), 2);
  assert.equal(idx.levelOf("d0"), 3);
  assert.equal(idx.levelOf("d1"), 3);
  assert.equal(idx.layersSnapshot().length, 4);
});

test("传递闭包：任意节点对 O(1) 查询结果正确", () => {
  const idx = new DAGReachabilityIndex();
  const edges: Array<[string, string]> = [
    ["a0", "b0"], ["a0", "b1"], ["a1", "b1"], ["a1", "b2"], ["a2", "b2"],
    ["b0", "c0"], ["b1", "c0"], ["b1", "c1"], ["b2", "c1"], ["b2", "c2"],
    ["c0", "d0"], ["c1", "d0"], ["c2", "d1"],
  ];
  for (const [u, v] of edges) idx.addEdge(u, v);

  // 跨层传递可达
  assert.ok(idx.isReachable("a0", "d0"));
  assert.ok(idx.isReachable("a1", "d0"));
  assert.ok(idx.isReachable("a2", "d1"));
  assert.ok(idx.isReachable("a2", "d0")); // a2 -> b2 -> c1 -> d0
  // 不可达
  assert.ok(!idx.isReachable("a0", "d1"));
  assert.ok(!idx.isReachable("b0", "c2"));
  assert.ok(!idx.isReachable("d0", "a0"));
  // 自反（节点可达自身）
  assert.ok(idx.isReachable("a0", "a0"));
  // 未知节点
  assert.ok(!idx.isReachable("a0", "zzz"));
});

test("跨层边插入：位运算增量更新下游可达集", () => {
  const idx = new DAGReachabilityIndex();
  const edges: Array<[string, string]> = [
    ["a0", "b0"], ["a1", "b0"], ["a1", "b1"],
    ["b0", "c0"], ["b1", "c1"],
    ["c0", "d0"], ["c1", "d1"],
  ];
  for (const [u, v] of edges) idx.addEdge(u, v);
  idx.build();

  const before = new Set(idx.reachableFrom("a0"));
  assert.ok(!idx.isReachable("a0", "d1"));
  assert.ok(!idx.isReachable("b0", "c1"));

  // 跨层边 b0 -> c1（跳过一层）：b0 的祖先 a0/a1 都必须获得 c1 的下游
  idx.addEdge("b0", "c1");

  assert.ok(idx.isReachable("b0", "c1"));
  assert.ok(idx.isReachable("b0", "d1"));
  assert.ok(idx.isReachable("a0", "c1"));
  assert.ok(idx.isReachable("a0", "d1"));
  assert.ok(idx.isReachable("a1", "d1"));
  // 未受影响的节点不变
  assert.ok(!idx.isReachable("c0", "d1"));
  assert.ok(!idx.isReachable("d0", "d1"));
  // 原有可达集全部保留
  for (const label of before) assert.ok(idx.isReachable("a0", label));
});

test("环检测：插入回边抛出 CycleError", () => {
  const idx = new DAGReachabilityIndex();
  idx.addEdge("x", "y");
  idx.addEdge("y", "z");
  idx.build();
  assert.throws(() => idx.addEdge("z", "x"), CycleError);
  assert.throws(() => idx.addEdge("x", "x"), CycleError);
  // 未构建时的环检测（DFS 路径）
  const idx2 = new DAGReachabilityIndex();
  idx2.addEdge("p", "q");
  idx2.addEdge("q", "r");
  assert.throws(() => idx2.addEdge("r", "p"), CycleError);
});

test("随机 DAG：增量插边后与 BFS 参考实现全对偶一致", () => {
  const rand = mulberry32(20261003);
  const N = 40;
  const nodes = Array.from({ length: N }, (_, i) => `n${i}`);
  // 随机拓扑序，只允许从序小指向序大，保证无环
  const order = [...nodes].sort(() => rand() - 0.5);
  const rank = new Map(order.map((label, i) => [label, i]));

  const edges: Array<[string, string]> = [];
  for (let i = 0; i < N; i += 1) {
    for (let j = i + 1; j < N; j += 1) {
      if (rand() < 0.06) edges.push([order[i], order[j]]);
    }
  }

  const idx = new DAGReachabilityIndex();
  for (const n of nodes) idx.addNode(n);
  for (const [u, v] of edges) idx.addEdge(u, v);
  idx.build();

  const checkAll = () => {
    const ref = referenceReachable(nodes, edges);
    for (const u of nodes) {
      for (const v of nodes) {
        assert.equal(
          idx.isReachable(u, v),
          ref.has(`${u}->${v}`),
          `mismatch at ${u} -> ${v}`,
        );
      }
    }
  };
  checkAll();

  // 动态插入 40 条跨层边（保持无环），逐条增量更新后再次全量校验
  let inserted = 0;
  while (inserted < 40) {
    const i = Math.floor(rand() * N);
    const j = Math.floor(rand() * N);
    if (i >= j) continue;
    const [u, v] = [order[i], order[j]];
    if (edges.some(([a, b]) => a === u && b === v)) continue;
    edges.push([u, v]);
    idx.addEdge(u, v);
    inserted += 1;
  }
  checkAll();
});

test("内存约束：压缩位图显著小于稠密邻接矩阵", () => {
  const idx = new DAGReachabilityIndex();
  // 层级树状 DAG：6 层，扇出 4。位位置按层级聚集分配，
  // 每个节点的下游子树在位图上形成连续段，稀疏块压缩收益最大。
  const FANOUT = 4;
  const LAYERS = 6;
  const widths: number[] = [];
  let width = FANOUT;
  for (let l = 0; l < LAYERS; l += 1) {
    widths.push(width);
    width *= FANOUT;
  }
  const label = (l: number, i: number) => `L${l}n${i}`;
  for (let l = 0; l < LAYERS; l += 1) {
    for (let i = 0; i < widths[l]; i += 1) idx.addNode(label(l, i));
  }
  for (let l = 0; l < LAYERS - 1; l += 1) {
    for (let i = 0; i < widths[l]; i += 1) {
      for (let k = 0; k < FANOUT; k += 1) {
        idx.addEdge(label(l, i), label(l + 1, i * FANOUT + k));
      }
    }
  }
  idx.build();

  const stats = idx.stats();
  assert.equal(stats.nodes, widths.reduce((a, b) => a + b, 0));
  // 压缩后内存严格小于稠密矩阵的 1/4
  assert.ok(
    stats.bitmapBytes * 4 < stats.denseBytes,
    `bitmap=${stats.bitmapBytes}B dense=${stats.denseBytes}B`,
  );
});

test("查询性能：20 万次 O(1) 查询在 1 秒内完成", () => {
  const rand = mulberry32(99);
  const idx = new DAGReachabilityIndex();
  const N = 500;
  const nodes = Array.from({ length: N }, (_, i) => `n${i}`);
  for (const n of nodes) idx.addNode(n);
  for (let i = 0; i < N; i += 1) {
    for (let k = 0; k < 3; k += 1) {
      const j = i + 1 + Math.floor(rand() * (N - i - 1));
      if (j < N) idx.addEdge(nodes[i], nodes[j]);
    }
  }
  idx.build();

  // id 快路径与标签路径结果一致
  for (let q = 0; q < 1000; q += 1) {
    const u = nodes[Math.floor(rand() * N)];
    const v = nodes[Math.floor(rand() * N)];
    assert.equal(
      idx.isReachableById(idx.nodeId(u)!, idx.nodeId(v)!),
      idx.isReachable(u, v),
    );
  }

  const QUERIES = 200_000;
  const start = performance.now();
  let hits = 0;
  for (let q = 0; q < QUERIES; q += 1) {
    const u = nodes[Math.floor(rand() * N)];
    const v = nodes[Math.floor(rand() * N)];
    if (idx.isReachable(u, v)) hits += 1;
  }
  const elapsed = performance.now() - start;
  assert.ok(hits > 0);
  assert.ok(elapsed < 1000, `${QUERIES} queries took ${elapsed.toFixed(1)}ms`);
});
