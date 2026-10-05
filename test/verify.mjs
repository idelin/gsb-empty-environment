// Correctness tests: DP result vs. exhaustive enumeration of ALL binary join trees.
import { optimize, optimizeLeftDeep } from "../dist/optimizer.js";

let failures = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log("ok   - " + name);
  } else {
    failures++;
    console.log("FAIL - " + name + (detail ? "  " + detail : ""));
  }
}

// ---------- brute force: enumerate every binary tree over the leaves ----------
function bruteForce(query) {
  const n = query.relations.length;
  const cards = query.relations.map((r) => r.cardinality);
  const opts = query.options || {};
  const allowCross = opts.allowCrossProducts === true;
  const mat = opts.materialization || {};
  const perTuple = mat.perTuple || 0;
  const flat = mat.flat || 0;
  const idx = new Map(query.relations.map((r, i) => [r.name, i]));
  const edges = [];
  for (const p of query.predicates || []) {
    if (p.left === p.right) continue;
    edges.push({ mask: (1 << idx.get(p.left)) | (1 << idx.get(p.right)), sel: p.selectivity });
  }
  function* trees(mask) {
    if ((mask & (mask - 1)) === 0) {
      yield { mask, card: cards[Math.log2(mask)], cost: 0 };
      return;
    }
    const lowest = mask & -mask;
    let s1 = (mask - 1) & mask;
    while (s1 > 0) {
      if (s1 & lowest) {
        const s2 = mask ^ s1;
        for (const t1 of trees(s1)) {
          for (const t2 of trees(s2)) {
            let sel = 1;
            let connected = false;
            for (const e of edges) {
              if ((e.mask & t1.mask) !== 0 && (e.mask & t2.mask) !== 0) {
                sel *= e.sel;
                connected = true;
              }
            }
            if (!connected && !allowCross) continue;
            const card = t1.card * t2.card * sel;
            yield { mask, card, cost: t1.cost + t2.cost + card + flat + perTuple * card };
          }
        }
      }
      s1 = (s1 - 1) & mask;
    }
  }
  let best = Infinity;
  for (const t of trees((1 << n) - 1)) {
    if (t.cost < best) best = t.cost;
  }
  return best;
}

// ---------- deterministic pseudo-random generator ----------
function rng(seed) {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

function randomQuery(rand, n, allowCross, withMat) {
  const relations = [];
  for (let i = 0; i < n; i++) {
    relations.push({ name: "R" + i, cardinality: 10 + Math.floor(rand() * 100000) });
  }
  const predicates = [];
  // random spanning tree to keep the graph connected
  for (let i = 1; i < n; i++) {
    const j = Math.floor(rand() * i);
    predicates.push({ left: "R" + i, right: "R" + j, selectivity: 0.0001 + rand() * 0.9 });
  }
  // extra random edges
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (rand() < 0.15) {
        predicates.push({ left: "R" + i, right: "R" + j, selectivity: 0.0001 + rand() * 0.9 });
      }
    }
  }
  const options = { allowCrossProducts: allowCross };
  if (withMat) {
    options.materialization = { perTuple: rand() * 0.5, flat: Math.floor(rand() * 100) };
  }
  return { relations, predicates, options };
}

// ---------- 1. hand-computed example ----------
{
  // A=100, B=200, sel(A,B)=0.1 -> join card = 2000, cost = 2000 + 10 + 0.5*2000 = 3010
  const q = {
    relations: [
      { name: "A", cardinality: 100 },
      { name: "B", cardinality: 200 },
    ],
    predicates: [{ left: "A", right: "B", selectivity: 0.1 }],
    options: { materialization: { perTuple: 0.5, flat: 10 } },
  };
  const r = optimize(q);
  check("hand-computed 2-relation cost", Math.abs(r.cost - 3010) < 1e-9, "got " + r.cost);
  check("hand-computed root card", r.plan.cardinality === 2000, "got " + r.plan.cardinality);
}

// ---------- 2. bushy beats left-deep on the classic 4-relation example ----------
{
  const q = {
    relations: [
      { name: "A", cardinality: 1000 },
      { name: "B", cardinality: 1000 },
      { name: "C", cardinality: 1000 },
      { name: "D", cardinality: 1000 },
    ],
    predicates: [
      { left: "A", right: "B", selectivity: 0.001 },
      { left: "C", right: "D", selectivity: 0.001 },
      { left: "B", right: "C", selectivity: 0.01 },
    ],
  };
  const bushy = optimize(q);
  const leftDeep = optimizeLeftDeep(q);
  check("bushy cost = 12000", Math.abs(bushy.cost - 12000) < 1e-9, "got " + bushy.cost);
  check("left-deep cost = 21000", Math.abs(leftDeep.cost - 21000) < 1e-9, "got " + leftDeep.cost);
  check("bushy strictly better than left-deep", bushy.cost < leftDeep.cost);
  const rootKids =
    bushy.plan.left.relations.length === 2 && bushy.plan.right.relations.length === 2;
  check("optimal plan is bushy (2+2 split at root)", rootKids);
}

// ---------- 3. cross-product configuration ----------
{
  const q = {
    relations: [
      { name: "X", cardinality: 500 },
      { name: "Y", cardinality: 800 },
      { name: "Z", cardinality: 300 },
    ],
    predicates: [{ left: "X", right: "Y", selectivity: 0.05 }],
    options: { allowCrossProducts: false },
  };
  const forbidden = optimize(q);
  check("disconnected graph + cross products forbidden => no plan", forbidden.plan === null);
  const allowed = optimize({
    relations: q.relations,
    predicates: q.predicates,
    options: { allowCrossProducts: true },
  });
  check("allowing cross products yields a plan", allowed.plan !== null);
  // XY = 500*800*0.05 = 20000; XY x Z = 20000*300 = 6e6; total = 20000 + 6e6 = 6020000
  check("cross-product plan cost", Math.abs(allowed.cost - 6020000) < 1e-6, "got " + allowed.cost);
  check("cross-product node flagged", allowed.plan.crossProduct === true);
}

// ---------- 4. randomized DP vs brute force ----------
{
  const rand = rng(42);
  let mismatches = 0;
  const trials = 40;
  for (let t = 0; t < trials; t++) {
    const n = 2 + Math.floor(rand() * 5); // 2..6 relations
    const allowCross = rand() < 0.5;
    const withMat = rand() < 0.5;
    const q = randomQuery(rand, n, allowCross, withMat);
    const dpCost = optimize(q).cost;
    const bfCost = bruteForce(q);
    const ok =
      (dpCost === Infinity && bfCost === Infinity) ||
      Math.abs(dpCost - bfCost) <= 1e-6 * Math.max(1, Math.abs(bfCost));
    if (!ok) {
      mismatches++;
      console.log("  mismatch trial " + t + ": dp=" + dpCost + " brute=" + bfCost);
    }
  }
  check("randomized DP == brute force (" + trials + " trials)", mismatches === 0);
}

// ---------- 5. 12-relation scale + validation ----------
{
  const rand = rng(7);
  const q = randomQuery(rand, 12, false, true);
  const t0 = Date.now();
  const r = optimize(q);
  const ms = Date.now() - t0;
  check("12 relations: plan found", r.plan !== null);
  check("12 relations: runs fast (" + ms + "ms)", ms < 5000);
  let threw = false;
  try {
    optimize({
      relations: Array.from({ length: 13 }, (_, i) => ({ name: "T" + i, cardinality: 10 })),
      predicates: [],
      options: { allowCrossProducts: true },
    });
  } catch (e) {
    threw = true;
  }
  check("13 relations rejected", threw);
}

console.log("");
if (failures > 0) {
  console.log(failures + " test(s) FAILED");
  process.exit(1);
}
console.log("all tests passed");
