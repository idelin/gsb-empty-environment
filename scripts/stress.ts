import { solveConstrainedTopo } from "../src/solver.ts";
import { verifyEvidence, type Edge } from "../src/evidence.ts";
import { lexMinValidPermutation } from "../src/bruteForce.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

let mismatches = 0, fallback = 0, solved = 0, infeasible = 0;
const t0 = Date.now();
// 多个种子 + n=9 重点
for (const seed of [1, 2, 3, 424242, 987654321]) {
  const rand = mulberry32(seed);
  const ITER = seed === 1 ? 8000 : 4000;
  for (let iter = 0; iter < ITER; iter++) {
    const n = 1 + Math.floor(rand() * 9); // 1..9
    const edgeProb = [0.05, 0.15, 0.3, 0.5, 0.7][Math.floor(rand() * 5)];
    const edges: Edge[] = [];
    for (let u = 0; u < n; u++)
      for (let v = 0; v < n; v++)
        if (u !== v && rand() < edgeProb) edges.push([u, v]);
    const fixed = new Map<number, number>();
    const fixProb = [0, 0.2, 0.4, 0.6, 0.9][Math.floor(rand() * 5)];
    for (let v = 0; v < n; v++) if (rand() < fixProb) fixed.set(v, Math.floor(rand() * n));
    const result = solveConstrainedTopo(n, edges, fixed, { budget: Infinity });
    const oracle = lexMinValidPermutation(n, edges, fixed);
    if (result.status === "infeasible") {
      infeasible++;
      if (result.evidence.kind === "exhaustive-search") fallback++;
      if (!verifyEvidence(n, edges, fixed, result.evidence)) { mismatches++; console.error("bad evidence", n, edges, fixed, result.evidence); }
      if (oracle !== null) { mismatches++; console.error("false infeasible", n, edges, fixed, oracle); }
    } else if (result.status === "solved") {
      solved++;
      if (oracle === null || JSON.stringify(result.permutation) !== JSON.stringify(oracle)) {
        mismatches++; console.error("lex-min mismatch", n, edges, fixed, result.permutation, oracle);
      }
    } else { mismatches++; console.error("unknown with inf budget", n, edges, fixed); }
  }
}
console.log("done in", Date.now() - t0, "ms");
console.log({ solved, infeasible, fallback, mismatches });
