import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { failingInput, policyA, policyB, schema } from '../src/fixture.ts';
import { Reducer } from '../src/reducer.ts';
import { validate, evalCond } from '../src/constraints.ts';
import { Oracle } from '../src/oracle.ts';
import { cascadeRemove, enumerateOps, applyOp } from '../src/ops.ts';
import type { Input } from '../src/model.ts';

const opts = { schema, policyA, policyB };

function differs(input: Input): boolean {
  const o = new Oracle(policyA, policyB);
  const p = o.evaluate(input);
  return p.a === true && p.b === false;
}

test('reduces irrelevant attributes, set elements and integers', () => {
  const r = new Reducer({ ...opts, budget: 500 }).reduce(failingInput);
  assert.deepEqual(r.candidate, { requestCount: 50, betaOptIn: true, quotaOverride: 40 });
  assert.equal(r.status, '1-minimal');
});

test('candidate stays schema-valid and keeps the same judgment difference', () => {
  const r = new Reducer({ ...opts, budget: 500 }).reduce(failingInput);
  assert.ok(validate(schema, r.candidate).ok);
  assert.ok(differs(r.candidate));
});

test('necessary attributes are kept: removing them kills the difference', () => {
  const r = new Reducer({ ...opts, budget: 500 }).reduce(failingInput);
  for (const attr of ['betaOptIn', 'quotaOverride', 'requestCount']) {
    const removed = cascadeRemove(schema, r.candidate, attr);
    assert.ok(!validate(schema, removed).ok || !differs(removed), `${attr} is necessary`);
  }
  // evidence shows the removal attempts were rejected
  const betaRemovals = r.evidence.filter(
    (e) => e.op.includes('betaOptIn') && e.outcome === 'rejected-difference-lost',
  );
  assert.ok(betaRemovals.length > 0);
});

test('field dependency: quotaOverride requires betaOptIn == true', () => {
  assert.ok(!validate(schema, { betaOptIn: false, quotaOverride: 50 }).ok);
  assert.ok(!validate(schema, { quotaOverride: 50 }).ok);
  assert.ok(validate(schema, { betaOptIn: true, quotaOverride: 50 }).ok);
  // cascade removal drops dependents so the candidate can stay valid
  const cascaded = cascadeRemove(schema, failingInput, 'betaOptIn');
  assert.equal(cascaded.quotaOverride, undefined);
  assert.ok(validate(schema, cascaded).ok);
});

test('schema constraint quotaOverride >= 10 is enforced during reduction', () => {
  const r = new Reducer({ ...opts, budget: 500 }).reduce(failingInput);
  assert.ok(evalCond({ op: 'int', attr: 'quotaOverride', cmp: '>=', value: 10 }, r.candidate));
  // invalid candidates are rejected without spending oracle budget
  const invalidSteps = r.evidence.filter((e) => e.outcome === 'rejected-invalid');
  for (const s of invalidSteps) assert.equal(s.verdicts, undefined);
});

test('1-minimal certificate covers every declared basic op on the result', () => {
  const r = new Reducer({ ...opts, budget: 500 }).reduce(failingInput);
  assert.equal(r.status, '1-minimal');
  assert.ok(r.certificate);
  const declared = enumerateOps(schema, r.candidate)
    .filter((op) => applyOp(schema, r.candidate, op) !== null)
    .length;
  assert.equal(r.certificate.length, declared);
  assert.ok(r.certificate.every((c) => c.outcome !== 'accepted'));
});

test('minimization is idempotent', () => {
  const once = new Reducer({ ...opts, budget: 500 }).reduce(failingInput);
  const twice = new Reducer({ ...opts, budget: 500 }).reduce(once.candidate);
  assert.deepEqual(twice.candidate, once.candidate);
  assert.equal(twice.status, '1-minimal');
});

test('budget exhaustion yields only a local minimum, never a certificate', () => {
  const r = new Reducer({ ...opts, budget: 5 }).reduce(failingInput);
  assert.equal(r.status, 'local-minimum');
  assert.equal(r.certificate, undefined);
  assert.ok(r.budgetExhausted);
  assert.ok(r.budget.spent <= 5);
  // the partial candidate is still valid and still exhibits the difference
  assert.ok(validate(schema, r.candidate).ok);
  assert.ok(differs(r.candidate));
});

test('oracle cache: re-evaluating an input costs no extra budget', () => {
  const o = new Oracle(policyA, policyB);
  o.evaluate(failingInput);
  o.evaluate(failingInput);
  assert.equal(o.spent, 1);
});

test('journal is saved step by step and recovery never loses the candidate', () => {
  const dir = mkdtempSync(join(tmpdir(), 'reducer-'));
  const journal = join(dir, 'journal.json');

  // crash mid-run: tiny budget leaves an in-progress journal
  const interrupted = new Reducer({ ...opts, budget: 8, journalPath: journal }).reduce(failingInput);
  assert.equal(interrupted.status, 'local-minimum');
  assert.ok(existsSync(journal));
  const saved = JSON.parse(readFileSync(journal, 'utf8'));
  assert.deepEqual(saved.candidate, interrupted.candidate);
  assert.equal(saved.ruleVersions.a, 'v2025-Q4');
  assert.equal(saved.ruleVersions.b, 'v2026-Q1');
  assert.ok(saved.evidence.length > 0);

  // resume with more budget: candidate is preserved and reduction completes
  const resumed = Reducer.resume(journal, { ...opts, extraBudget: 500 }).resumeReduce();
  assert.equal(resumed.status, '1-minimal');
  assert.deepEqual(resumed.candidate, { requestCount: 50, betaOptIn: true, quotaOverride: 40 });
  // spent budget carries over across the crash boundary
  assert.ok(resumed.budget.spent > interrupted.budget.spent);
});

test('policy language only reads explicit attributes', () => {
  const sneaky: Input = { ...failingInput, backdoor: 'yes' };
  assert.ok(!validate(schema, sneaky).ok); // undeclared attributes rejected
  assert.equal(evalCond({ op: 'present', attr: 'backdoor' }, failingInput), false);
});

test('initial input without a difference is rejected', () => {
  const noDiff: Input = { requestCount: 10 };
  assert.throws(() => new Reducer({ ...opts, budget: 100 }).reduce(noDiff), /no judgment difference/);
});

test('invalid initial input is rejected', () => {
  assert.throws(
    () => new Reducer({ ...opts, budget: 100 }).reduce({ requestCount: -3 }),
    /invalid/,
  );
});
