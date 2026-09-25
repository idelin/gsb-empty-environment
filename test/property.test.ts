/**
 * Randomized cross-check of the engine and the incremental session against
 * the independent brute-force reference (test/reference.ts), covering hard
 * rejects, negative weights, mutually exclusive conditions, shared unknowns
 * and duplicate rule ids.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, REJECT, type EvalResult } from '../src/engine.ts';
import { normalizePolicy, type PolicySpec } from '../src/policy.ts';
import { GradingSession } from '../src/session.ts';
import { refEvaluate, refEval, refGradeOf, toFrac, fadd, fcmp, type RefResult } from './reference.ts';
import type { Condition } from '../src/condition.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function makeGen(rand: () => number) {
  const int = (lo: number, hi: number) => lo + Math.floor(rand() * (hi - lo + 1));
  const pick = <T>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)];

  const cond = (names: string[], domains: Record<string, number[]>, depth: number): Condition => {
    if (depth <= 0 || rand() < 0.55) {
      const attr = pick(names);
      const domain = domains[attr];
      if (rand() < 0.3) {
        const size = int(1, domain.length);
        const set = [...domain].sort(() => rand() - 0.5).slice(0, size);
        return { kind: 'in', attr, set };
      }
      return {
        kind: 'cmp',
        attr,
        op: pick(['eq', 'ne', 'lt', 'le', 'gt', 'ge'] as const),
        value: pick(domain),
      };
    }
    if (rand() < 0.25) return { kind: 'not', arg: cond(names, domains, depth - 1) };
    const kind = rand() < 0.5 ? 'and' : 'or';
    const n = int(2, 3);
    return { kind, args: Array.from({ length: n }, () => cond(names, domains, depth - 1)) } as Condition;
  };

  const policy = (): PolicySpec => {
    const attrCount = int(2, 4);
    const names = ['a', 'b', 'c', 'd'].slice(0, attrCount);
    const domains: Record<string, number[]> = {};
    for (const n of names) {
      const size = int(1, 3);
      const pool = [-2, -1, 0, 1, 2, 3].sort(() => rand() - 0.5);
      domains[n] = pool.slice(0, size).sort((x, y) => x - y);
    }
    const ruleCount = int(1, 6);
    const rules: PolicySpec['rules'] = [];
    for (let i = 0; i < ruleCount; i++) {
      const rule = {
        id: `r${i}`,
        when: cond(names, domains, 2),
        weight: { num: int(-5, 5), den: int(1, 4) },
        hardReject: rand() < 0.15,
      };
      rules.push(rule);
      if (rand() < 0.15) rules.push({ ...rule }); // identical duplicate id: counted once
    }
    const bandCount = int(1, 3);
    const bands: PolicySpec['bands'] = [{ grade: 'g0', gte: null }];
    let prev = int(-6, 0);
    for (let i = 1; i < bandCount; i++) {
      prev += int(1, 5);
      bands.push({ grade: `g${i}`, gte: { num: prev, den: 2 } });
    }
    return { attributes: domains, rules, bands };
  };

  const knownAttrs = (spec: PolicySpec): Record<string, number> => {
    const known: Record<string, number> = {};
    for (const [name, domain] of Object.entries(spec.attributes)) {
      if (rand() < 0.6) known[name] = pick(domain);
    }
    return known;
  };

  return { policy, knownAttrs, int, pick };
}

function outcomeKeys(r: EvalResult): string[] {
  return r.possibleOutcomes.map((o) => (o.type === 'grade' ? o.grade : REJECT));
}

function assertMatchesReference(spec: PolicySpec, known: Record<string, number>, r: EvalResult, ref: RefResult): void {
  assert.deepEqual(outcomeKeys(r), ref.outcomes, `outcomes for ${JSON.stringify(known)}`);
  assert.equal(r.status, ref.outcomes.length === 1 ? 'definite' : 'pending');

  if (ref.minScore === null) {
    assert.equal(r.scoreBounds, null);
  } else {
    assert.ok(r.scoreBounds, 'bounds expected');
    assert.equal(fcmp(toFrac(r.scoreBounds!.min.toString()), ref.minScore), 0, 'min bound');
    assert.equal(fcmp(toFrac(r.scoreBounds!.max.toString()), ref.maxScore), 0, 'max bound');
  }

  // Every witness must genuinely produce its outcome under the reference,
  // and must assign every declared attribute exactly one in-domain value.
  for (const [key, witness] of r.witnesses) {
    for (const [name, domain] of Object.entries(spec.attributes)) {
      assert.ok(domain.includes(witness[name]), `witness ${key}: ${name} in domain`);
    }
    const seen = new Set<string>();
    let rejected = false;
    let score = toFrac(0);
    for (const rule of spec.rules) {
      if (seen.has(rule.id)) continue;
      seen.add(rule.id);
      if (!refEval(rule.when as Condition, witness)) continue;
      if (rule.hardReject) rejected = true;
      else score = fadd(score, toFrac(rule.weight ?? 0));
    }
    const actualKey = rejected ? REJECT : refGradeOf(spec, score);
    assert.equal(actualKey, key, `witness for "${key}" actually yields "${actualKey}"`);
  }
}

test('engine matches exhaustive reference on 300 random policies', () => {
  const rand = mulberry32(20260925);
  const gen = makeGen(rand);
  for (let trial = 0; trial < 300; trial++) {
    const spec = gen.policy();
    const known = gen.knownAttrs(spec);
    const policy = normalizePolicy(spec);
    const r = evaluate(policy, known);
    const ref = refEvaluate(spec, known);
    assertMatchesReference(spec, known, r, ref);
  }
});

test('incremental session matches fresh engine across random correction sequences', () => {
  const rand = mulberry32(777);
  const gen = makeGen(rand);
  for (let trial = 0; trial < 120; trial++) {
    const spec = gen.policy();
    const policy = normalizePolicy(spec);
    const session = new GradingSession(policy, gen.knownAttrs(spec));
    const names = Object.keys(spec.attributes);
    for (let step = 0; step < 6; step++) {
      const name = gen.pick(names);
      if (rand() < 0.7) session.setAttribute(name, gen.pick(spec.attributes[name]));
      else session.removeAttribute(name);
      const got = session.evaluate();
      const want = evaluate(policy, session.currentAttributes());
      assert.deepEqual(outcomeKeys(got), outcomeKeys(want), `trial ${trial} step ${step}`);
      assert.equal(got.status, want.status);
      assert.deepEqual(got.definiteOutcome, want.definiteOutcome);
      const fmt = (b: { min: { toString(): string }; max: { toString(): string } } | null) =>
        b === null ? null : `${b.min}..${b.max}`;
      assert.equal(fmt(got.scoreBounds), fmt(want.scoreBounds), `bounds trial ${trial} step ${step}`);
    }
  }
});
