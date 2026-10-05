/**
 * Test suite for the constrained topological sorter.
 *
 * Covers the empty graph, cycles, pins squeezed out by dependency chains,
 * budget-driven 'unknown' results, and a randomized cross-check of the
 * solver against brute-force permutation enumeration for n <= 9.
 */

import { solve, findCycle, buildGraph } from '../src/constrainedTopoSort.js';
import type { Edge } from '../src/constrainedTopoSort.js';
import { enumerateValidOrders, isValidOrder, verifyEvidence } from '../src/enumerate.js';

let passed = 0;
let failed = 0;

function check(name: string, cond: boolean, detail: string = ''): void {
  if (cond) {
    passed++;
    console.log('ok   - ' + name);
  } else {
    failed++;
    console.error('FAIL - ' + name + (detail ? ' :: ' + detail : ''));
  }
}

function eq(a: ReadonlyArray<number>, b: ReadonlyArray<number>): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

function mulberry32(seed: number) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---- 1. empty graph, no pins: lexicographic minimum is the identity ----
{
  const r = solve(6, [], new Map());
  check('empty graph: identity order', r.status === 'ok' && eq(r.order, [0, 1, 2, 3, 4, 5]));
}

// ---- 2. empty graph with pins ----
{
  const pins = new Map([
    [3, 0],
    [1, 2],
  ]);
  const r = solve(5, [], pins);
  check('empty graph with pins', r.status === 'ok' && eq(r.order, [3, 0, 1, 2, 4]));
  const en = enumerateValidOrders(5, [], pins);
  check(
    'empty graph with pins: matches enumeration',
    en.minOrder !== null && eq(en.minOrder, [3, 0, 1, 2, 4])
  );
}

// ---- 3. cycle: infeasible with a verifiable cycle witness ----
{
  const edges: Edge[] = [
    [0, 1],
    [1, 2],
    [2, 0],
  ];
  const r = solve(3, edges, new Map());
  check(
    'cycle: infeasible with cycle evidence',
    r.status === 'infeasible' &&
      r.evidence.kind === 'cycle' &&
      verifyEvidence(3, edges, new Map(), r.evidence)
  );
}
// ---- 4. dependency chain squeezes the pinned position ----
// A plain topological sort succeeds here, yet no positional solution exists:
// the solver must not confuse the two.
{
  const edges: Edge[] = [
    [0, 1],
    [1, 2],
    [2, 3],
  ];
  const pins = new Map([[3, 2]]);
  check(
    'chain squeeze: plain topo sort would succeed (acyclic)',
    findCycle(4, buildGraph(4, edges).succ) === null
  );
  const r = solve(4, edges, pins);
  check(
    'chain squeeze: infeasible with ancestor evidence',
    r.status === 'infeasible' &&
      r.evidence.kind === 'ancestors-exceed-slot' &&
      verifyEvidence(4, edges, pins, r.evidence)
  );
  check(
    'chain squeeze: enumeration confirms zero valid orders',
    enumerateValidOrders(4, edges, pins).count === 0
  );
}

// ---- 5. two pinned nodes contradict the precedence order ----
{
  const edges: Edge[] = [[0, 1]];
  const pins = new Map([
    [0, 2],
    [1, 1],
  ]);
  const r = solve(4, edges, pins);
  check(
    'pinned order contradiction',
    r.status === 'infeasible' &&
      r.evidence.kind === 'pinned-order-contradiction' &&
      verifyEvidence(4, edges, pins, r.evidence)
  );
}

// ---- 6. two nodes pinned to the same position ----
{
  const pins = new Map([
    [0, 1],
    [2, 1],
  ]);
  const r = solve(4, [], pins);
  check(
    'duplicate pin position',
    r.status === 'infeasible' &&
      r.evidence.kind === 'duplicate-pin-position' &&
      verifyEvidence(4, [], pins, r.evidence)
  );
}

// ---- 7. budget exhaustion yields 'unknown', never a wrong answer ----
{
  const r = solve(9, [], new Map(), 3);
  check(
    'tiny budget returns unknown',
    r.status === 'unknown' && r.reason === 'budget-exhausted' && r.stats.explored > 3
  );
  const r2 = solve(9, [], new Map());
  check(
    'default budget solves n=9 empty graph',
    r2.status === 'ok' && eq(r2.order, [0, 1, 2, 3, 4, 5, 6, 7, 8])
  );
}

// ---- 8. lexicographically smallest order with edges and pins ----
{
  const edges: Edge[] = [[3, 0]];
  const pins = new Map([[1, 1]]);
  const r = solve(4, edges, pins);
  check('lex-min with edge and pin', r.status === 'ok' && eq(r.order, [2, 1, 3, 0]));
  const en = enumerateValidOrders(4, edges, pins);
  check(
    'lex-min matches enumeration',
    en.minOrder !== null && eq(en.minOrder, [2, 1, 3, 0])
  );
}
// ---- 9. search-level infeasibility: two chains compete for early slots ----
// Chain 0->1->2 needs positions 0,1 before pin 2@2; chain 3->4 needs
// position 0 before pin 4@1. No single structural check fires; only the
// exhaustive search can prove infeasibility.
{
  const edges: Edge[] = [
    [0, 1],
    [1, 2],
    [3, 4],
  ];
  const pins = new Map([
    [2, 2],
    [4, 1],
  ]);
  const r = solve(5, edges, pins);
  check(
    'competing chains: infeasible via exhaustive search',
    r.status === 'infeasible' &&
      r.evidence.kind === 'exhaustive-search' &&
      verifyEvidence(5, edges, pins, r.evidence)
  );
  check(
    'competing chains: enumeration confirms zero valid orders',
    enumerateValidOrders(5, edges, pins).count === 0
  );
}

// ---- 10. n=9 spot cross-check at the enumeration bound ----
{
  const edges: Edge[] = [
    [0, 3],
    [3, 4],
    [1, 6],
  ];
  const pins = new Map([
    [7, 0],
    [2, 8],
    [5, 4],
  ]);
  const r = solve(9, edges, pins);
  const en = enumerateValidOrders(9, edges, pins);
  check(
    'n=9 cross-check: order equals enumeration minimum',
    r.status === 'ok' &&
      en.minOrder !== null &&
      eq(r.order, en.minOrder) &&
      isValidOrder(9, edges, pins, r.order)
  );
}

// ---- 11. randomized cross-check against enumeration (n <= 8) ----
{
  let mismatches = 0;
  let firstDetail = '';
  let okCount = 0;
  let infeasibleCount = 0;
  for (let seed = 1; seed <= 200; seed++) {
    const rand = mulberry32(seed);
    const n = 1 + Math.floor(rand() * 8);
    const base: number[] = [];
    for (let i = 0; i < n; i++) base.push(i);
    for (let i = n - 1; i > 0; i--) {
      const j = Math.floor(rand() * (i + 1));
      const t = base[i];
      base[i] = base[j];
      base[j] = t;
    }
    const edges: Edge[] = [];
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        if (rand() < 0.25) edges.push([base[i], base[j]]);
      }
    }
    const pins: Map<number, number> = new Map();
    for (let v = 0; v < n; v++) {
      if (rand() < 0.35) pins.set(v, Math.floor(rand() * n));
    }
    const r = solve(n, edges, pins);
    const en = enumerateValidOrders(n, edges, pins);
    let problem = '';
    if (r.status === 'ok') {
      okCount++;
      if (r.stats.explored > r.stats.budget) problem = 'budget exceeded';
      else if (!isValidOrder(n, edges, pins, r.order)) problem = 'order is invalid';
      else if (en.minOrder === null || !eq(r.order, en.minOrder)) {
        problem = 'order is not the enumeration minimum';
      }
    } else if (r.status === 'infeasible') {
      infeasibleCount++;
      if (en.count !== 0) problem = 'enumeration found ' + en.count + ' valid orders';
      else if (!verifyEvidence(n, edges, pins, r.evidence)) problem = 'evidence does not verify';
    } else {
      problem = 'unexpected unknown with default budget';
    }
    if (problem) {
      mismatches++;
      if (!firstDetail) {
        firstDetail = 'seed=' + seed + ' n=' + n + ': ' + problem;
      }
    }
  }
  check(
    'randomized cross-check (200 seeds, ok=' + okCount + ', infeasible=' + infeasibleCount + ')',
    mismatches === 0,
    firstDetail
  );
}

// ---- 12. boundary cases ----
{
  const r0 = solve(0, [], new Map());
  check('n=0: empty order', r0.status === 'ok' && r0.order.length === 0);

  const r1 = solve(1, [], new Map([[0, 0]]));
  check('n=1 with pin', r1.status === 'ok' && eq(r1.order, [0]));

  const r2 = solve(3, [], new Map([[0, 5]]));
  check(
    'pin out of range',
    r2.status === 'infeasible' &&
      r2.evidence.kind === 'pin-out-of-range' &&
      verifyEvidence(3, [], new Map([[0, 5]]), r2.evidence)
  );

  const selfEdges: Edge[] = [[2, 2]];
  const r3 = solve(3, selfEdges, new Map());
  check(
    'self-loop is a cycle',
    r3.status === 'infeasible' &&
      r3.evidence.kind === 'cycle' &&
      verifyEvidence(3, selfEdges, new Map(), r3.evidence)
  );

  const fanEdges: Edge[] = [
    [0, 1],
    [0, 2],
    [0, 3],
  ];
  const fanPins = new Map([[0, 1]]);
  const r4 = solve(4, fanEdges, fanPins);
  check(
    'descendants exceed slot',
    r4.status === 'infeasible' &&
      r4.evidence.kind === 'descendants-exceed-slot' &&
      verifyEvidence(4, fanEdges, fanPins, r4.evidence)
  );
}

console.log('---');
console.log('passed: ' + passed + ', failed: ' + failed);
process.exitCode = failed === 0 ? 0 : 1;
