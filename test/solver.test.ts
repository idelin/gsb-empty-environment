import { test } from "node:test";
import assert from "node:assert/strict";
import { solveConstrainedTopo } from "../src/solver.ts";
import { verifyEvidence, type Edge, type Evidence } from "../src/evidence.ts";
import {
  isValidPermutation,
  lexMinValidPermutation,
} from "../src/bruteForce.ts";

const BIG = { budget: Number.POSITIVE_INFINITY };

function chainEdges(k: number): Edge[] {
  const edges: Edge[] = [];
  for (let i = 0; i + 1 < k; i++) edges.push([i, i + 1]);
  return edges;
}

function assertConsistentWithBruteForce(
  n: number,
  edges: Edge[],
  fixed: Map<number, number>,
): void {
  const result = solveConstrainedTopo(n, edges, fixed, BIG);
  const oracle = lexMinValidPermutation(n, edges, fixed);
  if (oracle === null) {
    assert.equal(result.status, "infeasible", `expected infeasible, got ${JSON.stringify(result)}`);
    assert.ok(
      verifyEvidence(n, edges, fixed, (result as { evidence: Evidence }).evidence),
      "evidence must verify",
    );
  } else {
    assert.equal(result.status, "solved", `expected solved, got ${JSON.stringify(result)}`);
    const perm = (result as { permutation: number[] }).permutation;
    assert.deepEqual(perm, oracle, "must be the lexicographically smallest valid permutation");
    assert.ok(isValidPermutation(n, edges, fixed, perm));
  }
}

test("空图（n=9，无固定位置）：字典序最小排列为 0..8", () => {
  const result = solveConstrainedTopo(9, [], new Map(), BIG);
  assert.equal(result.status, "solved");
  assert.deepEqual((result as { permutation: number[] }).permutation, [0, 1, 2, 3, 4, 5, 6, 7, 8]);
  assertConsistentWithBruteForce(9, [], new Map());
});

test("空图（n=9）加固定位置：与枚举核对", () => {
  const fixed = new Map([[7, 0], [3, 5], [8, 8]]);
  const result = solveConstrainedTopo(9, [], fixed, BIG);
  assert.equal(result.status, "solved");
  const perm = (result as { permutation: number[] }).permutation;
  assert.equal(perm[0], 7);
  assert.equal(perm[5], 3);
  assert.equal(perm[8], 8);
  assertConsistentWithBruteForce(9, [], fixed);
});

test("n=0：空排列", () => {
  const result = solveConstrainedTopo(0, [], new Map(), BIG);
  assert.equal(result.status, "solved");
  assert.deepEqual((result as { permutation: number[] }).permutation, []);
});

test("环：不可行并给出环证据", () => {
  const edges: Edge[] = [[0, 1], [1, 2], [2, 0]];
  const result = solveConstrainedTopo(3, edges, new Map(), BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "cycle");
  assert.ok(verifyEvidence(3, edges, new Map(), evidence));
  assert.equal(lexMinValidPermutation(3, edges, new Map()), null);
});

test("自环：长度为 1 的环证据", () => {
  const edges: Edge[] = [[2, 2]];
  const result = solveConstrainedTopo(4, edges, new Map(), BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.deepEqual(evidence, { kind: "cycle", nodes: [2] });
  assert.ok(verifyEvidence(4, edges, new Map(), evidence));
});

test("位置被依赖链挤占：拓扑有序但无位置解（n=9）", () => {
  // 链 0→1→…→8 存在普通拓扑序 [0..8]，但节点 8 被固定在位置 4，
  // 其 8 个前驱挤不进前 4 个位置。
  const edges = chainEdges(9);
  const fixed = new Map([[8, 4]]);
  const result = solveConstrainedTopo(9, edges, fixed, BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "interval-squeeze");
  assert.ok(verifyEvidence(9, edges, fixed, evidence));
  assert.equal(lexMinValidPermutation(9, edges, fixed), null);
});

test("位置被依赖链挤占：短链情形（n=4）", () => {
  const edges = chainEdges(4); // 0→1→2→3
  const fixed = new Map([[3, 2]]); // 3 个前驱 vs 2 个空位
  const result = solveConstrainedTopo(4, edges, fixed, BIG);
  assert.equal(result.status, "infeasible");
  assert.ok(verifyEvidence(4, edges, fixed, (result as { evidence: Evidence }).evidence));
  assertConsistentWithBruteForce(4, edges, fixed);
});

test("链恰好放得下：位置解存在且字典序最小", () => {
  const edges = chainEdges(4);
  const fixed = new Map([[3, 3]]);
  const result = solveConstrainedTopo(4, edges, fixed, BIG);
  assert.equal(result.status, "solved");
  assert.deepEqual((result as { permutation: number[] }).permutation, [0, 1, 2, 3]);
  assertConsistentWithBruteForce(4, edges, fixed);
});

test("后缀挤占：后继挤不进固定位置之后", () => {
  // 0→1，0→2，节点 0 固定在位置 2（共 3 点）：位置 2 之后没有空位放后继
  const edges: Edge[] = [[0, 1], [0, 2]];
  const fixed = new Map([[0, 2]]);
  const result = solveConstrainedTopo(3, edges, fixed, BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "interval-squeeze");
  assert.ok(verifyEvidence(3, edges, fixed, evidence));
  assertConsistentWithBruteForce(3, edges, fixed);
});

test("中间区间挤占：前后缀检查无法命中的深层冲突", () => {
  // a@1，b@4，x1,x2,x3 都被夹在中间：a→xi→b。
  // xi 只能落在 {2,3} 两个位置，3 个节点挤不下。
  // 这与普通拓扑排序成功无关：必须靠区间挤占证据判定。
  const edges: Edge[] = [
    [5, 1], [5, 2], [5, 3], // a=5 → xi
    [1, 6], [2, 6], [3, 6], // xi → b=6
  ];
  const fixed = new Map([[5, 1], [6, 4]]);
  const result = solveConstrainedTopo(8, edges, fixed, BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "interval-squeeze");
  if (evidence.kind === "interval-squeeze") {
    // 区间 [1,4) 只有 3 个空位，但 a、x1、x2、x3 共 4 个节点被挤入
    assert.equal(evidence.s, 1);
    assert.equal(evidence.t, 4);
    assert.equal(evidence.members.length, 4);
    assert.ok(evidence.members.every((m) => [1, 2, 3, 5].includes(m.node)));
  }
  assert.ok(verifyEvidence(8, edges, fixed, evidence));
  assertConsistentWithBruteForce(8, edges, fixed);
});

test("顺序矛盾：u⇝v 但 pos(u) >= pos(v)", () => {
  const edges: Edge[] = [[0, 2], [2, 3]];
  const fixed = new Map([[0, 4], [3, 1]]);
  const result = solveConstrainedTopo(5, edges, fixed, BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "chain-contradiction");
  assert.ok(verifyEvidence(5, edges, fixed, evidence));
  assertConsistentWithBruteForce(5, edges, fixed);
});

test("链式挤占：中间节点占槽导致的深层矛盾", () => {
  // 0@2 → 3 → 2@3：链长 2 需要 pos(2) >= 4，但 2 固定在 3。
  // 普通拓扑排序成功（[0,1,3,2,4] 等），但无位置解。
  const edges: Edge[] = [[0, 3], [3, 2]];
  const fixed = new Map([[0, 2], [1, 1], [2, 3]]);
  const result = solveConstrainedTopo(5, edges, fixed, BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "chain-contradiction");
  assert.ok(verifyEvidence(5, edges, fixed, evidence));
  assertConsistentWithBruteForce(5, edges, fixed);
});

test("两个节点占用同一固定位置", () => {
  const fixed = new Map([[1, 2], [3, 2]]);
  const result = solveConstrainedTopo(5, [], fixed, BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "duplicate-fixed-position");
  assert.ok(verifyEvidence(5, [], fixed, evidence));
});

test("固定位置越界", () => {
  const fixed = new Map([[2, 9]]);
  const result = solveConstrainedTopo(5, [], fixed, BIG);
  assert.equal(result.status, "infeasible");
  const evidence = (result as { evidence: Evidence }).evidence;
  assert.equal(evidence.kind, "fixed-position-out-of-range");
  assert.ok(verifyEvidence(5, [], fixed, evidence));
});

test("搜索预算耗尽：返回 unknown 且不伪造解", () => {
  const result = solveConstrainedTopo(9, [], new Map(), { budget: 3 });
  assert.equal(result.status, "unknown");
  assert.ok(!("permutation" in result));
  // 同一实例预算充足时可解
  const solved = solveConstrainedTopo(9, [], new Map(), BIG);
  assert.equal(solved.status, "solved");
});

test("预算为 0：立即 unknown（除非无需搜索）", () => {
  const result = solveConstrainedTopo(2, [[0, 1]], new Map(), { budget: 0 });
  assert.equal(result.status, "unknown");
});

test("字典序最小：需要回溯的实例", () => {
  // 固定 2 在位置 2；候选若贪错需回溯。与枚举逐一核对。
  const edges: Edge[] = [[0, 2], [1, 2]];
  const fixed = new Map([[2, 2]]);
  assertConsistentWithBruteForce(4, edges, fixed);
  const result = solveConstrainedTopo(4, edges, fixed, BIG);
  assert.deepEqual((result as { permutation: number[] }).permutation, [0, 1, 2, 3]);
});

test("字典序最小：固定位置改变最优选择", () => {
  const edges: Edge[] = [[0, 3]];
  const fixed = new Map([[3, 2]]);
  assertConsistentWithBruteForce(4, edges, fixed);
  const result = solveConstrainedTopo(4, edges, fixed, BIG);
  assert.deepEqual((result as { permutation: number[] }).permutation, [0, 1, 3, 2]);
});

// mulberry32 确定性伪随机数
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

test("随机交叉验证：n<=8，与排列枚举全量比对（含环、冲突、挤占）", () => {
  const rand = mulberry32(20261005);
  const counts = { solved: 0, infeasible: 0 };
  const evidenceKinds = new Map<string, number>();
  for (let iter = 0; iter < 600; iter++) {
    const n = 1 + Math.floor(rand() * 8);
    const edgeProb = [0.1, 0.25, 0.4][Math.floor(rand() * 3)];
    const edges: Edge[] = [];
    for (let u = 0; u < n; u++) {
      for (let v = 0; v < n; v++) {
        if (u !== v && rand() < edgeProb) edges.push([u, v]);
      }
    }
    const fixed = new Map<number, number>();
    for (let v = 0; v < n; v++) {
      if (rand() < 0.35) fixed.set(v, Math.floor(rand() * n));
    }
    const result = solveConstrainedTopo(n, edges, fixed, BIG);
    assert.notEqual(result.status, "unknown", "无限预算下不得返回 unknown");
    assertConsistentWithBruteForce(n, edges, fixed);
    if (result.status === "infeasible") {
      counts.infeasible++;
      const kind = (result as { evidence: Evidence }).evidence.kind;
      evidenceKinds.set(kind, (evidenceKinds.get(kind) ?? 0) + 1);
    } else {
      counts.solved++;
    }
  }
  assert.ok(counts.solved > 0 && counts.infeasible > 0, "样本应同时覆盖两类结果");
  // 确保挤占类证据确实被随机样本覆盖到
  assert.ok(
    (evidenceKinds.get("interval-squeeze") ?? 0) > 0,
    `随机样本未覆盖挤占证据: ${JSON.stringify([...evidenceKinds])}`,
  );
});

test("9 点全量枚举核对：空图、环、链挤占、随机 DAG", () => {
  // 空图
  assertConsistentWithBruteForce(9, [], new Map([[4, 4]]));
  // 环
  const cycleEdges: Edge[] = [[0, 1], [1, 2], [2, 0], [3, 4]];
  assertConsistentWithBruteForce(9, cycleEdges, new Map([[5, 5]]));
  // 链 + 挤占
  assertConsistentWithBruteForce(9, chainEdges(6), new Map([[5, 3]]));
  // 随机 DAG（只加小编号→大编号的边）+ 随机固定位置
  const rand = mulberry32(99);
  for (let iter = 0; iter < 5; iter++) {
    const edges: Edge[] = [];
    for (let u = 0; u < 9; u++) {
      for (let v = u + 1; v < 9; v++) {
        if (rand() < 0.2) edges.push([u, v]);
      }
    }
    const fixed = new Map<number, number>();
    for (let v = 0; v < 9; v++) {
      if (rand() < 0.3) fixed.set(v, Math.floor(rand() * 9));
    }
    assertConsistentWithBruteForce(9, edges, fixed);
  }
});
