import { test } from 'node:test';
import assert from 'node:assert/strict';
import { and, gt, oneOf, type Policy } from '../src/policy.ts';
import { plan } from '../src/planner.ts';
import { AttributeScheduler, type AttrSource, type FetchResult } from '../src/scheduler.ts';
import { MemoryEvidenceStore } from '../src/evidence.ts';

interface Scenario {
  name: string;
  attrs: string[];
  costs: Record<string, number>;
  domains: Record<string, number[]>;
  conds: Record<string, (v: number) => boolean>;
  policy: Policy;
}

function permutations<T>(items: T[]): T[][] {
  if (items.length <= 1) return [items];
  return items.flatMap((x, i) => permutations(items.filter((_, j) => j !== i)).map((p) => [x, ...p]));
}

function assignments(domains: Record<string, number[]>, attrs: string[]): Record<string, number>[] {
  return attrs.reduce<Record<string, number>[]>(
    (acc, attr) => acc.flatMap((a) => domains[attr].map((v) => ({ ...a, [attr]: v }))),
    [{}],
  );
}

/** Exact total fetch cost of a fixed evaluation order over all assignments (and-semantics). */
function totalCostForOrder(order: string[], sc: Scenario): number {
  let total = 0;
  for (const a of assignments(sc.domains, sc.attrs)) {
    for (const attr of order) {
      total += sc.costs[attr];
      if (!sc.conds[attr](a[attr])) break; // short-circuit: deny decided
    }
  }
  return total;
}

async function totalCostViaScheduler(order: string[], sc: Scenario): Promise<number> {
  const calls: { subject: string; attrs: string[] }[] = [];
  const data = new Map<string, Record<string, number>>();
  for (const [i, a] of assignments(sc.domains, sc.attrs).entries()) data.set(`s${i}`, a);
  const source: AttrSource = {
    async fetch(subject, attrs): Promise<FetchResult> {
      calls.push({ subject, attrs: [...attrs] });
      return { version: 1, values: Object.fromEntries(attrs.map((x) => [x, data.get(subject)![x]])) };
    },
  };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), { costs: sc.costs });
  for (const subject of data.keys()) await scheduler.evaluate(sc.policy, subject, 'bench');
  // sanity: scheduler must fetch in the heuristic order
  const heuristic = plan(sc.policy, sc.costs).map((s) => s.attr);
  assert.deepEqual(heuristic, order, 'test order must match the planner heuristic order');
  return calls.reduce((sum, c) => sum + c.attrs.reduce((s, a) => s + sc.costs[a], 0), 0);
}

const scenarios: Scenario[] = [
  {
    name: 'three-condition and (uniform-ish pass rates)',
    attrs: ['a', 'b', 'c'],
    costs: { a: 1, b: 5, c: 20 },
    domains: { a: [0, 1, 2], b: [0, 1, 2], c: [0, 1, 2] },
    conds: { a: (v) => v > 0, b: (v) => v > 1, c: (v) => v === 2 },
    policy: and(gt('a', 0), gt('b', 1), oneOf('c', [2])),
  },
  {
    name: 'cheap almost-always-true vs costly almost-always-false',
    attrs: ['d', 'e'],
    costs: { d: 1, e: 2 },
    domains: { d: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9], e: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] },
    conds: { d: (v) => v > 0, e: (v) => v > 8 },
    policy: and(gt('d', 0), gt('e', 8)),
  },
];

test('cost benchmark over the full enumeration of fetch orders', async (t) => {
  const lines: string[] = [];
  for (const sc of scenarios) {
    const heuristicOrder = plan(sc.policy, sc.costs).map((s) => s.attr);
    const rows = permutations(sc.attrs).map((order) => ({ order, total: totalCostForOrder(order, sc) }));
    const optimal = Math.min(...rows.map((r) => r.total));
    const heuristicTotal = rows.find((r) => r.order.join() === heuristicOrder.join())!.total;

    // The scheduler, run for real over every assignment, must match the analytic model exactly.
    const actual = await totalCostViaScheduler(heuristicOrder, sc);
    assert.equal(actual, heuristicTotal, `${sc.name}: scheduler cost must match the analytic expectation`);

    assert.ok(heuristicTotal >= optimal, 'heuristic can never beat the enumerated optimum');
    lines.push(`scenario: ${sc.name}`);
    for (const r of rows.sort((x, y) => x.total - y.total)) {
      const tag = r.order.join() === heuristicOrder.join() ? '  <- heuristic (cheapest-first)' : '';
      lines.push(`  order [${r.order.join(', ')}]  total cost ${r.total}${tag}`);
    }
    lines.push(`  optimal total ${optimal}; heuristic total ${heuristicTotal}; gap ${heuristicTotal - optimal}`);
    lines.push('  note: cheapest-first is a heuristic and is NOT claimed to be globally minimal');
    t.diagnostic(lines.splice(0).join('\n'));
  }

  // The second scenario is constructed so the heuristic is strictly suboptimal,
  // which is exactly why no global-minimum claim is made.
  const sc2 = scenarios[1];
  const h = plan(sc2.policy, sc2.costs).map((s) => s.attr);
  const opt = Math.min(...permutations(sc2.attrs).map((o) => totalCostForOrder(o, sc2)));
  assert.ok(totalCostForOrder(h, sc2) > opt, 'heuristic is demonstrably not globally minimal here');
});
