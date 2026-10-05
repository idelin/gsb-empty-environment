import test from "node:test";
import assert from "node:assert/strict";
import {
  ThresholdClustering,
  type WeightedEdge,
} from "../src/threshold-clustering.ts";

// ---------- 参考实现：阈值子图 + BFS ----------

/** 在 "权重 <= threshold 的边构成的子图" 上 BFS 求连通分量（含孤点）。 */
function referenceClusters(
  n: number,
  edges: readonly WeightedEdge[],
  threshold: number,
): number[][] {
  const adj: number[][] = Array.from({ length: n }, () => []);
  for (const [u, v, w] of edges) {
    if (w <= threshold) {
      adj[u].push(v);
      adj[v].push(u);
    }
  }
  const seen = new Array<boolean>(n).fill(false);
  const out: number[][] = [];
  for (let s = 0; s < n; s++) {
    if (seen[s]) continue;
    const comp: number[] = [];
    const queue = [s];
    seen[s] = true;
    while (queue.length > 0) {
      const x = queue.pop()!;
      comp.push(x);
      for (const y of adj[x]) {
        if (!seen[y]) {
          seen[y] = true;
          queue.push(y);
        }
      }
    }
    out.push(comp);
  }
  return out;
}

/** u、v 连通的最早阈值：在阈值子图上 BFS，扫描递增权重。 */
function referenceMergeWeight(
  n: number,
  edges: readonly WeightedEdge[],
  u: number,
  v: number,
): number | null {
  if (u === v) return null;
  const weights = [...new Set(edges.map((e) => e[2]))].sort((a, b) => a - b);
  for (const t of weights) {
    const comps = referenceClusters(n, edges, t);
    if (comps.some((c) => c.includes(u) && c.includes(v))) return t;
  }
  return null;
}

function normalize(clusters: number[][]): number[][] {
  return clusters
    .map((c) => [...c].sort((a, b) => a - b))
    .sort((a, b) => a[0] - b[0]);
}

function assertClustersEqual(actual: number[][], expected: number[][]) {
  assert.deepEqual(normalize(actual), normalize(expected));
}

/** 确定性伪随机数（mulberry32）。 */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------- 基础分层与阈值查询 ----------

test("按权重递增分批合并，阈值查询返回正确分层", () => {
  const tc = new ThresholdClustering(5);
  tc.appendBatch(1, [[0, 1]]);
  tc.appendBatch(2, [[1, 2]]);
  tc.appendBatch(5, [[3, 4]]);

  assertClustersEqual(tc.clustersAt(0), [[0], [1], [2], [3], [4]]);
  assertClustersEqual(tc.clustersAt(1), [[0, 1], [2], [3], [4]]);
  assertClustersEqual(tc.clustersAt(2), [[0, 1, 2], [3], [4]]);
  assertClustersEqual(tc.clustersAt(4), [[0, 1, 2], [3], [4]]);
  assertClustersEqual(tc.clustersAt(5), [[0, 1, 2], [3, 4]]);
  assertClustersEqual(tc.clustersAt(100), [[0, 1, 2], [3, 4]]);

  assert.equal(tc.componentCountAt(0), 5);
  assert.equal(tc.componentCountAt(2), 3);
  assert.equal(tc.componentCountAt(5), 2);
  assert.deepEqual(tc.componentOf(2, 2), [0, 1, 2]);
  assert.deepEqual(tc.componentOf(4, 4), [4]);
  assert.equal(tc.areConnected(0, 2, 2), true);
  assert.equal(tc.areConnected(0, 2, 1), false);
  assert.equal(tc.areConnected(0, 4, 100), false);
});

// ---------- 同权边原子生效 ----------

test("同权边作为一批生效，不存在输入顺序产生的中间层", () => {
  // 三条同权边构成两个分量 {0,1,2} 与 {3,4}，必须同时出现。
  const tc = ThresholdClustering.fromEdges(5, [
    [0, 1, 2],
    [1, 2, 2],
    [3, 4, 2],
  ]);
  assert.equal(tc.batchCount, 1);
  assertClustersEqual(tc.clustersAt(1.999), [[0], [1], [2], [3], [4]]);
  assertClustersEqual(tc.clustersAt(2), [[0, 1, 2], [3, 4]]);

  // 逐条 appendEdges（同权）也自动并入同一批，而非形成中间层。
  const inc = new ThresholdClustering(5);
  inc.appendEdges([[0, 1, 2]]);
  inc.appendEdges([[1, 2, 2]]);
  inc.appendEdges([[3, 4, 2]]);
  assert.equal(inc.batchCount, 1);
  assertClustersEqual(inc.clustersAt(2), [[0, 1, 2], [3, 4]]);
});

test("输入顺序不影响结果：打乱边序后批次与聚类完全一致", () => {
  const n = 12;
  const edges: WeightedEdge[] = [];
  const rand = rng(42);
  for (let i = 0; i < 60; i++) {
    edges.push([
      Math.floor(rand() * n),
      Math.floor(rand() * n),
      Math.floor(rand() * 6), // 权重取自小集合，制造大量同权边
    ]);
  }
  const a = ThresholdClustering.fromEdges(n, edges);
  const shuffled = [...edges];
  for (let i = shuffled.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
  }
  const b = ThresholdClustering.fromEdges(n, shuffled);

  assert.deepEqual(
    a.batches.map((x) => x.weight),
    b.batches.map((x) => x.weight),
  );
  for (let t = -1; t <= 7; t++) {
    assertClustersEqual(a.clustersAt(t), b.clustersAt(t));
  }
  for (let u = 0; u < n; u++) {
    for (let v = 0; v < n; v++) {
      assert.equal(a.mergeWeight(u, v), b.mergeWeight(u, v));
    }
  }
});

// ---------- 撤回末批并追加新批 ----------

test("undoLastBatch 撤回末批，追加新批后等价于从头构建", () => {
  const tc = new ThresholdClustering(6);
  tc.appendBatch(1, [[0, 1]]);
  tc.appendBatch(2, [[2, 3]]);
  tc.appendBatch(3, [[4, 5]]);

  const undone = tc.undoLastBatch();
  assert.equal(undone?.weight, 3);
  assert.equal(tc.batchCount, 2);
  assertClustersEqual(tc.clustersAt(10), [[0, 1], [2, 3], [4], [5]]);

  // 追加权重相同（3）的新批：与从头构建 1,2,3' 完全一致。
  tc.appendBatch(3, [[1, 2]]);
  const fresh = new ThresholdClustering(6);
  fresh.appendBatch(1, [[0, 1]]);
  fresh.appendBatch(2, [[2, 3]]);
  fresh.appendBatch(3, [[1, 2]]);
  for (let t = 0; t <= 4; t++) {
    assertClustersEqual(tc.clustersAt(t), fresh.clustersAt(t));
  }
  assertClustersEqual(tc.clustersAt(3), [[0, 1, 2, 3], [4], [5]]);

  // 连续撤回到空。
  assert.equal(tc.undoLastBatch()?.weight, 3);
  assert.equal(tc.undoLastBatch()?.weight, 2);
  assert.equal(tc.undoLastBatch()?.weight, 1);
  assert.equal(tc.undoLastBatch(), undefined);
  assert.equal(tc.batchCount, 0);
  assertClustersEqual(tc.clustersAt(100), [[0], [1], [2], [3], [4], [5]]);
});

test("撤回后可用 appendEdges 追加多组新批", () => {
  const tc = ThresholdClustering.fromEdges(4, [
    [0, 1, 1],
    [1, 2, 2],
    [2, 3, 9],
  ]);
  tc.undoLastBatch(); // 撤回权重 9 的批
  tc.appendEdges([
    [2, 3, 3],
    [0, 3, 4],
  ]);
  assertClustersEqual(tc.clustersAt(2), [[0, 1, 2], [3]]);
  assertClustersEqual(tc.clustersAt(3), [[0, 1, 2, 3]]);
  assert.equal(tc.mergeWeight(0, 3), 3);
});

// ---------- 最早合并权重 ----------

test("mergeWeight / firstMergeWeight 与 BFS 参考一致", () => {
  const edges: WeightedEdge[] = [
    [0, 1, 1],
    [1, 2, 3],
    [3, 4, 2],
    [2, 3, 10],
    [0, 2, 8], // 平行于 0-1-2 路径的另一条边，权重更高，不改变最早合并
  ];
  const n = 6; // 顶点 5 是孤点
  const tc = ThresholdClustering.fromEdges(n, edges);

  for (let u = 0; u < n; u++) {
    for (let v = 0; v < n; v++) {
      assert.equal(
        tc.mergeWeight(u, v),
        referenceMergeWeight(n, edges, u, v),
        `mergeWeight(${u}, ${v})`,
      );
    }
  }
  assert.equal(tc.mergeWeight(0, 2), 3); // 走 0-1(1) + 1-2(3)，而非 0-2(8)
  assert.equal(tc.mergeWeight(0, 4), 10);
  assert.equal(tc.mergeWeight(0, 5), null); // 孤点永不连通
  assert.equal(tc.mergeWeight(3, 3), null);

  assert.equal(tc.firstMergeWeight(0), 1);
  assert.equal(tc.firstMergeWeight(4), 2);
  assert.equal(tc.firstMergeWeight(5), null);
});

// ---------- 平行边 / 孤点 / 多分量：BFS 随机对照 ----------

test("随机多重图：各阈值下聚类与阈值子图 BFS 一致（含平行边、孤点、多分量）", () => {
  for (const [seed, n, edgeCount, maxW] of [
    [1, 1, 0, 3],
    [2, 2, 5, 2],
    [7, 8, 30, 4],
    [13, 20, 80, 6],
    [99, 30, 120, 5],
  ] as const) {
    const rand = rng(seed);
    const edges: WeightedEdge[] = [];
    for (let i = 0; i < edgeCount; i++) {
      // 允许自环与平行边（同一对顶点重复出现）
      edges.push([
        Math.floor(rand() * n),
        Math.floor(rand() * n),
        Math.floor(rand() * maxW) + 1,
      ]);
    }
    const tc = ThresholdClustering.fromEdges(n, edges);
    for (let t = -1; t <= maxW + 1; t++) {
      assertClustersEqual(
        tc.clustersAt(t),
        referenceClusters(n, edges, t),
      );
      assert.equal(
        tc.componentCountAt(t),
        referenceClusters(n, edges, t).length,
      );
    }
    // 抽样核对 mergeWeight 与 componentOf
    for (let u = 0; u < n; u++) {
      for (let v = 0; v < n; v++) {
        assert.equal(
          tc.mergeWeight(u, v),
          referenceMergeWeight(n, edges, u, v),
          `seed=${seed} mergeWeight(${u}, ${v})`,
        );
      }
      for (let t = 0; t <= maxW; t++) {
        const comp = tc.componentOf(u, t);
        const ref = referenceClusters(n, edges, t).find((c) => c.includes(u))!;
        assert.deepEqual(
          [...comp].sort((x, y) => x - y),
          [...ref].sort((x, y) => x - y),
        );
      }
    }
  }
});

test("随机操作序列（追加/撤回交错）后仍与 BFS 一致", () => {
  const rand = rng(2024);
  const n = 15;
  const maxW = 5;
  const tc = new ThresholdClustering(n);
  const committed: WeightedEdge[] = []; // 测试侧镜像当前已提交的边

  for (let step = 0; step < 60; step++) {
    if (tc.batchCount > 0 && rand() < 0.35) {
      // 撤回末批
      const undone = tc.undoLastBatch()!;
      let remain = committed.length;
      // 从镜像中移除该批次的边（权重等于 undone.weight 的尾部若干条）
      while (remain > 0 && committed[remain - 1][2] === undone.weight) remain--;
      committed.length = remain;
    } else {
      // 追加一批：权重 >= 当前末批权重；小权重集合制造同权合并
      const base = tc.lastBatchWeight ?? 0;
      const w = base + Math.floor(rand() * 2); // base 或 base+1
      const batchEdges: WeightedEdge[] = [];
      const k = Math.floor(rand() * 6);
      for (let i = 0; i < k; i++) {
        batchEdges.push([
          Math.floor(rand() * n),
          Math.floor(rand() * n),
          w,
        ]);
      }
      tc.appendEdges(batchEdges);
      committed.push(...batchEdges);
    }

    // 每一步都用阈值子图 BFS 全面核对
    for (let t = -1; t <= maxW + 2; t++) {
      assertClustersEqual(
        tc.clustersAt(t),
        referenceClusters(n, committed, t),
      );
    }
    for (let s = 0; s < 10; s++) {
      const u = Math.floor(rand() * n);
      const v = Math.floor(rand() * n);
      assert.equal(
        tc.mergeWeight(u, v),
        referenceMergeWeight(n, committed, u, v),
      );
    }
  }
});

// ---------- 显式平行边 / 孤点 / 同时多分量 ----------

test("平行边、自环、孤点与同一批内多个分量", () => {
  const tc = new ThresholdClustering(7);
  tc.appendBatch(2, [
    [0, 1],
    [0, 1], // 平行边
    [1, 0], // 反向平行边
    [2, 2], // 自环
    [3, 4],
    [4, 5],
  ]);
  // 顶点 6 是孤点
  assertClustersEqual(tc.clustersAt(2), [[0, 1], [2], [3, 4, 5], [6]]);
  assert.equal(tc.mergeWeight(3, 5), 2);
  assert.equal(tc.mergeWeight(0, 3), null);
  assert.equal(tc.firstMergeWeight(2), null); // 自环不算脱离单点集
  assert.equal(tc.firstMergeWeight(6), null);
  assert.deepEqual(tc.componentOf(6, 100), [6]);
});

// ---------- 错误处理 ----------

test("权重回退抛错，提示先撤回", () => {
  const tc = new ThresholdClustering(3);
  tc.appendBatch(5, [[0, 1]]);
  assert.throws(() => tc.appendBatch(4, [[1, 2]]), RangeError);
  assert.throws(() => tc.appendEdges([[1, 2, 4]]), RangeError);
  // 同权不抛错，而是并入末批
  tc.appendBatch(5, [[1, 2]]);
  assert.equal(tc.batchCount, 1);
  assertClustersEqual(tc.clustersAt(5), [[0, 1, 2]]);
});

test("非法顶点与非法权重抛错", () => {
  const tc = new ThresholdClustering(3);
  assert.throws(() => tc.appendBatch(1, [[0, 3]]), RangeError);
  assert.throws(() => tc.appendBatch(1, [[-1, 0]]), RangeError);
  assert.throws(() => tc.appendBatch(NaN, [[0, 1]]), TypeError);
  assert.throws(() => tc.componentOf(3, 0), RangeError);
  assert.throws(() => new ThresholdClustering(-1), RangeError);
  assert.equal(tc.undoLastBatch(), undefined);
});
