// Exhaustive search over single-ellipsis placements: keep a prefix of
// `a` clusters and a suffix of `b` clusters, eliding the middle. For
// small layouts this enumerates every candidate and picks the best
// score, so the engine's output can be checked against the optimal
// retention objective directly.

export interface Placement { a: number; b: number }

export function compareScore(s1: number[], s2: number[]): number {
  const n = Math.max(s1.length, s2.length);
  for (let i = 0; i < n; i++) {
    const d = (s1[i] ?? 0) - (s2[i] ?? 0);
    if (d !== 0) return d;
  }
  return 0;
}

export function exhaustiveBest(
  n: number,
  cutLegal: (pos: number) => boolean,
  feasible: (a: number, b: number) => boolean,
  scoreOf: (a: number, b: number) => number[],
): (Placement & { score: number[] }) | null {
  let best: (Placement & { score: number[] }) | null = null;
  for (let a = 0; a <= n; a++) {
    if (!cutLegal(a)) continue;
    for (let b = 0; b <= n - a; b++) {
      if (!cutLegal(n - b)) continue;
      if (!feasible(a, b)) continue;
      const score = scoreOf(a, b);
      if (!best || compareScore(score, best.score) > 0) best = { a, b, score };
    }
  }
  return best;
}
