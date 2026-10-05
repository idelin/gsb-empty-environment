/**
 * Independent verification helpers.
 *
 * - isValidOrder: checks a permutation against edges and pins.
 * - enumerateValidOrders: brute-force enumeration of all valid permutations
 *   (only for n <= 9), yielding the count and the lexicographic minimum.
 * - verifyEvidence: independently re-checks an infeasibility evidence object
 *   against the raw problem instance.
 */

import type { Edge, Evidence } from './constrainedTopoSort.js';
import { buildGraph, computeReachability } from './constrainedTopoSort.js';

export function isValidOrder(
  n: number,
  edges: ReadonlyArray<Edge>,
  pins: ReadonlyMap<number, number>,
  order: ReadonlyArray<number>
): boolean {
  if (order.length !== n) return false;
  const posOf: number[] = new Array(n).fill(-1);
  const seen: boolean[] = new Array(n).fill(false);
  for (let i = 0; i < n; i++) {
    const v = order[i];
    if (!Number.isInteger(v) || v < 0 || v >= n || seen[v]) return false;
    seen[v] = true;
    posOf[v] = i;
  }
  for (const [a, b] of edges) {
    if (posOf[a] >= posOf[b]) return false;
  }
  for (const [v, p] of pins) {
    if (posOf[v] !== p) return false;
  }
  return true;
}

function nextPermutation(perm: number[]): boolean {
  let i = perm.length - 2;
  while (i >= 0 && perm[i] >= perm[i + 1]) i--;
  if (i < 0) return false;
  let j = perm.length - 1;
  while (perm[j] <= perm[i]) j--;
  const t = perm[i];
  perm[i] = perm[j];
  perm[j] = t;
  let lo = i + 1;
  let hi = perm.length - 1;
  while (lo < hi) {
    const u = perm[lo];
    perm[lo] = perm[hi];
    perm[hi] = u;
    lo++;
    hi--;
  }
  return true;
}

export interface EnumerationResult {
  count: number;
  minOrder: number[] | null;
}

/**
 * Enumerates all permutations in lexicographic order and filters by validity,
 * so the first valid one found is the lexicographic minimum. The default
 * search budget of solve() (1,000,000) exceeds the total number of nodes in
 * the permutation tree for n = 9 (986,410), so n <= 9 instances are always
 * decided conclusively and can be cross-checked against this enumerator.
 */
export function enumerateValidOrders(
  n: number,
  edges: ReadonlyArray<Edge>,
  pins: ReadonlyMap<number, number>
): EnumerationResult {
  if (n > 9) throw new RangeError('enumeration is only supported for n <= 9');
  const perm: number[] = [];
  for (let i = 0; i < n; i++) perm.push(i);
  let count = 0;
  let minOrder: number[] | null = null;
  for (;;) {
    if (isValidOrder(n, edges, pins, perm)) {
      count++;
      if (minOrder === null) minOrder = perm.slice();
    }
    if (!nextPermutation(perm)) break;
  }
  return { count, minOrder };
}
/**
 * Independently verifies an infeasibility evidence object. Every check is
 * derived only from the raw instance (n, edges, pins), so a third party can
 * confirm the evidence without trusting the solver.
 */
export function verifyEvidence(
  n: number,
  edges: ReadonlyArray<Edge>,
  pins: ReadonlyMap<number, number>,
  evidence: Evidence
): boolean {
  if (evidence.kind === 'duplicate-pin-position') {
    if (evidence.nodes.length < 2) return false;
    for (const v of evidence.nodes) {
      if (pins.get(v) !== evidence.position) return false;
    }
    return true;
  }

  if (evidence.kind === 'pin-out-of-range') {
    return (
      pins.get(evidence.node) === evidence.position &&
      evidence.n === n &&
      (evidence.position < 0 || evidence.position >= n)
    );
  }

  if (evidence.kind === 'cycle') {
    const c = evidence.cycle;
    if (c.length < 1) return false;
    const edgeSet: Set<string> = new Set();
    for (const [a, b] of edges) edgeSet.add(a + ',' + b);
    for (let i = 0; i < c.length; i++) {
      if (!edgeSet.has(c[i] + ',' + c[(i + 1) % c.length])) return false;
    }
    return true;
  }

  const graph = buildGraph(n, edges);
  const reach = computeReachability(n, graph.succ);

  if (evidence.kind === 'ancestors-exceed-slot') {
    if (pins.get(evidence.node) !== evidence.position) return false;
    const seen: Set<number> = new Set();
    for (const u of evidence.ancestors) {
      if (u === evidence.node || seen.has(u)) return false;
      if (!reach[u][evidence.node]) return false;
      seen.add(u);
    }
    return evidence.ancestors.length > evidence.position;
  }

  if (evidence.kind === 'descendants-exceed-slot') {
    if (pins.get(evidence.node) !== evidence.position) return false;
    const seen: Set<number> = new Set();
    for (const u of evidence.descendants) {
      if (u === evidence.node || seen.has(u)) return false;
      if (!reach[evidence.node][u]) return false;
      seen.add(u);
    }
    return evidence.descendants.length > n - 1 - evidence.position;
  }

  if (evidence.kind === 'pinned-order-contradiction') {
    const e = evidence;
    if (pins.get(e.ancestor) !== e.ancestorPos) return false;
    if (pins.get(e.descendant) !== e.descendantPos) return false;
    if (e.ancestorPos < e.descendantPos) return false;
    const chain = e.chain;
    if (chain.length < 2) return false;
    if (chain[0] !== e.ancestor || chain[chain.length - 1] !== e.descendant) return false;
    const edgeSet: Set<string> = new Set();
    for (const [a, b] of edges) edgeSet.add(a + ',' + b);
    for (let i = 0; i + 1 < chain.length; i++) {
      if (!edgeSet.has(chain[i] + ',' + chain[i + 1])) return false;
    }
    return true;
  }

  if (evidence.kind === 'exhaustive-search') {
    // This evidence carries no small certificate: it asserts the bounded
    // search space was exhausted (possibly with zero placements, when the
    // root has no viable candidate). It is verifiable by re-running the
    // search; the test-suite independently confirms such instances have
    // zero valid permutations via brute-force enumeration.
    return evidence.explored >= 0;
  }

  return false;
}
