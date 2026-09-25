import test from 'node:test';
import assert from 'node:assert/strict';
import { PolicyEvaluator } from '../src/evaluator.ts';
import { and, cmp } from '../src/policy.ts';
import { CancelledError } from '../src/types.ts';
import type { FetchOutcome } from '../src/types.ts';
import { ScriptedSource, valuesOutcome, waitFor } from './helpers.ts';

test('two concurrent evaluations share one fetch; cancelling one does not affect the other', async () => {
  let release: (o: FetchOutcome) => void = () => {};
  const gate = new Promise<FetchOutcome>((r) => {
    release = r;
  });
  const src = new ScriptedSource('s', new Set(['x', 'y']), 5, 0, (_keys, i) =>
    i === 0 ? gate : valuesOutcome('v1', { y: 5 }),
  );
  const ev = new PolicyEvaluator([src]);
  const policy = and(cmp('x', 'ge', 1), cmp('y', 'ge', 1));

  const c1 = new AbortController();
  const p1 = ev.evaluate(policy, { subject: { id: 'u' }, permission: 'p', signal: c1.signal });
  const p2 = ev.evaluate(policy, { subject: { id: 'u' }, permission: 'p' });

  await waitFor(() => src.calls.length === 1);
  assert.deepEqual(src.calls[0]!.keys, ['x'], 'both evaluations coalesce into one batch fetch');

  c1.abort();
  await assert.rejects(p1, CancelledError);

  release(valuesOutcome('v1', { x: 5 }));
  const r2 = await p2;
  assert.equal(r2.decision, 'allow');
  assert.deepEqual(
    src.calls.map((c) => c.keys),
    [['x'], ['y']],
    'cancelled evaluation never triggers the y fetch; survivor proceeds lazily',
  );
  assert.equal(ev.records().length, 1, 'only the completed evaluation commits a record');
  assert.equal(ev.records()[0]!.decision, 'allow');
  assert.deepEqual(
    ev.records()[0]!.evidence.map((e) => `${e.key}@${e.version}`),
    ['x@v1', 'y@v1'],
  );
});

test('aborting before the first flush means no fetch happens at all', async () => {
  const src = new ScriptedSource('s', new Set(['x']), 1, 0, () => valuesOutcome('v1', { x: 1 }));
  const ev = new PolicyEvaluator([src]);
  const c = new AbortController();
  const p = ev.evaluate(cmp('x', 'eq', 1), {
    subject: { id: 'u' },
    permission: 'p',
    signal: c.signal,
  });
  c.abort();
  await assert.rejects(p, CancelledError);
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(src.calls.length, 0);
  assert.equal(ev.records().length, 0);
});

test('cache merges reads for the same subject+permission and isolates different permissions', async () => {
  const src = new ScriptedSource('s', new Set(['x']), 1, 0, () => valuesOutcome('v1', { x: 7 }));
  const ev = new PolicyEvaluator([src]);
  const policy = cmp('x', 'eq', 7);

  const a = await ev.evaluate(policy, { subject: { id: 'u' }, permission: 'admin' });
  const b = await ev.evaluate(policy, { subject: { id: 'u' }, permission: 'admin' });
  assert.equal(a.decision, 'allow');
  assert.equal(b.decision, 'allow');
  assert.equal(src.calls.length, 1, 'second evaluation for same subject+permission hits the cache');

  const c = await ev.evaluate(policy, { subject: { id: 'u' }, permission: 'guest' });
  assert.equal(c.decision, 'allow');
  assert.equal(src.calls.length, 2, 'different permission scope is isolated and fetches separately');
  assert.deepEqual(
    src.calls.map((call) => call.permission),
    ['admin', 'guest'],
  );
});

test('reentrant fetcher calls are serialized into a later batch, after the in-flight one', async () => {
  let ev: PolicyEvaluator;
  let innerPromise: Promise<unknown> = Promise.resolve();
  const src = new ScriptedSource('s', new Set(['a', 'a2']), 1, 0, (keys, i) => {
    if (i === 0) {
      // Reentrant call: the fetcher itself starts another evaluation for the
      // same subject+permission+source. It must not deadlock or interleave;
      // it is served by a subsequent batch after the current one completes.
      innerPromise = ev.evaluate(cmp('a2', 'eq', 2), { subject: { id: 'u' }, permission: 'p' });
      return valuesOutcome('v1', { a: 1 });
    }
    return valuesOutcome('v1', { a2: 2 });
  });
  ev = new PolicyEvaluator([src]);

  const outer = await ev.evaluate(cmp('a', 'eq', 1), { subject: { id: 'u' }, permission: 'p' });
  const inner = (await innerPromise) as { decision: string };
  assert.equal(outer.decision, 'allow');
  assert.equal(inner.decision, 'allow');
  assert.deepEqual(
    src.calls.map((c) => c.keys),
    [['a'], ['a2']],
    'deterministic order: outer batch first, reentrant batch second',
  );
});
