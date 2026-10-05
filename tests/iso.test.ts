import { test } from "node:test";
import assert from "node:assert/strict";
import type { ColoredMultigraph } from "../src/graph.ts";
import { permuteGraph } from "../src/graph.ts";
import { refineColors } from "../src/refine.ts";
import { canonicalForm } from "../src/canonical.ts";
import { checkIsomorphism, verifyIsomorphism, verifyIsoResult } from "../src/isomorphic.ts";

// ---------- 工具 ----------

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

function emptyGraph(n: number, colors?: number[]): ColoredMultigraph {
  return {
    vertexCount: n,
    colors: colors ?? new Array<number>(n).fill(0),
    adj: Array.from({ length: n }, () => new Array<number>(n).fill(0)),
  };
}

function addEdge(g: ColoredMultigraph, u: number, v: number, times = 1): void {
  g.adj[u][v] += times;
  if (u !== v) g.adj[v][u] += times;
}

function randomGraph(rng: () => number, n: number, maxMult: number, numColors: number): ColoredMultigraph {
  const g = emptyGraph(n);
  for (let i = 0; i < n; i++) g.colors[i] = Math.floor(rng() * numColors);
  for (let i = 0; i < n; i++) {
    for (let j = i; j < n; j++) {
      const m = Math.floor(rng() * (maxMult + 1));
      if (m > 0) addEdge(g, i, j, m);
    }
  }
  return g;
}

function* permutations(n: number): Generator<number[]> {
  const a = Array.from({ length: n }, (_, i) => i);
  yield a.slice();
  const c = new Array<number>(n).fill(0);
  let i = 1;
  while (i < n) {
    if (c[i] < i) {
      const k = i % 2 === 0 ? 0 : c[i];
      [a[k], a[i]] = [a[i], a[k]];
      yield a.slice();
      c[i]++;
      i = 1;
    } else {
      c[i] = 0;
      i++;
    }
  }
}

function randomPermutation(rng: () => number, n: number): number[] {
  const p = Array.from({ length: n }, (_, i) => i);
  for (let i = n - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    [p[i], p[j]] = [p[j], p[i]];
  }
  return p;
}

/** 暴力枚举全部双射，作为 n 较小时的地面真值。 */
function bruteForceIsomorphic(a: ColoredMultigraph, b: ColoredMultigraph): boolean {
  if (a.vertexCount !== b.vertexCount) return false;
  const n = a.vertexCount;
  for (const p of permutations(n)) {
    let ok = true;
    for (let i = 0; i < n && ok; i++) {
      if (a.colors[i] !== b.colors[p[i]]) { ok = false; break; }
      for (let j = 0; j < n; j++) {
        if (a.adj[i][j] !== b.adj[p[i]][p[j]]) { ok = false; break; }
      }
    }
    if (ok) return true;
  }
  return false;
}

// ---------- 高对称图构造 ----------

function cycleGraph(n: number): ColoredMultigraph {
  const g = emptyGraph(n);
  for (let i = 0; i < n; i++) addEdge(g, i, (i + 1) % n);
  return g;
}

function disjointUnion(...gs: ColoredMultigraph[]): ColoredMultigraph {
  const n = gs.reduce((s, g) => s + g.vertexCount, 0);
  const out = emptyGraph(n);
  let off = 0;
  for (const g of gs) {
    for (let i = 0; i < g.vertexCount; i++) {
      out.colors[off + i] = g.colors[i];
      for (let j = 0; j < g.vertexCount; j++) out.adj[off + i][off + j] = g.adj[i][j];
    }
    off += g.vertexCount;
  }
  return out;
}

function completeGraph(n: number): ColoredMultigraph {
  const g = emptyGraph(n);
  for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) addEdge(g, i, j);
  return g;
}

function petersenGraph(): ColoredMultigraph {
  const g = emptyGraph(10);
  for (let i = 0; i < 5; i++) {
    addEdge(g, i, (i + 1) % 5);       // 外圈
    addEdge(g, i, i + 5);             // 辐条
    addEdge(g, i + 5, ((i + 2) % 5) + 5); // 内五角星
  }
  return g;
}

function completeBipartite(a: number, b: number): ColoredMultigraph {
  const g = emptyGraph(a + b);
  for (let i = 0; i < a; i++) for (let j = 0; j < b; j++) addEdge(g, i, a + j);
  return g;
}

function triangularPrism(): ColoredMultigraph {
  const g = emptyGraph(6);
  addEdge(g, 0, 1); addEdge(g, 1, 2); addEdge(g, 2, 0);
  addEdge(g, 3, 4); addEdge(g, 4, 5); addEdge(g, 5, 3);
  addEdge(g, 0, 3); addEdge(g, 1, 4); addEdge(g, 2, 5);
  return g;
}

function hypercube(d: number): ColoredMultigraph {
  const n = 1 << d;
  const g = emptyGraph(n);
  for (let v = 0; v < n; v++) for (let bit = 0; bit < d; bit++) addEdge(g, v, v ^ (1 << bit));
  return g;
}

/** Shrikhande 图：Z4×Z4 上差集 {±(1,0), ±(0,1), ±(1,1)} 的 Cayley 图，srg(16,6,2,2)。 */
function shrikhandeGraph(): ColoredMultigraph {
  const g = emptyGraph(16);
  const idx = (x: number, y: number) => ((x + 4) % 4) * 4 + ((y + 4) % 4);
  for (let x = 0; x < 4; x++) {
    for (let y = 0; y < 4; y++) {
      addEdge(g, idx(x, y), idx(x + 1, y));
      addEdge(g, idx(x, y), idx(x, y + 1));
      addEdge(g, idx(x, y), idx(x + 1, y + 1));
    }
  }
  return g;
}

/** 4×4 车图 L(K4,4)：同行或同列相邻，同为 srg(16,6,2,2)，与 Shrikhande 不同构。 */
function rookGraph(): ColoredMultigraph {
  const g = emptyGraph(16);
  const idx = (x: number, y: number) => x * 4 + y;
  for (let x = 0; x < 4; x++) {
    for (let y = 0; y < 4; y++) {
      for (let k = y + 1; k < 4; k++) addEdge(g, idx(x, y), idx(x, k));
      for (let k = x + 1; k < 4; k++) addEdge(g, idx(x, y), idx(k, y));
    }
  }
  return g;
}

// ---------- 测试 ----------

test("规范编码在任意重标号下不变（小图全排列）", () => {
  const rng = mulberry32(42);
  for (let t = 0; t < 8; t++) {
    const g = randomGraph(rng, 5, 2, 3); // 含平行边、自环、节点颜色
    const base = canonicalForm(g);
    assert.ok(base);
    for (const p of permutations(5)) {
      const h = permuteGraph(g, p);
      const ch = canonicalForm(h);
      assert.ok(ch);
      assert.equal(ch.certificate, base.certificate, "重标号后规范编码必须相同");
    }
  }
});

test("高对称图的规范编码在随机重标号下不变", () => {
  const rng = mulberry32(7);
  const graphs = [petersenGraph(), completeGraph(6), cycleGraph(9), hypercube(3), shrikhandeGraph(), rookGraph()];
  for (const g of graphs) {
    const base = canonicalForm(g);
    assert.ok(base);
    for (let t = 0; t < 15; t++) {
      const h = permuteGraph(g, randomPermutation(rng, g.vertexCount));
      const ch = canonicalForm(h);
      assert.ok(ch);
      assert.equal(ch.certificate, base.certificate);
    }
  }
});

test("随机小图判定结果与暴力枚举一致，且映射可核对", () => {
  const rng = mulberry32(1234);
  for (let t = 0; t < 300; t++) {
    const a = randomGraph(rng, 5, 2, 3);
    // 一半概率生成 a 的重标号（必同构），一半生成独立随机图
    const b = t % 2 === 0 ? permuteGraph(a, randomPermutation(rng, 5)) : randomGraph(rng, 5, 2, 3);
    const res = checkIsomorphism(a, b);
    const truth = bruteForceIsomorphic(a, b);
    assert.notEqual(res.status, "unknown");
    assert.equal(res.status === "isomorphic", truth, `第 ${t} 组判定与暴力结果不符`);
    assert.ok(verifyIsoResult(a, b, res), "结论必须可独立核对");
    if (res.status === "isomorphic") {
      assert.ok(verifyIsomorphism(a, b, res.mapping), "返回的映射必须是合法同构");
    }
  }
});

test("颜色直方图相同（细化后仍不可区分）不等于同构", () => {
  const pairs: Array<[string, ColoredMultigraph, ColoredMultigraph]> = [
    ["C6 vs 2×C3", cycleGraph(6), disjointUnion(cycleGraph(3), cycleGraph(3))],
    ["C8 vs C3+C5", cycleGraph(8), disjointUnion(cycleGraph(3), cycleGraph(5))],
    ["三棱柱 vs K3,3", triangularPrism(), completeBipartite(3, 3)],
    ["Shrikhande vs 4×4车图", shrikhandeGraph(), rookGraph()],
  ];
  for (const [name, a, b] of pairs) {
    // 前提：1-WL 细化确实无法区分（全图只有一个颜色类）
    assert.equal(new Set(refineColors(a, a.colors)).size, 1, `${name}: 前提应满足 WL 不可区分`);
    assert.equal(new Set(refineColors(b, b.colors)).size, 1, `${name}: 前提应满足 WL 不可区分`);
    const res = checkIsomorphism(a, b);
    assert.equal(res.status, "non-isomorphic", `${name} 必须判为不同构`);
    assert.ok(verifyIsoResult(a, b, res), `${name}: 不同构结论必须可核对`);
  }
});

test("高对称图自同构：重标号后同构且映射正确", () => {
  const rng = mulberry32(99);
  const graphs = [petersenGraph(), completeGraph(7), hypercube(3), shrikhandeGraph(), rookGraph(), cycleGraph(12)];
  for (const g of graphs) {
    for (let t = 0; t < 8; t++) {
      const p = randomPermutation(rng, g.vertexCount);
      const h = permuteGraph(g, p);
      const res = checkIsomorphism(g, h);
      assert.equal(res.status, "isomorphic");
      if (res.status === "isomorphic") assert.ok(verifyIsomorphism(g, h, res.mapping));
    }
  }
});

test("平行边与自环的重数参与判定", () => {
  // 同一简单骨架、不同重数模式 ⇒ 不同构
  const a = emptyGraph(3);
  addEdge(a, 0, 1, 2); addEdge(a, 1, 2, 1);
  const b = emptyGraph(3);
  addEdge(b, 0, 1, 2); addEdge(b, 1, 2, 2);
  assert.equal(checkIsomorphism(a, b).status, "non-isomorphic");

  // 重数模式的重标号 ⇒ 同构
  const c = emptyGraph(3);
  addEdge(c, 2, 0, 1); addEdge(c, 0, 1, 2);
  const res = checkIsomorphism(a, c);
  assert.equal(res.status, "isomorphic");
  if (res.status === "isomorphic") assert.ok(verifyIsomorphism(a, c, res.mapping));

  // 自环有无 ⇒ 不同构
  const d = emptyGraph(2);
  addEdge(d, 0, 0, 1);
  const e = emptyGraph(2);
  addEdge(e, 0, 1, 1);
  assert.equal(checkIsomorphism(d, e).status, "non-isomorphic");
});

test("节点颜色参与判定", () => {
  const a = emptyGraph(3, [0, 0, 1]);
  addEdge(a, 0, 1); addEdge(a, 1, 2);
  const b = emptyGraph(3, [0, 1, 0]);
  addEdge(b, 0, 1); addEdge(b, 1, 2);
  // 直方图相同，但颜色位置相对结构不同 ⇒ 不同构
  const res = checkIsomorphism(a, b);
  assert.equal(res.status, "non-isomorphic");
  assert.ok(verifyIsoResult(a, b, res));
});

test("预算耗尽返回 unknown，且可核对耗尽事实", () => {
  const p = petersenGraph(); // 细化后仍是单一颜色类，必须搜索
  const res = checkIsomorphism(p, p, 0);
  assert.equal(res.status, "unknown");
  if (res.status === "unknown") assert.equal(res.reason, "budget-exhausted");
  assert.ok(verifyIsoResult(p, p, res, 0));
  // 预算充足时同一对图必然可判定
  assert.equal(checkIsomorphism(p, p, 1000).status, "isomorphic");
});

test("顶点数或颜色直方图不同直接判不同构", () => {
  const a = cycleGraph(4);
  const b = cycleGraph(5);
  assert.equal(checkIsomorphism(a, b).status, "non-isomorphic");
  const c = emptyGraph(4, [0, 0, 0, 1]);
  const d = emptyGraph(4, [0, 0, 1, 1]);
  const res = checkIsomorphism(c, d);
  assert.equal(res.status, "non-isomorphic");
  assert.ok(verifyIsoResult(c, d, res));
});
