/**
 * Cross-validation: the main evaluator and the incremental engine are checked
 * against the independent exhaustive reference on randomized policies,
 * covering hard rejects, negative rational weights and mutually exclusive
 * conditions.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Condition } from '../src/condition.ts';
import { loadPolicy } from '../src/policy.ts';
import type { PolicySpec } from '../src/policy.ts';
import { evaluate } from '../src/evaluator.ts';
import { referenceEvaluate } from '../src/reference.ts';
import { IncrementalEngine } from '../src/incremental.ts';
import { cmp, toString } from '../src/rational.ts';
import type { AttributeValue } from '../src/schema.ts';

const schema = {
  a: { type: 'int', min: 0, max: 3 },
  b: { type: 'int', min: 0, max: 2 },
  c: { type: 'bool' },
  d: { type: 'enum', values: ['x', 'y'] },
} as const;

type Rng = () => number;

function mulberry32(seed: number): Rng {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ATTRS = ['a', 'b', 'c', 'd'] as const;
const CMPS = ['eq', 'ne', 'lt', 'lte', 'gt', 'gte'] as const;

function randomCondition(rng: Rng, depth: number): Condition {
  const pick = rng();
  if (depth > 0 && pick < 0.35) {
    const n = 2 + Math.floor(rng() * 2);
    const args = Array.from({ length: n }, () => randomCondition(rng, depth - 1));
    return { op: rng() < 0.5 ? 'and' : 'or', args };
  }
  if (depth > 0 && pick < 0.45) {
    return { op: 'not', arg: randomCondition(rng, depth - 1) };
  }
  const attr = ATTRS[Math.floor(rng() * ATTRS.length)];
  const dom = schema[attr];
  if (dom.type === 'bool') return { op: 'bool', attr, value: rng() < 0.5 };
  if (dom.type === 'enum') {
    return { op: 'in', attr, values: [dom.values[Math.floor(rng() * dom.values.length)]] };
  }
  const value = dom.min + Math.floor(rng() * (dom.max - dom.min + 1));
  return { op: 'cmp', attr, cmp: CMPS[Math.floor(rng() * CMPS.length)], value };
}

function randomPolicy(rng: Rng): PolicySpec {
  const rules: PolicySpec['rules'] = [];
  const nRules = 4 + Math.floor(rng() * 5);
  for (let i = 0; i < nRules; i++) {
    const id = `r${i}`;
    if (rng() < 0.2) {
      rules.push({ id, when: randomCondition(rng, 2), hardReject: true });
      continue;
    }
    // every third scoring rule gets a partner with a mutually exclusive guard
    if (i % 3 === 0) {
      const attr = (['a', 'b'] as const)[Math.floor(rng() * 2)];
      const cut = schema[attr].min + Math.floor(rng() * (schema[attr].max - schema[attr].min + 1));
      rules.push({
        id,
        when: { op: 'cmp', attr, cmp: 'gte', value: cut },
        weight: 1 + Math.floor(rng() * 4),
      });
      rules.push({
        id: `${id}-mutex`,
        when: { op: 'cmp', attr, cmp: 'lt', value: cut },
        weight: 1 + Math.floor(rng() * 4),
      });
      continue;
    }
    const num = -3 + Math.floor(rng() * 7); // negative weights included
    const den = 1 + Math.floor(rng() * 3);
    rules.push({ id, when: randomCondition(rng, 2), weight: `${num}/${den}` });
  }
  const t1 = -2 + Math.floor(rng() * 3);
  const t2 = t1 + 1 + Math.floor(rng() * 4);
  return {
    schema,
    rules,
    thresholds: [
      { grade: 'hi', min: t2 },
      { grade: 'mid', min: t1 },
      { grade: 'lo', min: -100 },
    ],
  };
}

function randomAttrs(rng: Rng): Record<string, AttributeValue> {
  const out: Record<string, AttributeValue> = {};
  for (const name of ATTRS) {
    if (rng() < 0.4) continue; // leave unknown
    const dom = schema[name];
    if (dom.type === 'bool') out[name] = rng() < 0.5;
    else if (dom.type === 'enum') out[name] = dom.values[Math.floor(rng() * dom.values.length)];
    else out[name] = dom.min + Math.floor(rng() * (dom.max - dom.min + 1));
  }
  return out;
}

function summarizeMain(r: ReturnType<typeof evaluate>) {
  return {
    outcomes: r.outcomes.map((o) => (o.kind === 'rejected' ? 'rejected' : `grade:${o.grade}`)).sort(),
    scores: r.reachableScores.map(toString),
    min: toString(r.scoreBounds.min),
    max: toString(r.scoreBounds.max),
  };
}

function summarizeRef(r: ReturnType<typeof referenceEvaluate>) {
  let min = r.rows[0].score;
  let max = r.rows[0].score;
  for (const row of r.rows) {
    if (cmp(row.score, min) < 0) min = row.score;
    if (cmp(row.score, max) > 0) max = row.score;
  }
  return {
    outcomes: r.reachableOutcomes,
    scores: r.reachableScores,
    min: toString(min),
    max: toString(max),
  };
}

test('evaluator and incremental engine match the exhaustive reference (200 random policies)', () => {
  const rng = mulberry32(0x5eed);
  for (let trial = 0; trial < 200; trial++) {
    const spec = randomPolicy(rng);
    const policy = loadPolicy(spec);
    const attrs = randomAttrs(rng);

    const expected = summarizeRef(referenceEvaluate(policy, attrs));
    assert.deepEqual(summarizeMain(evaluate(policy, attrs)), expected, `evaluator mismatch, trial ${trial}`);

    // incremental engine from scratch must agree
    const engine = new IncrementalEngine(policy);
    engine.setAttributes(attrs);
    assert.deepEqual(summarizeMain(engine.evaluate()), expected, `engine mismatch, trial ${trial}`);

    // apply two random corrections and re-check each time
    for (let step = 0; step < 2; step++) {
      const change: Record<string, AttributeValue | null> = {};
      const name = ATTRS[Math.floor(rng() * ATTRS.length)];
      if (rng() < 0.3) {
        change[name] = null; // back to unknown
        delete attrs[name];
      } else {
        const dom = schema[name];
        const v: AttributeValue =
          dom.type === 'bool'
            ? rng() < 0.5
            : dom.type === 'enum'
              ? dom.values[Math.floor(rng() * dom.values.length)]
              : dom.min + Math.floor(rng() * (dom.max - dom.min + 1));
        change[name] = v;
        attrs[name] = v;
      }
      engine.update(change);
      const after = summarizeRef(referenceEvaluate(policy, attrs));
      assert.deepEqual(
        summarizeMain(engine.evaluate()),
        after,
        `engine mismatch after correction, trial ${trial} step ${step}`,
      );
    }
  }
});

test('witnesses produced by the evaluator satisfy the reference rows', () => {
  const rng = mulberry32(0xc0ffee);
  for (let trial = 0; trial < 100; trial++) {
    const policy = loadPolicy(randomPolicy(rng));
    const attrs = randomAttrs(rng);
    const result = evaluate(policy, attrs);
    const ref = referenceEvaluate(policy, attrs);
    for (const outcome of result.outcomes) {
      const row = ref.rows.find((r) =>
        Object.entries(outcome.witness).every(([k, v]) => r.assignment[k] === v),
      );
      assert.ok(row, 'witness must correspond to a reference assignment');
      if (outcome.kind === 'rejected') assert.equal(row!.rejected, true);
      else {
        assert.equal(row!.rejected, false);
        assert.equal(row!.grade, outcome.grade);
      }
    }
  }
});
