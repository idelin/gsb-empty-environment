import test from 'node:test';
import assert from 'node:assert/strict';
import { PolicyEvaluator } from '../src/evaluator.ts';
import { and, cmp } from '../src/policy.ts';
import type { Expr } from '../src/policy.ts';
import { ScriptedSource, valuesOutcome } from './helpers.ts';

interface CondDef {
  readonly attr: string;
  readonly cond: Expr;
  readonly value: number;
  readonly cost: number;
}

// A: cheap and true. B: medium cost and FALSE (decisive). C: expensive and true.
const DEFS: Record<string, CondDef> = {
  A: { attr: 'a', cond: cmp('a', 'ge', 1), value: 1, cost: 1 },
  B: { attr: 'b', cond: cmp('b', 'ge', 5), value: 1, cost: 10 },
  C: { attr: 'c', cond: cmp('c', 'ge', 1), value: 1, cost: 100 },
};

function permutations(items: readonly string[]): string[][] {
  if (items.length <= 1) {
    return [[...items]];
  }
  const out: string[][] = [];
  for (const item of items) {
    for (const rest of permutations(items.filter((x) => x !== item))) {
      out.push([item, ...rest]);
    }
  }
  return out;
}

function build(order: readonly string[]) {
  const sources = order.map(
    (name) =>
      new ScriptedSource(`s-${name}`, new Set([DEFS[name]!.attr]), DEFS[name]!.cost, 0, () =>
        valuesOutcome('v1', { [DEFS[name]!.attr]: DEFS[name]!.value }),
      ),
  );
  const ev = new PolicyEvaluator(sources);
  const policy = and(...order.map((n) => DEFS[n]!.cond));
  const cost = () => sources.reduce((sum, s) => sum + s.totalCost(), 0);
  return { ev, policy, cost };
}

test('full enumeration of fetch orders gives a cost baseline; the heuristic claims no optimum', async () => {
  const costs = new Map<string, number>();
  for (const order of permutations(['A', 'B', 'C'])) {
    const { ev, policy, cost } = build(order);
    const rec = await ev.evaluate(policy, { subject: { id: 'u' }, permission: 'p' }, 'as-written');
    assert.equal(rec.decision, 'deny');
    costs.set(order.join(''), cost());
  }

  assert.deepEqual(Object.fromEntries(costs), {
    ABC: 11,
    ACB: 111,
    BAC: 10,
    BCA: 10,
    CAB: 111,
    CBA: 110,
  });

  const { ev, policy, cost } = build(['A', 'B', 'C']);
  const plan = ev.planOf(policy);
  const rec = await ev.evaluate(policy, { subject: { id: 'u' }, permission: 'p' }, 'planned');
  assert.equal(rec.decision, 'deny');
  const heuristicCost = cost();

  const all = [...costs.values()];
  const min = Math.min(...all);
  const max = Math.max(...all);

  assert.equal(plan.claimsOptimal, false, 'the heuristic explicitly claims no global optimum');
  assert.ok(heuristicCost >= min && heuristicCost <= max, 'heuristic stays within the enumerated range');
  // Cheapest-first orders A,B,C (cost 11). The true minimum (10) needs the
  // decisive false condition B first, which a cost-only heuristic cannot know
  // without selectivity estimates. This gap is expected and documented.
  assert.equal(heuristicCost, 11);
  assert.ok(heuristicCost > min, 'heuristic is not globally minimal on this instance');

  console.log('enumeration baseline:', Object.fromEntries(costs));
  console.log(`heuristic cost=${heuristicCost}, enumerated min=${min}, max=${max}`);
});
