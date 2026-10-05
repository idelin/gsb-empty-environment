import test from "node:test";
import assert from "node:assert/strict";
import {
  createGraph,
  relabel,
  isomorphic,
  canonicalForm,
  verifyIsomorphism,
  type ColoredGraph,
} from "../src/graphiso.ts";

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

function* permutations(n: number): Generator<number[]> {
  const a = Array.from({ length: n }, (_, i) => i);
  yield a.slice();
  const c = new Array<number>(n).fill(0);
  let i = 1;
  while (i < n) {
    if (c[i] < i) {
      const j = i % 2 === 0 ? 0 : c[i];
      [a[i], a[j]] = [a[j], a[i]];
      yield a.slice();
      c[i]++;
      i = 1;
    } else {
      c[i] = 0;
      i++;
    }
  }
}

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomPermutation(n: number, rand: () => number): number[] {
  const a = Array.from({ length: n }, (_, i) => i);
  for (let i = n - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [a[i], a[j]] = [a[j], a[i]];
  }
  return a;
}

const mono = (n: number): number[] => new Array<number>(n).fill(0);

// ---------------------------------------------------------------------------
// 图构造
// ---------------------------------------------------------------------------

function complete(n: number): ColoredGraph {
  const edges: [number, number][] = [];
  for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) edges.push([i, j]);
  return createGraph(n, mono(n), edges);
}

function cycle(n: number): ColoredGraph {
  const edges: [number, number][] = [];
  for (let i = 0; i < n; i++) edges.push([i, (i + 1) % n]);
  return createGraph(n, mono(n), edges);
}

function completeBipartite(a: number, b: number): ColoredGraph {
  const edges: [number, number][] = [];
  for (let i = 0; i < a; i++) for (let j = 0; j < b; j++) edges.push([i, a + j]);
  return createGraph(a + b, mono(a + b), edges);
}

function petersen(): ColoredGraph {
  const edges: [number, number][] = [];
  for (let i = 0; i < 5; i++) {
    edges.push([i, (i + 1) % 5]); // 外圈五边形
    edges.push([5 + i, 5 + ((i + 2) % 5)]); // 内圈五角星
    edges.push([i, 5 + i]); // 辐条
  }
  return createGraph(10, mono(10), edges);
}

function hypercube(d: number): ColoredGraph {
  const n = 1 << d;
  const edges: [number, number][] = [];
  for (let v = 0; v < n; v++) {
    for (let bit = 0; bit < d; bit++) {
      const u = v ^ (1 << bit);
      if (v < u) edges.push([v, u]);
    }
  }
  return createGraph(n, mono(n), edges);
}

/** Shrikhande 图：强正则 (16,6,2,2)，1-WL 不可分。 */
function shrikhande(): ColoredGraph {
  const idx = (x: number, y: number) => 4 * ((x % 4 + 4) % 4) + ((y % 4 + 4) % 4);
  const edges = new Set<string>();
  for (let x = 0; x < 4; x++) {
    for (let y = 0; y < 4; y++) {
      for (const [dx, dy] of [[1, 0], [0, 1], [1, 1], [1, -1]] as const) {
        const a = idx(x, y);
        const b = idx(x + dx, y + dy);
        if (a !== b) edges.add(a < b ? `${a},${b}` : `${b},${a}`);
      }
    }
  }
  const list = [...edges].map((s) => s.split(",").map(Number) as [number, number]);
  return createGraph(16, mono(16), list);
}

/** 4x4 车图 L2(4)：与 Shrikhande 同参数强正则但不同构。 */
function rook4(): ColoredGraph {
  const edges: [number, number][] = [];
  const idx = (x: number, y: number) => 4 * x + y;
  for (let x = 0; x < 4; x++) {
    for (let y = 0; y < 4; y++) {
      for (let k = 0; k < 4; k++) {
        if (k !== y) edges.push([idx(x, y), idx(x, k)]);
        if (k !== x) edges.push([idx(x, y), idx(k, y)]);
      }
    }
  }
  return createGraph(16, mono(16), edges);
}

/** 三棱柱：6 顶点 3 正则，含三角形（与 K3,3 不同构）。 */
function prism(): ColoredGraph {
  return createGraph(6, mono(6), [
    [0, 1], [1, 2], [2, 0],
    [3, 4], [4, 5], [5, 3],
    [0, 3], [1, 4], [2, 5],
  ]);
}

/** 带平行边的多重图：4-环，对边加倍。 */
function multiOpposite(): ColoredGraph {
  return createGraph(4, mono(4), [
    [0, 1], [0, 1], [2, 3], [2, 3], [1, 2], [3, 0],
  ]);
}

/** 4-环，邻边加倍（与 multiOpposite 度序列相同但不同构）。 */
function multiAdjacent(): ColoredGraph {
  return createGraph(4, mono(4), [
    [0, 1], [0, 1], [1, 2], [1, 2], [2, 3], [3, 0],
  ]);
}

/** 带自环与平行边的小图。 */
function loopGraph(): ColoredGraph {
  return createGraph(3, [0, 1, 1], [
    [0, 0], [0, 1], [0, 1], [1, 2],
  ]);
}

// ---------------------------------------------------------------------------
// 断言辅助
// ---------------------------------------------------------------------------

function assertIsoUnderPerm(g: ColoredGraph, perm: number[]): void {
  const g2 = relabel(g, perm);
  const r = isomorphic(g, g2);
  assert.equal(r.status, "isomorphic", `perm=${perm} reason=${JSON.stringify(r)}`);
  if (r.status === "isomorphic") {
    assert.ok(verifyIsomorphism(g, g2, r.mapping), "returned mapping must verify");
  }
  const c1 = canonicalForm(g);
  const c2 = canonicalForm(g2);
  assert.equal(c1.status, "ok");
  assert.equal(c2.status, "ok");
  if (c1.status === "ok" && c2.status === "ok") {
    assert.equal(c1.canonical, c2.canonical, "canonical form must be relabeling-invariant");
  }
}

// ---------------------------------------------------------------------------
// 测试
// ---------------------------------------------------------------------------

test("小图全排列：高对称图在任意顶点重标号下判同构且规范编码一致", () => {
  const cases: Array<[string, ColoredGraph]> = [
    ["K4", complete(4)],
    ["K5", complete(5)],
    ["C5", cycle(5)],
    ["C6", cycle(6)],
    ["K3,3", completeBipartite(3, 3)],
    ["K2,3", completeBipartite(2, 3)],
    ["prism", prism()],
    ["multiOpposite", multiOpposite()],
    ["multiAdjacent", multiAdjacent()],
    ["loopGraph", loopGraph()],
    ["coloredPath", createGraph(4, [0, 0, 1, 1], [[0, 1], [1, 2], [2, 3]])],
  ];
  for (const [name, g] of cases) {
    assert.ok(g.n <= 6, `${name}: keep n<=6 for exhaustive permutations`);
    let count = 0;
    for (const perm of permutations(g.n)) {
      assertIsoUnderPerm(g, perm);
      count++;
    }
    // 确认真的枚举了 n! 个排列
    let fact = 1;
    for (let i = 2; i <= g.n; i++) fact *= i;
    assert.equal(count, fact, `${name}: enumerated all permutations`);
  }
});

test("大对称图随机重标号：Petersen / Q3 / Shrikhande / Rook4", () => {
  const rand = mulberry32(20261005);
  const cases: Array<[string, ColoredGraph, number]> = [
    ["petersen", petersen(), 150],
    ["Q3", hypercube(3), 150],
    ["shrikhande", shrikhande(), 60],
    ["rook4", rook4(), 60],
  ];
  for (const [, g, trials] of cases) {
    for (let t = 0; t < trials; t++) {
      assertIsoUnderPerm(g, randomPermutation(g.n, rand));
    }
  }
});

test("不同构对：结构差异必须被检出", () => {
  const twoTriangles = createGraph(6, mono(6), [
    [0, 1], [1, 2], [2, 0], [3, 4], [4, 5], [5, 3],
  ]);
  const pairs: Array<[string, ColoredGraph, ColoredGraph]> = [
    ["C6 vs 2*C3", cycle(6), twoTriangles],
    ["K3,3 vs prism", completeBipartite(3, 3), prism()],
    ["shrikhande vs rook4", shrikhande(), rook4()],
    ["multiOpposite vs multiAdjacent", multiOpposite(), multiAdjacent()],
    ["colored path vs alternating path",
      createGraph(4, [0, 0, 1, 1], [[0, 1], [1, 2], [2, 3]]),
      createGraph(4, [0, 1, 0, 1], [[0, 1], [1, 2], [2, 3]])],
    ["C4 vs C3+isolated", cycle(4), createGraph(4, mono(4), [[0, 1], [1, 2], [2, 0]])],
    ["loop vs no loop", loopGraph(), createGraph(3, [0, 1, 1], [[0, 1], [0, 1], [1, 2], [1, 2]])],
  ];
  for (const [name, g1, g2] of pairs) {
    const r = isomorphic(g1, g2);
    assert.equal(r.status, "not-isomorphic", `${name}: ${JSON.stringify(r)}`);
    if (r.status === "not-isomorphic" && r.canonical1 !== undefined) {
      assert.notEqual(r.canonical1, r.canonical2, `${name}: certificates must differ`);
    }
  }
});

test("颜色直方图相同不推出同构（直方图陷阱）", () => {
  // Shrikhande 与 Rook4：全 0 色、同为 6 正则 16 顶点，1-WL 完全不可分
  const s = shrikhande();
  const r = rook4();
  assert.deepEqual(
    s.colors.slice().sort(),
    r.colors.slice().sort(),
    "precondition: identical color histograms",
  );
  const res = isomorphic(s, r);
  assert.equal(res.status, "not-isomorphic");

  // 着色直方图相同但颜色结构不同
  const g1 = createGraph(4, [0, 0, 1, 1], [[0, 1], [1, 2], [2, 3]]);
  const g2 = createGraph(4, [0, 1, 0, 1], [[0, 1], [1, 2], [2, 3]]);
  assert.deepEqual(g1.colors.slice().sort(), g2.colors.slice().sort());
  assert.equal(isomorphic(g1, g2).status, "not-isomorphic");
});

test("预算耗尽返回 unknown，而不是误判", () => {
  const g = petersen();
  const g2 = relabel(g, [3, 0, 7, 1, 9, 2, 8, 4, 6, 5]);
  const r = isomorphic(g, g2, 1);
  assert.equal(r.status, "unknown");
  const c = canonicalForm(g, 1);
  assert.equal(c.status, "unknown");
  // 预算充足时同一对图必须判同构
  const ok = isomorphic(g, g2);
  assert.equal(ok.status, "isomorphic");
});

test("核对器拒绝非法映射", () => {
  const g = createGraph(4, [0, 0, 1, 2], [[0, 1], [1, 2], [2, 3], [3, 0]]);
  assert.ok(verifyIsomorphism(g, g, [0, 1, 2, 3]));
  assert.ok(!verifyIsomorphism(g, g, [2, 1, 0, 3]), "color violation must fail");
  assert.ok(!verifyIsomorphism(g, g, [0, 0, 1, 2]), "non-bijection must fail");
  assert.ok(!verifyIsomorphism(g, g, [0, 1, 2]), "wrong length must fail");
  assert.ok(!verifyIsomorphism(g, g, [0, 2, 1, 3]), "edge violation must fail");
});

test("边界：空图、单点、自环、平行边计数", () => {
  const e1 = createGraph(0, [], []);
  const e2 = createGraph(0, [], []);
  assert.equal(isomorphic(e1, e2).status, "isomorphic");

  const s1 = createGraph(1, [7], []);
  const s2 = createGraph(1, [7], []);
  const s3 = createGraph(1, [8], []);
  assert.equal(isomorphic(s1, s2).status, "isomorphic");
  assert.equal(isomorphic(s1, s3).status, "not-isomorphic");

  // 平行边重数必须精确匹配
  const d1 = createGraph(2, mono(2), [[0, 1], [0, 1]]);
  const d2 = createGraph(2, mono(2), [[0, 1], [0, 1], [0, 1]]);
  assert.equal(isomorphic(d1, d2).status, "not-isomorphic");
  const d3 = relabel(d1, [1, 0]);
  const rd = isomorphic(d1, d3);
  assert.equal(rd.status, "isomorphic");
  if (rd.status === "isomorphic") assert.ok(verifyIsomorphism(d1, d3, rd.mapping));
});

test("规范编码对同一抽象图的任意输入顺序恒定", () => {
  const rand = mulberry32(42);
  const g = petersen();
  const base = canonicalForm(g);
  assert.equal(base.status, "ok");
  if (base.status !== "ok") return;
  for (let t = 0; t < 100; t++) {
    const c = canonicalForm(relabel(g, randomPermutation(g.n, rand)));
    assert.equal(c.status, "ok");
    if (c.status === "ok") assert.equal(c.canonical, base.canonical);
  }
});
