import test from 'node:test';
import assert from 'node:assert/strict';
import { PolicyEvaluator } from '../src/evaluator.ts';
import { and, cmp } from '../src/policy.ts';
import { AttrFetchError, FetchTimeoutError, VersionConflictError } from '../src/types.ts';
import { ScriptedSource, valuesOutcome } from './helpers.ts';

test('fetch timeout rejects deterministically and a later evaluation recovers', async () => {
  const src = new ScriptedSource('s', new Set(['x']), 1, 0, (_keys, i) =>
    i === 0 ? new Promise(() => {}) : valuesOutcome('v1', { x: 1 }),
  );
  const ev = new PolicyEvaluator([src], { fetchTimeoutMs: 20 });

  await assert.rejects(
    ev.evaluate(cmp('x', 'eq', 1), { subject: { id: 'u' }, permission: 'p' }),
    FetchTimeoutError,
  );
  assert.equal(ev.records().length, 0, 'failed evaluation commits nothing');

  const rec = await ev.evaluate(cmp('x', 'eq', 1), { subject: { id: 'u' }, permission: 'p' });
  assert.equal(rec.decision, 'allow');
  assert.equal(src.calls.length, 2, 'timed-out batch does not poison later fetches');
  assert.equal(ev.records().length, 1);
});

test('mixed-version reads trigger a retry and converge on a consistent version', async () => {
  const src = new ScriptedSource('s', new Set(['a', 'b']), 1, 0, (keys, i) =>
    i === 0
      ? valuesOutcome('v1', { a: 5 })
      : valuesOutcome('v2', Object.fromEntries(keys.map((k) => [k, 5]))),
  );
  const ev = new PolicyEvaluator([src]);
  const policy = and(cmp('a', 'ge', 1), cmp('b', 'ge', 1));

  const rec = await ev.evaluate(policy, { subject: { id: 'u' }, permission: 'p' }, 'as-written');
  assert.equal(rec.decision, 'allow');
  assert.equal(rec.attempts, 2, 'one version conflict -> exactly one retry');
  assert.deepEqual(
    rec.evidence.map((e) => `${e.key}@${e.version}`),
    ['a@v2', 'b@v2'],
    'all evidence in the committed record is at a single consistent version',
  );
  assert.deepEqual(
    src.calls.map((c) => c.keys),
    [['a'], ['b'], ['a'], ['b']],
  );
});

test('version churn beyond the retry budget fails deterministically', async () => {
  let n = 0;
  const src = new ScriptedSource('s', new Set(['a', 'b']), 1, 0, (keys) =>
    valuesOutcome(`v${++n}`, Object.fromEntries(keys.map((k) => [k, 1]))),
  );
  const ev = new PolicyEvaluator([src], { maxVersionRetries: 2 });
  const policy = and(cmp('a', 'ge', 1), cmp('b', 'ge', 1));

  await assert.rejects(
    ev.evaluate(policy, { subject: { id: 'u' }, permission: 'p' }, 'as-written'),
    VersionConflictError,
  );
  assert.equal(src.calls.length, 6, 'attempts = 1 initial + 2 retries, 2 fetches each');
  assert.equal(ev.records().length, 0);
});

test('partial batch failure: healthy attributes serve, failed ones reject deterministically', async () => {
  const src = new ScriptedSource('s', new Set(['good', 'bad']), 1, 0, () =>
    valuesOutcome('v1', { good: 7 }, { bad: new Error('upstream denied') }),
  );
  const ev = new PolicyEvaluator([src]);

  const pGood = ev.evaluate(cmp('good', 'ge', 5), { subject: { id: 'u' }, permission: 'p' });
  const pBad = ev.evaluate(cmp('bad', 'ge', 5), { subject: { id: 'u' }, permission: 'p' });

  const good = await pGood;
  assert.equal(good.decision, 'allow');
  await assert.rejects(pBad, AttrFetchError);
  assert.equal(src.calls.length, 1, 'both evaluations shared a single partially-failed batch');

  const again = await ev.evaluate(cmp('good', 'ge', 5), { subject: { id: 'u' }, permission: 'p' });
  assert.equal(again.decision, 'allow');
  assert.equal(src.calls.length, 1, 'successful values are cached');

  await assert.rejects(
    ev.evaluate(cmp('bad', 'ge', 5), { subject: { id: 'u' }, permission: 'p' }),
    AttrFetchError,
    'failures are not cached: the retry fetches again and fails the same way',
  );
  assert.equal(src.calls.length, 2);
  assert.equal(ev.records().length, 2, 'only successful evaluations commit records');
});
