import { test } from "node:test";
import assert from "node:assert/strict";
import { DagReachabilityIndex } from "../src/reachability-index.ts";
import { CompressedBitmap } from "../src/compressed-bitmap.ts";

function buildLayeredDag(): DagReachabilityIndex {
  const index = new DagReachabilityIndex();
  index.addEdge("L0-a", "L1-a");
  index.addEdge("L0-a", "L1-b");
  index.addEdge("L0-b", "L1-b");
  index.addEdge("L1-a", "L2-a");
  index.addEdge("L1-b", "L2-a");
  index.addEdge("L1-b", "L2-b");
  index.addEdge("L2-a", "L3-a");
  index.addEdge("L2-b", "L3-a");
  return index;
}

test("构建层级 DAG：拓扑层级按最长路径分层", () => {
  const index = buildLayeredDag();
  assert.equal(index.levelOf("L0-a"), 0);
  assert.equal(index.levelOf("L0-b"), 0);
  assert.equal(index.levelOf("L1-a"), 1);
  assert.equal(index.levelOf("L1-b"), 1);
  assert.equal(index.levelOf("L2-a"), 2);
  assert.equal(index.levelOf("L2-b"), 2);
  assert.equal(index.levelOf("L3-a"), 3);
  assert.deepEqual(index.layers(), [
    ["L0-a", "L0-b"],
    ["L1-a", "L1-b"],
    ["L2-a", "L2-b"],
    ["L3-a"],
  ]);
});

test("层级 DAG 上的传递可达性查询", () => {
  const index = buildLayeredDag();
  assert.equal(index.reachable("L0-a", "L3-a"), true);
  assert.equal(index.reachable("L0-b", "L3-a"), true);
  assert.equal(index.reachable("L0-b", "L2-a"), true);
  assert.equal(index.reachable("L0-b", "L1-a"), false);
  assert.equal(index.reachable("L3-a", "L0-a"), false);
  assert.equal(index.reachable("L1-a", "L2-b"), false);
  assert.equal(index.reachable("L0-a", "L0-a"), true);
});

test("添加跨层边后，全部祖先的下游可达集被位运算并行更新", () => {
  const index = buildLayeredDag();
  index.addNode("L4-x");
  index.addEdge("L3-a", "L4-x");
  assert.equal(index.reachable("L0-b", "L4-x"), true);

  index.addEdge("L0-b", "L3-a");
  assert.equal(index.reachable("L0-b", "L4-x"), true);

  index.addNode("L1-x");
  index.addEdge("L1-x", "L2-b");
  index.addEdge("L0-b", "L1-x");
  assert.equal(index.reachable("L0-b", "L2-b"), true);
  assert.equal(index.reachable("L0-b", "L3-a"), true);
  assert.equal(index.reachable("L1-x", "L4-x"), true);
  assert.equal(index.reachable("L1-a", "L2-b"), false);
});

test("跨层边使目标节点层级提升并向下游传播", () => {
  const index = new DagReachabilityIndex();
  index.addEdge("a", "b");
  index.addEdge("b", "c");
  index.addEdge("x", "y");
  assert.equal(index.levelOf("y"), 1);
  index.addEdge("c", "y");
  assert.equal(index.levelOf("y"), 3);
  index.addEdge("y", "z");
  assert.equal(index.levelOf("z"), 4);
});

test("拒绝成环边与自环", () => {
  const index = buildLayeredDag();
  assert.throws(() => index.addEdge("L3-a", "L0-a"), /cycle/);
  assert.throws(() => index.addEdge("L2-a", "L1-a"), /cycle/);
  assert.throws(() => index.addEdge("L1-a", "L1-a"), /cycle/);
});

test("重复边插入幂等", () => {
  const index = buildLayeredDag();
  const before = index.memoryBytes();
  index.addEdge("L0-a", "L1-a");
  assert.equal(index.memoryBytes(), before);
  assert.equal(index.reachable("L0-a", "L3-a"), true);
});

test("压缩位图：稀疏集合内存严格受非零块约束", () => {
  const bitmap = new CompressedBitmap();
  bitmap.set(3);
  bitmap.set(300);
  assert.equal(bitmap.chunkCount(), 2);
  assert.equal(bitmap.has(3), true);
  assert.equal(bitmap.has(300), true);
  assert.equal(bitmap.has(4), false);
  assert.equal(bitmap.byteSize(), 2 * (8 * 4 + 16));

  const other = new CompressedBitmap();
  other.set(3);
  other.set(5000);
  assert.equal(bitmap.unionFrom(other), true);
  assert.equal(bitmap.has(5000), true);
  assert.equal(bitmap.unionFrom(other), false);
  assert.equal(bitmap.count(), 3);
});

test("内存占用受压缩算法约束：稀疏 DAG 上远低于稠密矩阵", () => {
  const index = new DagReachabilityIndex();
  const components = 128;
  const chainLength = 8;
  for (let c = 0; c < components; c++) {
    for (let i = 0; i < chainLength - 1; i++) {
      index.addEdge(`c${c}-n${i}`, `c${c}-n${i + 1}`);
    }
  }
  index.addEdge("c0-n7", "c1-n0");
  index.addEdge("c2-n7", "c3-n0");

  const n = index.nodeCount();
  const used = index.memoryBytes();
  const dense = index.denseMatrixBytes();
  assert.equal(n, components * chainLength);
  assert.ok(
    used * 2 <= dense,
    `compressed ${used} should be at most half of dense ${dense}`,
  );
  assert.ok(index.reachable("c0-n0", "c1-n7"));
  assert.ok(!index.reachable("c1-n0", "c0-n0"));
  assert.ok(!index.reachable("c5-n0", "c6-n0"));
});

test("随机 DAG 模糊测试：与 BFS 基线逐对一致", () => {
  const rng = mulberry32(42);
  const n = 120;
  const index = new DagReachabilityIndex();
  const adj: number[][] = Array.from({ length: n }, () => []);
  for (let i = 0; i < n; i++) index.addNode(`v${i}`);

  const edges: Array<[number, number]> = [];
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (rng() < 0.03) edges.push([i, j]);
    }
  }
  shuffle(edges, rng);
  for (const [u, v] of edges) {
    index.addEdge(`v${u}`, `v${v}`);
    adj[u].push(v);
  }

  const crossLayer: Array<[number, number]> = [];
  for (let k = 0; k < 40; k++) {
    const u = Math.floor(rng() * (n - 2));
    const v = u + 2 + Math.floor(rng() * (n - u - 2));
    crossLayer.push([u, v]);
  }
  for (const [u, v] of crossLayer) {
    index.addEdge(`v${u}`, `v${v}`);
    adj[u].push(v);
  }

  const closure = bfsClosure(adj, n);
  for (let u = 0; u < n; u++) {
    for (let v = 0; v < n; v++) {
      assert.equal(
        index.reachable(`v${u}`, `v${v}`),
        closure[u].has(v),
        `reachability mismatch for (${u}, ${v})`,
      );
    }
  }
});

test("单次查询为 O(1) 位测试：大规模图上查询耗时不随图规模增长", () => {
  const index = new DagReachabilityIndex();
  const n = 2000;
  for (let i = 0; i < n - 1; i++) index.addEdge(`c${i}`, `c${i + 1}`);
  const iterations = 200000;
  const start = process.hrtime.bigint();
  let hits = 0;
  for (let k = 0; k < iterations; k++) {
    if (index.reachable(`c${k % 100}`, `c${n - 1 - (k % 100)}`)) hits++;
  }
  const elapsedMs = Number(process.hrtime.bigint() - start) / 1e6;
  assert.ok(hits > 0);
  assert.ok(elapsedMs < 1000, `${iterations} queries took ${elapsedMs}ms`);
});

function bfsClosure(adj: number[][], n: number): Set<number>[] {
  const closure: Set<number>[] = Array.from({ length: n }, () => new Set());
  for (let s = 0; s < n; s++) {
    closure[s].add(s);
    const queue = [s];
    while (queue.length > 0) {
      const cur = queue.shift()!;
      for (const next of adj[cur]) {
        if (!closure[s].has(next)) {
          closure[s].add(next);
          queue.push(next);
        }
      }
    }
  }
  return closure;
}

function mulberry32(seed: number): () => number {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function shuffle<T>(arr: T[], rng: () => number): void {
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
}
