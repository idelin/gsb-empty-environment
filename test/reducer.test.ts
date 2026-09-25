import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { Reducer, type ReductionResult } from '../src/reducer.ts';
import { differentialOracle, judge, type Policy } from '../src/policy.ts';
import { isValid, validate, type Schema } from '../src/schema.ts';
import type { Input } from '../src/condition.ts';
import { accessSchema, policyV1, policyV2, failingInput } from '../examples/scenario.ts';

const ruleVersion = `${policyV1.version}+${policyV2.version}`;
const oracle = differentialOracle(policyV1, policyV2);

function journalDir(): string {
  return mkdtempSync(join(tmpdir(), 'reducer-test-'));
}

function run(budget: number, journalPath: string, resume = false, version = ruleVersion): ReductionResult {
  return new Reducer({
    schema: accessSchema, oracle, ruleVersion: version,
    budget, journalPath, resume,
  }).reduce(structuredClone(failingInput));
}

const EXPECTED_MINIMAL: Input = { age: 18, roles: ['admin'], clearance: 5 };

test('reduces attributes, set elements and integers to the 1-minimal candidate', () => {
  const result = run(500, join(journalDir(), 'j.jsonl'));
  assert.deepEqual(result.candidate, EXPECTED_MINIMAL);
  assert.equal(result.status, 'minimal');
  assert.ok(result.oracleCalls <= result.budget);
  // step evidence carries budget state
  for (const step of result.steps) {
    assert.ok(step.op.length > 0);
    assert.ok(step.budgetLeft >= 0);
    assert.ok(isValid(accessSchema, step.after));
  }
});

test('certificate: every declared basic op fails to preserve the difference', () => {
  const result = run(500, join(journalDir(), 'j.jsonl'));
  assert.ok(result.certificate.complete);
  assert.ok(result.certificate.checks.length > 0);
  for (const check of result.certificate.checks) {
    assert.notEqual(check.verdict, 'diff', `op still preserves: ${check.op}`);
    assert.notEqual(check.verdict, 'not-checked');
  }
});

test('dependent and necessary attributes are kept (deleting them kills validity or the diff)', () => {
  const result = run(500, join(journalDir(), 'j.jsonl'));
  // clearance is dependency-bound to roles containing admin: not removable.
  assert.ok('clearance' in result.candidate);
  assert.deepEqual(result.candidate.roles, ['admin']);
  // sanity: dropping any of these either breaks the schema or the difference
  const noClearance: Input = { ...result.candidate };
  delete noClearance.clearance;
  assert.ok(!isValid(accessSchema, noClearance));
  const noAdmin = { ...result.candidate, roles: ['viewer'], clearance: undefined };
  assert.ok(oracle(noAdmin as Input) !== 'diff' || !isValid(accessSchema, noAdmin as Input));
});

test('validity first: the oracle never sees a schema-invalid candidate', () => {
  const seen: Input[] = [];
  const spying = (input: Input) => {
    seen.push(structuredClone(input));
    return oracle(input);
  };
  new Reducer({
    schema: accessSchema, oracle: spying, ruleVersion,
    budget: 500, journalPath: join(journalDir(), 'j.jsonl'),
  }).reduce(structuredClone(failingInput));
  assert.ok(seen.length > 0);
  for (const input of seen) {
    assert.deepEqual(validate(accessSchema, input), []);
  }
});

test('minimization is idempotent', () => {
  const first = run(500, join(journalDir(), 'j.jsonl'));
  const second = new Reducer({
    schema: accessSchema, oracle, ruleVersion,
    budget: 500, journalPath: join(journalDir(), 'j2.jsonl'),
  }).reduce(structuredClone(first.candidate));
  assert.deepEqual(second.candidate, first.candidate);
  assert.equal(second.steps.length, 0, 'no further reduction step on re-run');
  assert.equal(second.status, 'minimal');
});

test('budget exhaustion claims only a local minimum, never minimal', () => {
  const dir = journalDir();
  const starved = run(4, join(dir, 'j.jsonl'));
  assert.equal(starved.status, 'local-minimum');
  assert.ok(starved.reason.includes('budget'));
  assert.equal(starved.oracleCalls, 4);
  // journal must not record a minimal finish either
  const events = readFileSync(join(dir, 'j.jsonl'), 'utf8').trim().split('\n').map((l) => JSON.parse(l));
  const finish = events.find((e) => e.type === 'finish');
  assert.equal(finish.status, 'local-minimum');
});

test('journal records oracle results, rule version, steps and best checkpoints', () => {
  const dir = journalDir();
  const path = join(dir, 'j.jsonl');
  run(500, path);
  const events = readFileSync(path, 'utf8').trim().split('\n').map((l) => JSON.parse(l));
  const start = events.find((e) => e.type === 'run-start');
  assert.equal(start.ruleVersion, ruleVersion);
  assert.ok(events.some((e) => e.type === 'oracle'));
  assert.ok(events.some((e) => e.type === 'step' && typeof e.budgetLeft === 'number'));
  assert.ok(events.some((e) => e.type === 'best'));
  const finish = events.find((e) => e.type === 'finish');
  assert.equal(finish.status, 'minimal');
});

test('resume never loses the checkpointed candidate and converges to the same minimum', () => {
  const dir = journalDir();
  const path = join(dir, 'j.jsonl');
  const starved = run(4, path);
  const resumed = run(500, path, true);
  assert.deepEqual(resumed.candidate, EXPECTED_MINIMAL);
  // the resumed run started from the checkpoint, not from scratch:
  assert.ok(
    JSON.stringify(resumed.steps[0]?.before ?? resumed.candidate).length <=
      JSON.stringify(failingInput).length,
  );
  assert.notDeepEqual(starved.candidate, failingInput); // starved run made progress that was kept
});

test('cached oracle results are keyed by rule version', () => {
  const dir = journalDir();
  const path = join(dir, 'j.jsonl');
  run(500, path);
  const counting = () => {
    let calls = 0;
    const wrapped = (input: Input) => { calls++; return oracle(input); };
    return { wrapped, calls: () => calls };
  };
  // same rule version: every candidate is already cached, zero new oracle calls
  const same = counting();
  new Reducer({
    schema: accessSchema, oracle: same.wrapped, ruleVersion,
    budget: 500, journalPath: path, resume: true,
  }).reduce(structuredClone(failingInput));
  assert.equal(same.calls(), 0);
  // different rule version: cache ignored, candidates re-evaluated
  const other = counting();
  new Reducer({
    schema: accessSchema, oracle: other.wrapped, ruleVersion: 'v9+v10',
    budget: 500, journalPath: path, resume: true,
  }).reduce(structuredClone(failingInput));
  assert.ok(other.calls() > 0);
});

test('initial input must be valid and exhibit the difference', () => {
  const dir = journalDir();
  assert.throws(() =>
    new Reducer({
      schema: accessSchema, oracle, ruleVersion,
      budget: 100, journalPath: join(dir, 'a.jsonl'),
    }).reduce({ age: 200, roles: ['viewer'] }), /not schema-valid/);
  assert.throws(() =>
    new Reducer({
      schema: accessSchema, oracle, ruleVersion,
      budget: 100, journalPath: join(dir, 'b.jsonl'),
    }).reduce({ age: 30, roles: ['viewer'] }), /does not exhibit/);
});

test('optional attribute that is necessary for the diff is kept', () => {
  // Difference exists only while the optional boolean flag is present and true.
  const schema: Schema = {
    name: 'flag-demo',
    fields: {
      flag: { type: { kind: 'bool' } },
      noise: { type: { kind: 'int', min: 0, max: 100 } },
    },
  };
  const pA: Policy = {
    name: 'p', version: 'a', default: 'deny',
    rules: [{ when: { op: 'flag', attr: 'flag', is: true }, label: 'allow' }],
  };
  const pB: Policy = { name: 'p', version: 'b', default: 'deny', rules: [] };
  const diff = differentialOracle(pA, pB);
  const result = new Reducer({
    schema, oracle: diff, ruleVersion: 'a+b',
    budget: 200, journalPath: join(journalDir(), 'j.jsonl'),
  }).reduce({ flag: true, noise: 42 });
  // the irrelevant optional integer is removed entirely; the necessary flag stays
  assert.deepEqual(result.candidate, { flag: true });
  assert.equal(result.status, 'minimal');
  assert.equal(judge(pA, result.candidate), 'allow');
  assert.equal(judge(pB, result.candidate), 'deny');
});
