import { readFileSync, mkdirSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { stripTypes } from "../scripts/strip-types.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(here, "../src/seam-carver.ts"), "utf8");
mkdirSync(join(here, ".build"), { recursive: true });
const outFile = join(here, ".build/seam-carver.mjs");
writeFileSync(outFile, stripTypes(src));
const { SeamCarver, carveSeams, gradientEnergy } = await import(outFile);

let passed = 0;
let failed = 0;
function check(name, cond) {
  if (cond) { passed++; console.log("ok   - " + name); }
  else { failed++; console.log("FAIL - " + name); }
}
function throws(name, fn) {
  try { fn(); failed++; console.log("FAIL - " + name + " (no throw)"); }
  catch { passed++; console.log("ok   - " + name); }
}

// Brute-force minimum seam energy over all valid top-to-bottom paths
// (|dx| <= 1 between adjacent rows), never visiting protected cells.
function bruteForceMinEnergy(energy, protectedSet) {
  const h = energy.length, w = energy[0].length;
  const memo = new Map();
  function dfs(y, x) {
    if (protectedSet.has(y + "," + x)) return Infinity;
    if (y === h - 1) return energy[y][x];
    const key = y * w + x;
    if (memo.has(key)) return memo.get(key);
    let best = Infinity;
    for (let dx = -1; dx <= 1; dx++) {
      const nx = x + dx;
      if (nx < 0 || nx >= w) continue;
      const sub = dfs(y + 1, nx);
      if (sub < best) best = sub;
    }
    const result = best === Infinity ? Infinity : best + energy[y][x];
    memo.set(key, result);
    return result;
  }
  let best = Infinity;
  for (let x = 0; x < w; x++) {
    const v = dfs(0, x);
    if (v < best) best = v;
  }
  return best;
}

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// 1. Hand-computed minimum path.
{
  const energy = [
    [1, 4, 3],
    [5, 2, 1],
    [3, 3, 1],
  ];
  const report = carveSeams(energy, 1);
  check("known map: feasible", report.feasible);
  check("known map: energy 4", report.seams[0].energy === 4);
  check(
    "known map: path drifts right",
    JSON.stringify(report.seams[0].path) ===
      JSON.stringify([{ x: 0, y: 0 }, { x: 1, y: 1 }, { x: 2, y: 2 }])
  );
}

// 2. Protected point on the optimal path forces a detour.
{
  const energy = [
    [1, 4, 3],
    [5, 2, 1],
    [3, 3, 1],
  ];
  const report = carveSeams(energy, 1, { protectedPoints: [{ x: 1, y: 1 }] });
  const seam = report.seams[0];
  check("protected: energy 5", seam.energy === 5);
  check(
    "protected: path avoids protected cell",
    seam.path.every((p) => !(p.x === 1 && p.y === 1))
  );
  check(
    "protected: exact path",
    JSON.stringify(seam.path) ===
      JSON.stringify([{ x: 2, y: 0 }, { x: 2, y: 1 }, { x: 2, y: 2 }])
  );
}

// 3. Fully protected row => no feasible seam.
{
  const energy = [
    [1, 2, 3],
    [4, 5, 6],
    [7, 8, 9],
  ];
  const report = carveSeams(energy, 1, {
    protectedPoints: [{ x: 0, y: 1 }, { x: 1, y: 1 }, { x: 2, y: 1 }],
  });
  check("blocked row: not feasible", !report.feasible);
  check("blocked row: no seams", report.seams.length === 0);
  check("blocked row: reason reported", report.reason.length > 0);
}

// 4. Infeasibility appears mid-way through consecutive removals.
{
  const energy = [
    [1, 9],
    [1, 9],
    [1, 9],
  ];
  const report = carveSeams(energy, 2, {
    protectedPoints: [{ x: 1, y: 0 }, { x: 1, y: 1 }, { x: 1, y: 2 }],
  });
  check("mid-way block: one seam removed", report.removed === 1);
  check("mid-way block: not feasible overall", !report.feasible);
  check(
    "mid-way block: first seam is column 0",
    report.seams[0].path.every((p) => p.x === 0)
  );
}

// 5. Coordinates map back to the original image across rounds.
{
  const energy = [
    [5, 1, 5, 1, 5],
    [5, 1, 5, 1, 5],
    [5, 1, 5, 1, 5],
    [5, 1, 5, 1, 5],
  ];
  const report = carveSeams(energy, 3);
  check("mapping: 3 seams removed", report.removed === 3);
  const removedPerRow = [[], [], [], []];
  let ok = true;
  for (const seam of report.seams) {
    for (const p of seam.path) {
      if (p.y < 0 || p.y > 3 || p.x < 0 || p.x > 4) ok = false;
      if (removedPerRow[p.y].includes(p.x)) ok = false; // already removed earlier
      removedPerRow[p.y].push(p.x);
    }
  }
  check("mapping: original coords, never reused", ok);
  check(
    "mapping: seams are the two low-energy columns plus one more",
    JSON.stringify(report.seams[0].path.map((p) => p.x)) === JSON.stringify([1, 1, 1, 1]) &&
    JSON.stringify(report.seams[1].path.map((p) => p.x)) === JSON.stringify([3, 3, 3, 3])
  );
}

// 6. Energy is recomputed after every single removal (never batch-selected).
{
  const energy = [
    [1, 1, 1],
    [1, 1, 1],
    [1, 1, 1],
  ];
  const calls = [];
  const recomputeEnergy = (remaining) => {
    calls.push(remaining.map((row) => row.map((p) => p.x)));
    // Penalize the current leftmost column so the next seam must move.
    return remaining.map((row) => row.map((p, i) => (i === 0 ? 50 : p.energy)));
  };
  const report = carveSeams(energy, 2, { recomputeEnergy });
  check("recompute: called after each removal", calls.length === 2);
  check(
    "recompute: widths shrink one column at a time",
    calls[0][0].length === 2 && calls[1][0].length === 1
  );
  check(
    "recompute: remaining original coords reported",
    JSON.stringify(calls[0][0]) === JSON.stringify([1, 2]) &&
    JSON.stringify(calls[1][0]) === JSON.stringify([1])
  );
  check(
    "recompute: second seam respects new energy",
    report.seams[1].path.every((p) => p.x === 2)
  );
}

// 7. Randomized cross-check against brute force (single seam).
{
  const rand = mulberry32(12345);
  let ok = true;
  for (let iter = 0; iter < 300; iter++) {
    const h = 1 + Math.floor(rand() * 6);
    const w = 1 + Math.floor(rand() * 6);
    const energy = [];
    const protectedPoints = [];
    const protectedSet = new Set();
    for (let y = 0; y < h; y++) {
      const row = [];
      for (let x = 0; x < w; x++) {
        row.push(Math.floor(rand() * 10));
        if (rand() < 0.25) {
          protectedPoints.push({ x, y });
          protectedSet.add(y + "," + x);
        }
      }
      energy.push(row);
    }
    const expected = bruteForceMinEnergy(energy, protectedSet);
    const seam = new SeamCarver(energy, { protectedPoints }).carveOne();
    if (expected === Infinity) {
      if (seam !== null) { ok = false; break; }
      continue;
    }
    if (seam === null || seam.energy !== expected) { ok = false; break; }
    if (seam.path.length !== h) { ok = false; break; }
    let sum = 0;
    for (let y = 0; y < h; y++) {
      const p = seam.path[y];
      if (p.y !== y) { ok = false; break; }
      if (y > 0 && Math.abs(p.x - seam.path[y - 1].x) > 1) { ok = false; break; }
      if (protectedSet.has(y + "," + p.x)) { ok = false; break; }
      sum += energy[y][p.x];
    }
    if (sum !== seam.energy) ok = false;
    if (!ok) break;
  }
  check("randomized: matches brute force (300 cases)", ok);
}

// 8. Randomized multi-round carving with recomputation tracking.
{
  const rand = mulberry32(999);
  let ok = true;
  for (let iter = 0; iter < 100; iter++) {
    const h = 2 + Math.floor(rand() * 4);
    const w = 3 + Math.floor(rand() * 4);
    const energy = [];
    for (let y = 0; y < h; y++) {
      const row = [];
      for (let x = 0; x < w; x++) row.push(Math.floor(rand() * 20));
      energy.push(row);
    }
    // Test-side shadow state: original x still present per row + current energy.
    let shadow = energy.map((row, y) =>
      row.map((e, x) => ({ x, y, energy: e }))
    );
    const recomputeEnergy = (remaining) => {
      // Deterministic pseudo-recomputation the test can mirror.
      return remaining.map((row) =>
        row.map((p, i) => (p.energy + i * 3 + p.y) % 17)
      );
    };
    const carver = new SeamCarver(energy, { recomputeEnergy });
    const rounds = 1 + Math.floor(rand() * (w - 1));
    for (let r = 0; r < rounds; r++) {
      const seam = carver.carveOne();
      if (seam === null) { ok = false; break; }
      // Verify reported energy equals sum over shadow energies at reported original x.
      let sum = 0;
      for (let y = 0; y < h; y++) {
        const p = seam.path[y];
        const idx = shadow[y].findIndex((c) => c.x === p.x);
        if (idx < 0) { ok = false; break; }
        sum += shadow[y][idx].energy;
        shadow[y].splice(idx, 1);
      }
      if (!ok || sum !== seam.energy) { ok = false; break; }
      // Mirror the recomputation.
      shadow = recomputeEnergy(shadow).map((row, y) =>
        row.map((e, i) => ({ x: shadow[y][i].x, y, energy: e }))
      );
    }
    if (!ok) break;
  }
  check("randomized multi-round: energies and coords consistent", ok);
}

// 9. Input validation.
throws("ragged map rejected", () => new SeamCarver([[1, 2], [3]]));
throws("non-integer energy rejected", () => new SeamCarver([[1.5]]));
throws("empty map rejected", () => new SeamCarver([]));
throws("out-of-bounds protected point rejected", () =>
  new SeamCarver([[1, 2]], { protectedPoints: [{ x: 5, y: 0 }] })
);
throws("negative count rejected", () => carveSeams([[1]], -1));

// 10. gradientEnergy helper sanity.
{
  const g = gradientEnergy([
    [0, 0, 10],
    [0, 0, 10],
  ]);
  check(
    "gradientEnergy: 2x3 known output",
    JSON.stringify(g) === JSON.stringify([[0, 10, 10], [0, 10, 10]])
  );
}

// 11. carve(0) is a no-op.
{
  const report = carveSeams([[1, 2], [3, 4]], 0);
  check("carve(0): feasible no-op", report.feasible && report.removed === 0);
}

console.log("\n" + passed + " passed, " + failed + " failed");
process.exit(failed === 0 ? 0 : 1);
