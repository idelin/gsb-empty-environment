import { colorDistance, type RGBA } from "./color.ts";
import type { Connectivity, Seed, ThresholdMode } from "./grower.ts";

export interface ReferenceOptions {
  width: number;
  height: number;
  pixels: ArrayLike<number>;
  connectivity: Connectivity;
  threshold: number;
  mode: ThresholdMode;
  barriers?: ArrayLike<number | boolean>;
}

export interface ReferenceResult {
  owner: (string | null)[];
  cost: number[];
}

/**
 * Brute-force reference: for every seed/pixel pair, enumerate ALL simple
 * paths (explicit-stack DFS, still no recursion) and take the minimum
 * bottleneck cost. Exponential -- only meant for tiny verification images.
 *
 * This is the ground truth that RegionGrower.compute() is checked against.
 */
export function referenceLabeling(
  options: ReferenceOptions,
  seeds: Seed[],
): ReferenceResult {
  const { width, height, connectivity, threshold, mode } = options;
  const n = width * height;
  const barrier = new Uint8Array(n);
  if (options.barriers) {
    for (let i = 0; i < n; i++) barrier[i] = options.barriers[i] ? 1 : 0;
  }
  const colors: RGBA[] = [];
  for (let i = 0; i < n; i++) {
    const o = i * 4;
    colors[i] = [
      options.pixels[o],
      options.pixels[o + 1],
      options.pixels[o + 2],
      options.pixels[o + 3],
    ];
  }
  const offsets: [number, number][] =
    connectivity === 4
      ? [[1, 0], [-1, 0], [0, 1], [0, -1]]
      : [[1, 0], [-1, 0], [0, 1], [0, -1], [1, 1], [1, -1], [-1, 1], [-1, -1]];

  // Same-pixel conflict resolution, identical to RegionGrower.
  const sorted = [...seeds].sort(
    (a, b) =>
      (a.priority ?? 0) - (b.priority ?? 0) ||
      (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
  const seen = new Set<number>();
  const active: { seed: Seed; rank: number; index: number; color: RGBA }[] = [];
  for (const s of sorted) {
    const idx = s.y * width + s.x;
    if (seen.has(idx)) continue;
    seen.add(idx);
    active.push({ seed: s, rank: active.length, index: idx, color: colors[idx] });
  }

  const best = new Float64Array(n).fill(Infinity);
  const bestRank = new Int32Array(n).fill(-1);
  const bestId: (string | null)[] = new Array(n).fill(null);

  for (const a of active) {
    // Min bottleneck cost from this seed to every pixel, by enumerating
    // every simple path. costTo[i] = min over paths of the path bottleneck.
    const costTo = new Float64Array(n).fill(Infinity);
    costTo[a.index] = 0;
    // Iterative DFS over simple paths; state = (pixel, costSoFar).
    const visited = new Uint8Array(n);
    const stack: { v: number; cost: number }[] = [{ v: a.index, cost: 0 }];
    visited[a.index] = 1;
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      const ux = top.v % width;
      const uy = (top.v / width) | 0;
      // Find next unvisited neighbor (re-scan each time; tiny images only).
      let advanced = false;
      for (const [dx, dy] of offsets) {
        const vx = ux + dx;
        const vy = uy + dy;
        if (vx < 0 || vy < 0 || vx >= width || vy >= height) continue;
        const v = vy * width + vx;
        if (barrier[v] || visited[v]) continue;
        const step =
          mode === "seed"
            ? colorDistance(colors[v], a.color)
            : colorDistance(colors[top.v], colors[v]);
        const cand = top.cost > step ? top.cost : step;
        if (cand >= costTo[v]) {
          // Cannot improve; but still must explore? No: extending a path
          // never lowers the bottleneck, so any continuation has cost >=
          // cand >= costTo[v] for v itself; continuations beyond v are
          // reached via other paths. Prune.
          continue;
        }
        costTo[v] = cand;
        visited[v] = 1;
        stack.push({ v, cost: cand });
        advanced = true;
        break;
      }
      if (!advanced) {
        stack.pop();
        if (stack.length > 0) visited[top.v] = 0;
      }
    }
    // Merge into global best with (cost, rank) ordering.
    for (let i = 0; i < n; i++) {
      const c = costTo[i];
      if (c < best[i] || (c === best[i] && (bestRank[i] === -1 || a.rank < bestRank[i]))) {
        best[i] = c;
        bestRank[i] = a.rank;
        bestId[i] = a.seed.id;
      }
    }
  }

  const owner: (string | null)[] = new Array(n).fill(null);
  const cost = new Array<number>(n).fill(Infinity);
  for (let i = 0; i < n; i++) {
    if (!barrier[i] && bestId[i] !== null && best[i] <= threshold) {
      owner[i] = bestId[i];
      cost[i] = best[i];
    }
  }
  return { owner, cost };
}
