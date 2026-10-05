export const MAX_EDGES = 25;

export class InputError extends Error {}

export interface ObservationInput {
  id: string;
  path: string[];
  result: "ok" | "fail";
}

export interface ProblemInput {
  topology: { edges: [string, string][] };
  observations: ObservationInput[];
}

export interface OkResult {
  status: "ok";
  minFaultEdges: number;
  solutionCount: number;
  solutions: string[][];
  edges: Record<string, [string, string]>;
}

export interface ContradictoryResult {
  status: "contradictory";
  message: string;
  minUnsatisfiableCoreSize: number;
  coreCount: number;
  cores: string[][];
}

export type SolveResult = OkResult | ContradictoryResult;

interface ValidObservation extends ObservationInput {
  mask: number;
}

interface Validated {
  edges: [string, string][];
  observations: ValidObservation[];
}

function edgeKey(u: string, v: string): string {
  return u < v ? `${u} ${v}` : `${v} ${u}`;
}

function popcount(x: number): number {
  x = x - ((x >> 1) & 0x55555555);
  x = (x & 0x33333333) + ((x >> 2) & 0x33333333);
  return (((x + (x >> 4)) & 0x0f0f0f0f) * 0x01010101) >> 24;
}

function fail(message: string): never {
  throw new InputError(message);
}

function validate(input: unknown): Validated {
  if (typeof input !== "object" || input === null || Array.isArray(input)) {
    fail("input must be a JSON object");
  }
  const obj = input as Record<string, unknown>;

  const topo = obj.topology;
  if (typeof topo !== "object" || topo === null || Array.isArray(topo)) {
    fail('"topology" must be an object with an "edges" array');
  }
  const rawEdges = (topo as Record<string, unknown>).edges;
  if (!Array.isArray(rawEdges)) fail('"topology.edges" must be an array');
  if (rawEdges.length > MAX_EDGES) {
    fail(`topology has ${rawEdges.length} edges; maximum supported is ${MAX_EDGES}`);
  }

  const edges: [string, string][] = [];
  const seenEdges = new Set<string>();
  for (const e of rawEdges) {
    if (!Array.isArray(e) || e.length !== 2 || typeof e[0] !== "string" || typeof e[1] !== "string") {
      fail("each edge must be a [node, node] pair of strings");
    }
    const [u, v] = e as [string, string];
    if (u.length === 0 || v.length === 0) fail("edge endpoints must be non-empty strings");
    if (u === v) fail(`self-loop edge ["${u}", "${v}"] is not allowed`);
    const key = edgeKey(u, v);
    if (seenEdges.has(key)) fail(`duplicate edge ["${u}", "${v}"]`);
    seenEdges.add(key);
    edges.push([u, v]);
  }

  const rawObs = obj.observations;
  if (!Array.isArray(rawObs)) fail('"observations" must be an array');

  const edgeIndex = new Map<string, number>();
  edges.forEach(([u, v], i) => edgeIndex.set(edgeKey(u, v), i));

  const seenIds = new Set<string>();
  const observations: ValidObservation[] = [];
  for (const o of rawObs) {
    if (typeof o !== "object" || o === null || Array.isArray(o)) {
      fail("each observation must be an object");
    }
    const rec = o as Record<string, unknown>;
    if (typeof rec.id !== "string" || rec.id.length === 0) {
      fail('each observation needs a non-empty string "id"');
    }
    if (seenIds.has(rec.id)) fail(`duplicate observation id "${rec.id}"`);
    seenIds.add(rec.id);
    if (rec.result !== "ok" && rec.result !== "fail") {
      fail(`observation "${rec.id}": "result" must be "ok" or "fail"`);
    }
    if (!Array.isArray(rec.path) || rec.path.length === 0) {
      fail(`observation "${rec.id}": "path" must be a non-empty array of node names`);
    }
    const path = rec.path as unknown[];
    let mask = 0;
    for (let i = 0; i < path.length; i++) {
      if (typeof path[i] !== "string" || (path[i] as string).length === 0) {
        fail(`observation "${rec.id}": path nodes must be non-empty strings`);
      }
      if (i > 0) {
        const idx = edgeIndex.get(edgeKey(path[i - 1] as string, path[i] as string));
        if (idx === undefined) {
          fail(
            `observation "${rec.id}": no edge between "${path[i - 1]}" and "${path[i]}" in topology`,
          );
        }
        mask |= 1 << idx;
      }
    }
    observations.push({ id: rec.id, path: path as string[], result: rec.result, mask });
  }

  return { edges, observations };
}

/**
 * Remove duplicate masks and masks that are supersets of another mask
 * (hitting the smaller set already satisfies the larger one).
 */
function reduceSets(sets: number[]): number[] {
  const unique = [...new Set(sets)].sort((a, b) => popcount(a) - popcount(b));
  const kept: number[] = [];
  for (const m of unique) {
    if (!kept.some((s) => (s & m) === s)) kept.push(m);
  }
  return kept;
}

function greedyUpperBound(sets: number[], numEdges: number): number {
  let covered = 0;
  let count = 0;
  for (;;) {
    let bestBit = 0;
    let bestHits = 0;
    for (let e = 0; e < numEdges; e++) {
      const bit = 1 << e;
      if (covered & bit) continue;
      let hits = 0;
      for (const s of sets) {
        if ((s & covered) === 0 && s & bit) hits++;
      }
      if (hits > bestHits) {
        bestHits = hits;
        bestBit = bit;
      }
    }
    if (bestHits === 0) return count;
    covered |= bestBit;
    count++;
  }
}

/**
 * Enumerate all minimum-cardinality hitting sets for the given family of
 * non-empty edge masks. Returns the minimum size and the solution masks.
 */
export function enumerateMinHittingSets(
  rawSets: number[],
  numEdges: number,
): { size: number; solutions: number[] } {
  const sets = reduceSets(rawSets).sort((a, b) => popcount(a) - popcount(b));
  let best = greedyUpperBound(sets, numEdges);
  const solutions: number[] = [];
  const UNSEEN = 255;
  const seen = new Uint8Array(1 << numEdges).fill(UNSEEN);

  function dfs(covered: number, depth: number): void {
    if (depth > best) return;
    if (seen[covered] <= depth) return;
    seen[covered] = depth;

    let target = 0;
    for (const s of sets) {
      if ((s & covered) === 0) {
        target = s;
        break;
      }
    }
    if (target === 0) {
      if (depth < best) {
        best = depth;
        solutions.length = 0;
      }
      solutions.push(covered);
      return;
    }
    if (depth === best) return;

    // Branch on edges of the smallest unhit set, most-covering first.
    const branches: { bit: number; hits: number }[] = [];
    let t = target;
    while (t !== 0) {
      const bit = t & -t;
      t ^= bit;
      let hits = 0;
      for (const s of sets) {
        if ((s & covered) === 0 && s & bit) hits++;
      }
      branches.push({ bit, hits });
    }
    branches.sort((a, b) => b.hits - a.hits);
    for (const { bit } of branches) dfs(covered | bit, depth + 1);
  }

  dfs(0, 0);
  solutions.sort((a, b) => a - b);
  return { size: best, solutions };
}

/**
 * Enumerate all minimum-cardinality subsets of `sets` (indices into it)
 * whose union covers `target`, considering only subsets of size <= limit.
 */
function enumerateMinCovers(
  target: number,
  sets: number[],
  limit: number,
): number[][] {
  const relevant = sets
    .map((mask, idx) => ({ mask: mask & target, idx }))
    .filter((s) => s.mask !== 0)
    .sort((a, b) => popcount(b.mask) - popcount(a.mask));

  const suffixUnion = new Array<number>(relevant.length + 1).fill(0);
  for (let i = relevant.length - 1; i >= 0; i--) {
    suffixUnion[i] = suffixUnion[i + 1] | relevant[i].mask;
  }

  let best = Number.POSITIVE_INFINITY;
  const out: number[][] = [];
  const chosen: number[] = [];

  function dfs(start: number, needed: number): void {
    if (needed === 0) {
      if (chosen.length < best) {
        best = chosen.length;
        out.length = 0;
      }
      if (chosen.length === best) out.push([...chosen]);
      return;
    }
    if (chosen.length >= best || chosen.length >= limit) return;
    if (start >= relevant.length) return;
    if ((suffixUnion[start] & needed) !== needed) return;
    for (let j = start; j < relevant.length; j++) {
      chosen.push(relevant[j].idx);
      dfs(j + 1, needed & ~relevant[j].mask);
      chosen.pop();
    }
  }

  dfs(0, target);
  return out;
}

export function solve(input: unknown): SolveResult {
  const { edges, observations } = validate(input);
  const numEdges = edges.length;
  const fullMask = numEdges === 0 ? 0 : (1 << numEdges) - 1;

  const okObs = observations.filter((o) => o.result === "ok");
  const failObs = observations.filter((o) => o.result === "fail");

  let healthyMask = 0;
  for (const o of okObs) healthyMask |= o.mask;
  const candidateMask = fullMask & ~healthyMask;

  const edgeIds = edges.map((_, i) => `e${i}`);
  const edgeLegend: Record<string, [string, string]> = {};
  edges.forEach(([u, v], i) => {
    edgeLegend[`e${i}`] = [u, v];
  });

  const violated = failObs.filter((o) => (o.mask & candidateMask) === 0);

  if (violated.length > 0) {
    // Contradictory input: enumerate all minimum-cardinality unsatisfiable
    // observation subsets. A core is one failed observation plus a minimum
    // set of "ok" observations whose paths jointly cover its whole path.
    const okMasks = okObs.map((o) => o.mask);
    let bestSize = Number.POSITIVE_INFINITY;
    const cores: string[][] = [];

    for (const f of violated) {
      if (f.mask === 0) {
        // A failed observation over an empty path is unsatisfiable alone.
        if (1 < bestSize) {
          bestSize = 1;
          cores.length = 0;
        }
        cores.push([f.id]);
        continue;
      }
      if (bestSize === 1) continue;
      const covers = enumerateMinCovers(f.mask, okMasks, bestSize - 1);
      if (covers.length === 0) continue;
      const coreSize = covers[0].length + 1;
      if (coreSize < bestSize) {
        bestSize = coreSize;
        cores.length = 0;
      }
      if (coreSize === bestSize) {
        for (const cover of covers) {
          cores.push([f.id, ...cover.map((i) => okObs[i].id)]);
        }
      }
    }

    const normalized = cores
      .map((core) => [...core].sort())
      .sort((a, b) => a.join("").localeCompare(b.join("")));
    return {
      status: "contradictory",
      message:
        "observations are mutually contradictory: some failed paths are fully covered by successful observations",
      minUnsatisfiableCoreSize: bestSize,
      coreCount: normalized.length,
      cores: normalized,
    };
  }

  const sets = failObs.map((o) => o.mask & candidateMask);
  const { size, solutions } = enumerateMinHittingSets(sets, numEdges);

  const toEdgeList = (mask: number): string[] => {
    const list: string[] = [];
    for (let i = 0; i < numEdges; i++) {
      if (mask & (1 << i)) list.push(edgeIds[i]);
    }
    return list;
  };

  return {
    status: "ok",
    minFaultEdges: size,
    solutionCount: solutions.length,
    solutions: solutions.map(toEdgeList),
    edges: edgeLegend,
  };
}
