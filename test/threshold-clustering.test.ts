import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ThresholdClustering,
  type WeightedEdge,
} from "../src/threshold-clustering.ts";

/** 参照实现：阈值子图（w <= threshold 的边）上 BFS 求连通分量。 */
function bfsComponents(
  vertexCount: number,
  edges: WeightedEdge[],
  threshold: number
): number[][] {
  const adj: number[][] = Array.from({ length: vertexCount }, () => []);
  for (const e of edges) {
    if (e.w <= threshold) {
      adj[e.u].push(e.v);
      adj[e.v].push(e.u);
    }
  }
  const seen = new Array<boolean>(vertexCount).fill(false);
  const comps: number[][] = [];
  for (let s = 0; s < vertexCount; s++) {
    if (seen[s]) continue;
    const comp: number[] = [];
    const queue = [s];
    seen[s] = true;
    while (queue.length) {
      const x = queue.pop()!;
      comp.push(x);
      for (const y of adj[x]) {
        if (!seen[y]) {
          seen[y] = true;
          queue.push(y);
        }
      }
    }
    comps.push(comp.sort((a, b) => a - b));
  }
  return comps.sort((a, b) => a[0] - b[0]);
}

/** 参照实现：阈值子图 BFS 求 u、v 最早连通的权重（在权重候选集上二分）。 */
function bfsMergeWeight(
  vertexCount: number,
  edges: WeightedEdge[],
  u: number,
  v: number
): number {
  if (u === v) return -Infinity;
  const weights = [...new Set(edges.map((e) => e.w))].sort((a, b) => a - b);
  const connected = (t: number) =>
    bfsComponents(vertexCount, edges, t).some(
      (c) => c.includes(u) && c.includes(v)
    );
  if (!connected(Infinity)) return Infinity;
  let lo = 0;
  let hi = weights.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (connected(weights[mid])) hi = mid;
    else lo = mid + 1;
  }
  return weights[lo];
}

function e(u: number, v: number, w: number): WeightedEdge {
  return { u, v, w };
}

test("同权边作为一批原子生效：多个分量同时合并，无中间层", () => {
  const c = new ThresholdClustering(8);
  // 同一批权重 5：{0,1,2} 连通，{3,4} 连通；两条边缺一不可。
  c.appendBatch([e(0, 1, 5), e(1, 2, 5), e(3, 4, 5)]);
  // 阈值 4.999：全部孤点。
  assert.equal(c.componentsAt(4.999).length, 8);
  // 阈值 5：两批同时整体生效，不存在只有部分边生效的中间层。
  assert.deepEqual(c.componentsAt(5), [
    [0, 1, 2],
    [3, 4],
    [5],
    [6],
    [7],
  ]);
  assert.equal(c.batchCount, 1);
});

test("同权边乱序输入产生完全相同的内部状态", () => {
  const edges = [e(2, 0, 5), e(3, 4, 5), e(1, 2, 5), e(5, 6, 5)];
  const a = new ThresholdClustering(8);
  a.appendBatch(edges);
  const b = new ThresholdClustering(8);
  b.appendBatch([edges[3], edges[1], edges[2], edges[0]].reverse());
  for (const t of [0, 4, 5, 6, Infinity]) {
    assert.deepEqual(a.componentsAt(t), b.componentsAt(t));
  }
  // 内部快照也一致（批内排序保证确定性）。
  assert.deepEqual(
    (a as any).levels[0].parent,
    (b as any).levels[0].parent
  );
});

test("平行边（同权/异权重复边）不改变结果", () => {
  const c = new ThresholdClustering(4);
  c.appendBatch([e(0, 1, 1), e(0, 1, 1), e(1, 0, 1)]);
  c.appendBatch([e(1, 2, 2), e(0, 1, 2), e(2, 1, 2)]);
  assert.deepEqual(c.componentsAt(1), [[0, 1], [2], [3]]);
  assert.deepEqual(c.componentsAt(2), [[0, 1, 2], [3]]);
  assert.equal(c.mergeWeight(0, 2), 2);
});

test("孤点始终作为单点分量出现", () => {
  const c = new ThresholdClustering(5);
  c.appendBatch([e(0, 1, 3)]);
  assert.deepEqual(c.componentsAt(10), [[0, 1], [2], [3], [4]]);
  assert.deepEqual(c.membersAt(4, 10), [4]);
  assert.equal(c.mergeWeight(4, 0), Infinity);
  assert.equal(c.mergeWeight(4, 4), -Infinity);
});

test("撤销末批后可追加新批，历史分叉正确", () => {
  const c = new ThresholdClustering(6);
  c.appendBatch([e(0, 1, 1)]);
  c.appendBatch([e(1, 2, 2)]);
  c.appendBatch([e(2, 3, 3)]);
  assert.deepEqual(c.componentsAt(Infinity), [[0, 1, 2, 3], [4], [5]]);

  assert.equal(c.undoLastBatch(), true); // 撤销权重 3
  assert.deepEqual(c.componentsAt(Infinity), [[0, 1, 2], [3], [4], [5]]);
  assert.equal(c.mergeWeight(0, 3), Infinity);

  // 追加新批：权重等于末层 -> 并入末层；权重更大 -> 新层。
  c.appendBatch([e(3, 4, 2), e(4, 5, 4)]);
  assert.deepEqual(c.batchWeights, [1, 2, 4]);
  assert.deepEqual(c.componentsAt(2), [[0, 1, 2], [3, 4], [5]]);
  assert.deepEqual(c.componentsAt(4), [[0, 1, 2], [3, 4, 5]]);
  assert.equal(c.mergeWeight(3, 5), 4);

  // 权重小于末层必须报错。
  assert.throws(() => c.appendBatch([e(0, 5, 3)]), /undoLastBatch/);

  // 全部撤销。
  assert.equal(c.undoLastBatch(), true);
  assert.equal(c.undoLastBatch(), true);
  assert.equal(c.undoLastBatch(), true);
  assert.equal(c.undoLastBatch(), false);
  assert.equal(c.componentsAt(Infinity).length, 6);
});

test("membersAt / connectedAt 与阈值子图 BFS 一致", () => {
  const c = new ThresholdClustering(7);
  const edges = [
    e(0, 1, 2),
    e(1, 2, 3),
    e(3, 4, 3), // 与上一批同权，同时生效
    e(2, 3, 5),
    e(5, 6, 1),
  ];
  c.appendBatch(edges);
  for (const t of [0, 1, 2, 3, 4, 5, 6]) {
    assert.deepEqual(c.componentsAt(t), bfsComponents(7, edges, t));
    for (let v = 0; v < 7; v++) {
      const expected = bfsComponents(7, edges, t).find((comp) =>
        comp.includes(v)
      )!;
      assert.deepEqual(c.membersAt(v, t), expected);
    }
  }
  assert.equal(c.connectedAt(0, 4, 4), false);
  assert.equal(c.connectedAt(0, 4, 5), true);
});

test("mergeWeight 与 BFS 参照一致", () => {
  const c = new ThresholdClustering(6);
  const edges = [e(0, 1, 2), e(1, 2, 4), e(3, 4, 1), e(2, 3, 7)];
  c.appendBatch(edges);
  for (let u = 0; u < 6; u++) {
    for (let v = 0; v < 6; v++) {
      assert.equal(
        c.mergeWeight(u, v),
        bfsMergeWeight(6, edges, u, v),
        `mergeWeight(${u}, ${v})`
      );
    }
  }
});

test("随机差分测试：追加/撤销/再追加，全程与 BFS 核对", () => {
  // 可复现的伪随机数。
  let seed = 0xC0FFEE;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };

  const N = 12;
  const c = new ThresholdClustering(N);
  const committed: WeightedEdge[] = []; // 当前生效的边
  let nextWeight = 0;

  for (let step = 0; step < 60; step++) {
    const action = rand();
    if (action < 0.65 || c.batchCount === 0) {
      // 追加一批：1~4 条边，权重非降；含平行边与自环外任意顶点对。
      nextWeight += 1 + Math.floor(rand() * 3);
      const w = rand() < 0.3 && c.batchCount > 0 ? c.batchWeights.at(-1)! : nextWeight;
      const batch: WeightedEdge[] = [];
      const k = 1 + Math.floor(rand() * 4);
      for (let i = 0; i < k; i++) {
        batch.push(
          e(Math.floor(rand() * N), Math.floor(rand() * N), w)
        );
      }
      c.appendBatch(batch);
      committed.push(...batch);
    } else {
      // 撤销末批。
      const w = c.batchWeights.at(-1)!;
      c.undoLastBatch();
      while (committed.length && committed[committed.length - 1].w === w) {
        committed.pop();
      }
    }
    // 在多个阈值下与 BFS 参照核对（含小数阈值与极值）。
    const thresholds = [-Infinity, 0, 2.5, 5, 7.5, 11, 20, Infinity];
    for (const t of thresholds) {
      assert.deepEqual(
        c.componentsAt(t),
        bfsComponents(N, committed, t),
        `step=${step} threshold=${t}`
      );
    }
    // 随机抽查 mergeWeight。
    for (let i = 0; i < 5; i++) {
      const u = Math.floor(rand() * N);
      const v = Math.floor(rand() * N);
      assert.equal(c.mergeWeight(u, v), bfsMergeWeight(N, committed, u, v));
    }
  }
});

test("输入校验", () => {
  const c = new ThresholdClustering(3);
  assert.throws(() => c.appendBatch([e(0, 3, 1)]), RangeError);
  assert.throws(() => c.appendBatch([e(0, 1, NaN)]), TypeError);
  assert.throws(() => c.membersAt(9, 0), RangeError);
  assert.throws(() => new ThresholdClustering(-1), RangeError);
  // 空批是 no-op。
  c.appendBatch([]);
  assert.equal(c.batchCount, 0);
});
