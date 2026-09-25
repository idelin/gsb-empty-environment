/**
 * Limit solver. Limits and their dependency relations are compiled to
 * difference constraints `x_a - x_b <= c` and solved with Bellman-Ford:
 *
 *  - feasibility: no negative cycle (otherwise the obligations conflict);
 *  - tightest upper bound of v = shortest path ZERO -> v;
 *  - tightest lower bound of v = -(shortest path v -> ZERO).
 *
 * Every propagated bound keeps a provenance chain of the obligations whose
 * constraints derived it, so the merged plan can cite its sources.
 */

import type { LimitObligation, LimitRelation } from './obligations.ts';

export const ZERO = '@zero';

export type LimitConstraint = {
  /** constraint: value(a) - value(b) <= c */
  a: string;
  b: string;
  c: number;
  /** obligation id that contributed this constraint */
  source: string;
  label: string;
};

export function relationConstraints(r: LimitRelation, source: string): LimitConstraint[] {
  const off = r.offset ?? 0;
  const fmt = (k: number) => (k === 0 ? '' : k > 0 ? ` + ${k}` : ` - ${-k}`);
  switch (r.rel) {
    case '<=':
      return [{ a: r.left, b: r.right, c: off, source, label: `${r.left} <= ${r.right}${fmt(off)}` }];
    case '>=':
      return [{ a: r.right, b: r.left, c: -off, source, label: `${r.left} >= ${r.right}${fmt(off)}` }];
    case '==':
      return [
        { a: r.left, b: r.right, c: off, source, label: `${r.left} == ${r.right}${fmt(off)}` },
        { a: r.right, b: r.left, c: -off, source, label: `${r.left} == ${r.right}${fmt(off)}` },
      ];
  }
}

export function constraintsOf(ob: LimitObligation): LimitConstraint[] {
  const out: LimitConstraint[] = [];
  if (ob.bound === 'upper') {
    out.push({ a: ob.name, b: ZERO, c: ob.value, source: ob.id, label: `${ob.name} <= ${ob.value}` });
  } else {
    out.push({ a: ZERO, b: ob.name, c: -ob.value, source: ob.id, label: `${ob.name} >= ${ob.value}` });
  }
  for (const r of ob.requires ?? []) out.push(...relationConstraints(r, ob.id));
  return out;
}

export type BoundEvidence = { source: string; label: string };

export type Bound = { value: number; evidence: BoundEvidence[] };

export type LimitSolution =
  | { ok: true; upper: ReadonlyMap<string, Bound>; lower: ReadonlyMap<string, Bound> }
  | { ok: false };

type Edge = { from: string; to: string; w: number; c: LimitConstraint };

type BellmanFordResult = { dist: Map<string, number>; pred: Map<string, Edge> } | null;

function bellmanFord(nodes: readonly string[], edges: readonly Edge[], source: string | null): BellmanFordResult {
  const dist = new Map<string, number>();
  const pred = new Map<string, Edge>();
  for (const n of nodes) dist.set(n, source === null ? 0 : n === source ? 0 : Infinity);
  for (let i = 0; i < nodes.length; i++) {
    let relaxed = false;
    for (const e of edges) {
      const d = dist.get(e.from)!;
      if (d === Infinity) continue;
      if (dist.get(e.to)! > d + e.w) {
        if (i === nodes.length - 1) return null; // negative cycle => infeasible
        dist.set(e.to, d + e.w);
        pred.set(e.to, e);
        relaxed = true;
      }
    }
    if (!relaxed) break;
  }
  return { dist, pred };
}

function evidenceChain(pred: ReadonlyMap<string, Edge>, node: string, maxSteps: number): BoundEvidence[] {
  const chain: BoundEvidence[] = [];
  const seen = new Set<string>();
  let cur = node;
  for (let i = 0; i < maxSteps; i++) {
    const e = pred.get(cur);
    if (!e) break;
    chain.push({ source: e.c.source, label: e.c.label });
    if (seen.has(e.from)) break;
    seen.add(e.from);
    cur = e.from;
  }
  return chain;
}

export function solveConstraints(constraints: readonly LimitConstraint[]): LimitSolution {
  const nodeSet = new Set<string>([ZERO]);
  for (const c of constraints) {
    nodeSet.add(c.a);
    nodeSet.add(c.b);
  }
  const nodes = [...nodeSet];
  const edges: Edge[] = constraints.map((c) => ({ from: c.b, to: c.a, w: c.c, c }));
  // Feasibility: super-source initialization (all distances 0) detects any negative cycle.
  if (bellmanFord(nodes, edges, null) === null) return { ok: false };
  // Tightest upper bounds: shortest paths from ZERO in the constraint graph.
  const up = bellmanFord(nodes, edges, ZERO)!;
  // Tightest lower bounds: shortest paths from ZERO in the reversed graph.
  const revEdges: Edge[] = constraints.map((c) => ({ from: c.a, to: c.b, w: c.c, c }));
  const dn = bellmanFord(nodes, revEdges, ZERO)!;

  const upper = new Map<string, Bound>();
  const lower = new Map<string, Bound>();
  for (const n of nodes) {
    if (n === ZERO) continue;
    const u = up.dist.get(n)!;
    if (u !== Infinity) {
      upper.set(n, { value: u, evidence: evidenceChain(up.pred, n, nodes.length) });
    }
    const l = dn.dist.get(n)!;
    if (l !== Infinity) {
      lower.set(n, { value: -l, evidence: evidenceChain(dn.pred, n, nodes.length) });
    }
  }
  return { ok: true, upper, lower };
}

export function solveLimitObligations(obs: readonly LimitObligation[]): LimitSolution {
  return solveConstraints(obs.flatMap(constraintsOf));
}
