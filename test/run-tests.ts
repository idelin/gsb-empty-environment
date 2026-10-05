import assert from "node:assert/strict";
import { parseFormula, evalFormula } from "../src/formula.ts";
import type { Formula } from "../src/formula.ts";
import { existsDisjointPair } from "../src/search.ts";
import { checkIntersection } from "../src/check.ts";

function allQuorums(f: Formula, n: number): number[][] {
  const res: number[][] = [];
  for (let mask = 0; mask < 1 << n; mask++) {
    const memb: boolean[] = [];
    for (let i = 0; i < n; i++) memb.push(((mask >> i) & 1) === 1);
    if (evalFormula(f, memb)) {
      const s: number[] = [];
      for (let i = 0; i < n; i++) if (memb[i]) s.push(i);
      res.push(s);
    }
  }
  return res;
}

function cmpListLex(x: number[], y: number[]): number {
  const m = Math.min(x.length, y.length);
  for (let i = 0; i < m; i++) if (x[i] !== y[i]) return x[i] - y[i];
  return x.length - y.length;
}

function bruteMinPair(fa: Formula, fb: Formula, n: number): [number[], number[]] | null {
  const qa = allQuorums(fa, n);
  const qb = allQuorums(fb, n);
  let best: [number[], number[]] | null = null;
  for (const a of qa) {
    const sa = new Set(a);
    for (const b of qb) {
      if (b.some((v) => sa.has(v))) continue;
      if (
        best === null ||
        cmpListLex(a, best[0]) < 0 ||
        (cmpListLex(a, best[0]) === 0 && cmpListLex(b, best[1]) < 0)
      ) {
        best = [a, b];
      }
    }
  }
  return best;
}

let passed = 0;
function test(name: string, fn: () => void): void {
  fn();
  passed++;
  console.log(`ok - ${name}`);
}

const q = (raw: unknown, n: number): Formula => parseFormula(raw, n);

test("5 节点多数派 (3-of-5)：rw 与 ww 均相交", () => {
  const f = q({ atLeast: 3, group: [0, 1, 2, 3, 4] }, 5);
  const rw = checkIntersection(f, f, 5);
  assert.equal(rw.intersects, true);
  if (rw.intersects) {
    assert.ok(rw.proof.nodesVisited > 0);
    assert.ok(rw.proof.pruned > 0);
    assert.ok(rw.proof.nodesVisited < 3 ** 5, "剪枝后搜索树应远小于全枚举");
  }
});

test("2-of-5 对 2-of-5：不相交，见证为 [0,1] 与 [2,3]", () => {
  const f = q({ atLeast: 2, group: [0, 1, 2, 3, 4] }, 5);
  const r = checkIntersection(f, f, 5);
  assert.equal(r.intersects, false);
  if (!r.intersects) {
    assert.deepEqual(r.witness.a, [0, 1]);
    assert.deepEqual(r.witness.b, [2, 3]);
  }
});

test("any[0,1] 写法定：见证为 [0] 与 [1]", () => {
  const f = q({ any: [0, 1] }, 2);
  const r = checkIntersection(f, f, 2);
  assert.equal(r.intersects, false);
  if (!r.intersects) {
    assert.deepEqual(r.witness.a, [0]);
    assert.deepEqual(r.witness.b, [1]);
  }
});

test("字典序按有序列表比较：前缀更短者更小", () => {
  const fa = q({ k: 2, of: [0, 1, 2] }, 4);
  const fb = q(3, 4);
  const r = checkIntersection(fa, fb, 4);
  assert.equal(r.intersects, false);
  if (!r.intersects) {
    assert.deepEqual(r.witness.a, [0, 1]);
    assert.deepEqual(r.witness.b, [2, 3]);
  }
});

test("嵌套公式：all[2-of{0,1,2}, any{3,4}] 与 3-of-5 读写相交", () => {
  const write = q({ all: [{ k: 2, of: [0, 1, 2] }, { any: [3, 4] }] }, 5);
  const read = q({ atLeast: 3, group: [0, 1, 2, 3, 4] }, 5);
  const rw = checkIntersection(read, write, 5);
  assert.equal(rw.intersects, true);
  const ww = checkIntersection(write, write, 5);
  assert.equal(ww.intersects, true);
});

test("单节点法定 {0}：ww 相交（唯一写集合与自身相交）", () => {
  const f = q(0, 3);
  const r = checkIntersection(f, f, 3);
  assert.equal(r.intersects, true);
});

test("20 节点多数派 (11-of-20)：ww 相交且搜索量受控", () => {
  const group = Array.from({ length: 20 }, (_, i) => i);
  const f = q({ atLeast: 11, group }, 20);
  const r = checkIntersection(f, f, 20);
  assert.equal(r.intersects, true);
  if (r.intersects) {
    assert.ok(r.proof.nodesVisited < 100000, `搜索节点过多: ${r.proof.nodesVisited}`);
  }
});

test("20 节点 10-of-20：ww 不相交，见证为前后两半", () => {
  const group = Array.from({ length: 20 }, (_, i) => i);
  const f = q({ atLeast: 10, group }, 20);
  const r = checkIntersection(f, f, 20);
  assert.equal(r.intersects, false);
  if (!r.intersects) {
    assert.deepEqual(r.witness.a, Array.from({ length: 10 }, (_, i) => i));
    assert.deepEqual(r.witness.b, Array.from({ length: 10 }, (_, i) => i + 10));
  }
});

test("非法输入：k 超界、节点越界、节点数超限", () => {
  assert.throws(() => q({ atLeast: 4, group: [0, 1, 2] }, 3));
  assert.throws(() => q({ atLeast: 1, group: [0, 5] }, 3));
  const f = q(0, 1);
  assert.throws(() => checkIntersection(f, f, 21));
  assert.throws(() => checkIntersection(f, f, 0));
});

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 2 ** 32;
  };
}

function randomFormula(rand: () => number, n: number, depth: number): Formula {
  const size = 1 + Math.floor(rand() * n);
  const group = new Set<number>();
  while (group.size < size) group.add(Math.floor(rand() * n));
  const children: Formula[] = [...group].map((node) => ({ kind: "leaf", node }));
  let f: Formula = { kind: "threshold", k: 1 + Math.floor(rand() * size), children };
  if (depth > 0 && rand() < 0.5) {
    const g = randomFormula(rand, n, depth - 1);
    f = {
      kind: "threshold",
      k: rand() < 0.5 ? 1 : 2,
      children: [f, g],
    };
  }
  return f;
}

test("随机小实例与暴力枚举对拍（存在性 + 字典序最小见证）", () => {
  const rand = lcg(20261005);
  const n = 5;
  for (let t = 0; t < 300; t++) {
    const fa = randomFormula(rand, n, 2);
    const fb = randomFormula(rand, n, 2);
    const brute = bruteMinPair(fa, fb, n);
    const r = checkIntersection(fa, fb, n);
    assert.equal(r.intersects, brute === null, `case ${t} 存在性不一致`);
    if (!r.intersects && brute !== null) {
      assert.deepEqual(r.witness.a, brute[0], `case ${t} 见证 A 非最小`);
      assert.deepEqual(r.witness.b, brute[1], `case ${t} 见证 B 非最小`);
    }
    const ww = checkIntersection(fa, fa, n);
    const bruteWw = bruteMinPair(fa, fa, n);
    assert.equal(ww.intersects, bruteWw === null, `case ${t} ww 存在性不一致`);
    if (!ww.intersects && bruteWw !== null) {
      assert.deepEqual(ww.witness.a, bruteWw[0], `case ${t} ww 见证 A 非最小`);
      assert.deepEqual(ww.witness.b, bruteWw[1], `case ${t} ww 见证 B 非最小`);
    }
  }
});

test("存在性搜索与暴力枚举一致（独立验证 existsDisjointPair）", () => {
  const rand = lcg(12345);
  const n = 5;
  for (let t = 0; t < 200; t++) {
    const fa = randomFormula(rand, n, 2);
    const fb = randomFormula(rand, n, 2);
    const r = existsDisjointPair(fa, fb, n);
    assert.equal(r.found, bruteMinPair(fa, fb, n) !== null, `case ${t}`);
  }
});

console.log(`\n全部 ${passed} 组测试通过`);
