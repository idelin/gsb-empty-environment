import { test } from "node:test";
import assert from "node:assert/strict";
import { compileSpec, evalExact, triEval, type Spec, type SpecJson } from "../src/spec.ts";
import {
  checkIntersection,
  createStats,
  findDisjointPair,
  findMinSatisfying,
  minQuorumSize,
} from "../src/solver.ts";

function makeSpec(json: SpecJson, n: number): Spec {
  const names = Array.from({ length: n }, (_, i) => `n${i}`);
  return compileSpec(json, new Map(names.map((s, i) => [s, i])), n);
}

function toAssign(set: number[], n: number): Uint8Array {
  const a = new Uint8Array(n).fill(2);
  for (const i of set) a[i] = 1;
  return a;
}

// ---- 暴力预言机（仅测试用）：按字典序枚举全部子集 ----

function allSubsetsDictOrder(n: number): number[][] {
  const out: number[][] = [];
  const prefix: number[] = [];
  function walk(idx: number): void {
    out.push([...prefix]);
    for (let j = idx; j < n; j++) {
      prefix.push(j);
      walk(j + 1);
      prefix.pop();
    }
  }
  walk(0);
  return out;
}

function bruteForcePair(specA: Spec, specB: Spec, n: number): { a: number[]; b: number[] } | null {
  const sets = allSubsetsDictOrder(n);
  const satA = sets.filter((s) => evalExact(specA, toAssign(s, n)));
  const satB = sets.filter((s) => evalExact(specB, toAssign(s, n)));
  for (const a of satA) {
    const inA = new Set(a);
    for (const b of satB) {
      if (b.every((x) => !inA.has(x))) return { a, b };
    }
  }
  return null;
}

function bruteForceMinSize(spec: Spec, n: number): number {
  let best = n + 1;
  for (const s of allSubsetsDictOrder(n)) {
    if (s.length < best && evalExact(spec, toAssign(s, n))) best = s.length;
  }
  return best;
}

function bruteForceMinSatisfying(spec: Spec, n: number, allowed: boolean[]): number[] | null {
  for (const s of allSubsetsDictOrder(n)) {
    if (!s.every((x) => allowed[x])) continue;
    if (evalExact(spec, toAssign(s, n))) return s;
  }
  return null;
}

// ---- 基础求值 ----

test("triEval 三值语义", () => {
  const spec = makeSpec({ atLeast: 2, of: ["n0", "n1", "n2"] }, 3);
  assert.equal(triEval(spec, toAssign([0, 1], 3)), 1);
  assert.equal(triEval(spec, toAssign([0], 3)), -1); // 只剩 n2 未定？不，toAssign 全部确定
  const partial = new Uint8Array([1, 0, 0]); // n0 在内，其余未定
  assert.equal(triEval(spec, partial), 0);
  const dead = new Uint8Array([1, 2, 0]); // n1 在外，最多再选 n2 也只有 2 个？1+1=2 可达
  assert.equal(triEval(spec, dead), 0);
  const dead2 = new Uint8Array([1, 2, 2]);
  assert.equal(triEval(spec, dead2), -1);
});

test("all/any 组合求值", () => {
  const spec = makeSpec(
    {
      any: [
        { all: [{ atLeast: 2, of: ["n0", "n1", "n2"] }, { atLeast: 1, of: ["n3"] }] },
        { atLeast: 3, of: ["n0", "n1", "n2", "n3"] },
      ],
    },
    4,
  );
  assert.equal(evalExact(spec, toAssign([0, 1, 3], 4)), true);
  assert.equal(evalExact(spec, toAssign([0, 1], 4)), false);
  assert.equal(evalExact(spec, toAssign([1, 2, 3], 4)), true); // 走第二个分支
});

// ---- 经典场景 ----

test("多数派法定集合 n=5：读写、写写均成立（大小论证）", () => {
  const read = makeSpec({ atLeast: 3, of: "*" }, 5);
  const write = makeSpec({ atLeast: 3, of: "*" }, 5);
  const rw = checkIntersection(read, write, 5, "读", "写");
  assert.equal(rw.holds, true);
  assert.equal(rw.proof!.method, "size-argument");
  const ww = checkIntersection(write, write, 5, "写", "写");
  assert.equal(ww.holds, true);
  assert.equal(ww.proof!.method, "size-argument");
});

test("n=5 读3写2：读写不相交见证为字典序最小", () => {
  const read = makeSpec({ atLeast: 3, of: "*" }, 5);
  const write = makeSpec({ atLeast: 2, of: "*" }, 5);
  const rw = checkIntersection(read, write, 5, "读", "写");
  assert.equal(rw.holds, false);
  assert.deepEqual(rw.witness, { a: [0, 1, 2], b: [3, 4] });
});

test("n=4 写2：写写见证 W1=[0,1] W2=[2,3]", () => {
  const write = makeSpec({ atLeast: 2, of: "*" }, 4);
  const ww = checkIntersection(write, write, 4, "写", "写");
  assert.equal(ww.holds, false);
  assert.deepEqual(ww.witness, { a: [0, 1], b: [2, 3] });
});

test("3x3 网格法定集合（行+列压缩描述）：写写、读写均成立", () => {
  // 节点 r{1..3}c{1..3}；写 = 含某整行且含某整列；读 = 任意 5 个
  const names: string[] = [];
  for (let r = 1; r <= 3; r++) for (let c = 1; c <= 3; c++) names.push(`r${r}c${c}`);
  const rows = [0, 1, 2].map((r) => names.slice(r * 3, r * 3 + 3));
  const cols = [0, 1, 2].map((c) => [names[c], names[c + 3], names[c + 6]]);
  const writeJson: SpecJson = {
    all: [
      { any: rows.map((g) => ({ atLeast: 3, of: g })) },
      { any: cols.map((g) => ({ atLeast: 3, of: g })) },
    ],
  };
  const indexOf = new Map(names.map((s, i) => [s, i]));
  const write = compileSpec(writeJson, indexOf, 9);
  const read = compileSpec({ atLeast: 5, of: "*" }, indexOf, 9);
  const ww = checkIntersection(write, write, 9, "写", "写");
  assert.equal(ww.holds, true);
  const rw = checkIntersection(read, write, 9, "读", "写");
  assert.equal(rw.holds, true);
  assert.equal(rw.proof!.method, "size-argument"); // 5+5=10 > 9
});

test("不可满足规格导致空虚成立", () => {
  const read = makeSpec({ atLeast: 6, of: "*" }, 5); // 不可能
  const write = makeSpec({ atLeast: 3, of: "*" }, 5);
  const rw = checkIntersection(read, write, 5, "读", "写");
  assert.equal(rw.holds, true);
  assert.equal(rw.proof!.method, "vacuous");
});

test("需要完备搜索才能证明的案例（大小论证不够）", () => {
  // Fano 平面：7 条线的两两相交族。写集合 = 包含某条线（至少3个该线组）。
  // 最小写集合大小 = 3，2*3=6 <= 7，大小论证失败；
  // 但任意两条 Fano 线恰交于一点，故任意两写集合必相交，需搜索证明。
  const lines = [
    [0, 1, 2],
    [0, 3, 4],
    [0, 5, 6],
    [1, 3, 5],
    [1, 4, 6],
    [2, 3, 6],
    [2, 4, 5],
  ];
  const write = makeSpec(
    { any: lines.map((line) => ({ atLeast: 3, of: line.map((i) => `n${i}`) })) },
    7,
  );
  const ww = checkIntersection(write, write, 7, "写", "写");
  assert.equal(ww.holds, true);
  assert.equal(ww.proof!.method, "exhaustive-search");
  assert.ok(ww.proof!.stats.nodesExpanded > 0);
  assert.ok(ww.proof!.stats.prunes > 0);
});

// ---- 与暴力预言机交叉验证 ----

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1103515245 + 12345) & 0x7fffffff;
    return s / 0x7fffffff;
  };
}

function randomSpecJson(rand: () => number, n: number, depth: number): SpecJson {
  if (depth === 0 || rand() < 0.5) {
    const size = 1 + Math.floor(rand() * n);
    const pool = Array.from({ length: n }, (_, i) => `n${i}`);
    for (let i = pool.length - 1; i > 0; i--) {
      const j = Math.floor(rand() * (i + 1));
      [pool[i], pool[j]] = [pool[j], pool[i]];
    }
    const k = Math.floor(rand() * (size + 2)); // 允许 k > size（不可满足原子）
    return { atLeast: k, of: pool.slice(0, size) };
  }
  const m = 1 + Math.floor(rand() * 3);
  const children = Array.from({ length: m }, () => randomSpecJson(rand, n, depth - 1));
  return rand() < 0.5 ? { all: children } : { any: children };
}

test("随机规格：findDisjointPair 与暴力枚举一致（含字典序最小性）", () => {
  for (let seed = 1; seed <= 300; seed++) {
    const rand = lcg(seed);
    const n = 1 + Math.floor(rand() * 8);
    const specA = makeSpec(randomSpecJson(rand, n, 2), n);
    const specB = makeSpec(randomSpecJson(rand, n, 2), n);
    const stats = createStats();
    const got = findDisjointPair(specA, specB, n, stats);
    const want = bruteForcePair(specA, specB, n);
    assert.deepEqual(got, want, `seed=${seed} n=${n}`);
  }
});

test("随机规格：minQuorumSize 与暴力一致", () => {
  for (let seed = 301; seed <= 450; seed++) {
    const rand = lcg(seed);
    const n = 1 + Math.floor(rand() * 8);
    const spec = makeSpec(randomSpecJson(rand, n, 2), n);
    const stats = createStats();
    assert.equal(minQuorumSize(spec, n, stats), bruteForceMinSize(spec, n), `seed=${seed}`);
  }
});

test("随机规格：findMinSatisfying（含 allowed 限制）与暴力一致", () => {
  for (let seed = 451; seed <= 600; seed++) {
    const rand = lcg(seed);
    const n = 1 + Math.floor(rand() * 8);
    const spec = makeSpec(randomSpecJson(rand, n, 2), n);
    const allowed = Array.from({ length: n }, () => rand() < 0.7);
    const stats = createStats();
    assert.deepEqual(
      findMinSatisfying(spec, n, allowed, stats),
      bruteForceMinSatisfying(spec, n, allowed),
      `seed=${seed}`,
    );
  }
});

test("checkIntersection 与暴力判定一致", () => {
  for (let seed = 601; seed <= 800; seed++) {
    const rand = lcg(seed);
    const n = 1 + Math.floor(rand() * 8);
    const specA = makeSpec(randomSpecJson(rand, n, 2), n);
    const specB = makeSpec(randomSpecJson(rand, n, 2), n);
    const outcome = checkIntersection(specA, specB, n, "甲", "乙");
    const witness = bruteForcePair(specA, specB, n);
    assert.equal(outcome.holds, witness === null, `seed=${seed}`);
    if (!outcome.holds) assert.deepEqual(outcome.witness, witness, `seed=${seed}`);
  }
});
