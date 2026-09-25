import { test } from 'node:test';
import assert from 'node:assert/strict';
import { and, eq } from '../src/policy.ts';
import {
  AttributeScheduler,
  CancelledError,
  type AttrSource,
  type FetchResult,
} from '../src/scheduler.ts';
import { MemoryEvidenceStore } from '../src/evidence.ts';

const tick = (): Promise<void> => new Promise((r) => setImmediate(r));

test('two concurrent evaluations share one fetch; cancelling one does not affect the other', async () => {
  const calls: string[][] = [];
  const signals: AbortSignal[] = [];
  const deferreds: ((r: FetchResult) => void)[] = [];
  const source: AttrSource = {
    fetch(_subject, attrs, _scope, signal) {
      calls.push([...attrs]);
      signals.push(signal);
      return new Promise<FetchResult>((res) => deferreds.push(res));
    },
  };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), {
    costs: { a: 1, b: 1 },
  });
  const policy = and(eq('a', 1), eq('b', 1));

  const ac1 = new AbortController();
  const p1 = scheduler.evaluate(policy, 'alice', 'scope-a', { signal: ac1.signal });
  const p2 = scheduler.evaluate(policy, 'alice', 'scope-a');
  await tick();

  assert.deepEqual(calls, [['a']], 'both evaluations share a single batched fetch for a');

  ac1.abort();
  await assert.rejects(p1, CancelledError);
  assert.equal(signals[0].aborted, false, 'shared fetch must not be aborted while p2 still needs it');

  deferreds[0]({ version: 1, values: { a: 1 } });
  await tick();
  assert.deepEqual(calls, [['a'], ['b']]);
  deferreds[1]({ version: 1, values: { b: 1 } });

  const d2 = await p2;
  assert.equal(d2.allow, true);
  assert.deepEqual(
    d2.accesses.map((x) => x.attr).sort(),
    ['a', 'b'],
  );
  assert.equal(calls.length, 2, 'no extra fetches after the surviving evaluation completed');
});

test('cache is merged per subject+scope and isolated across permission scopes', async () => {
  const calls: { attrs: string[]; scope: string }[] = [];
  const source: AttrSource = {
    async fetch(_subject, attrs, scope): Promise<FetchResult> {
      calls.push({ attrs: [...attrs], scope });
      return { version: 1, values: Object.fromEntries(attrs.map((a) => [a, 1])) };
    },
  };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), { costs: { a: 1 } });
  const policy = eq('a', 1);

  await scheduler.evaluate(policy, 'alice', 'scope-a');
  await scheduler.evaluate(policy, 'alice', 'scope-a'); // cache hit
  assert.equal(calls.length, 1, 'same subject+scope is served from the merged cache');

  await scheduler.evaluate(policy, 'alice', 'scope-admin'); // different permission scope
  assert.equal(calls.length, 2, 'a different scope is isolated and fetches separately');
  assert.equal(calls[1].scope, 'scope-admin');

  await scheduler.evaluate(policy, 'bob', 'scope-a'); // different subject
  assert.equal(calls.length, 3);
});

test('an evaluation cancelled before the first flush never fetches anything', async () => {
  const calls: string[][] = [];
  const source: AttrSource = {
    async fetch(_s, attrs): Promise<FetchResult> {
      calls.push([...attrs]);
      return { version: 1, values: {} };
    },
  };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), { costs: { a: 1, b: 1 } });
  const ac = new AbortController();
  const p = scheduler.evaluate(and(eq('a', 1), eq('b', 1)), 'alice', 'scope-a', { signal: ac.signal });
  ac.abort();
  await assert.rejects(p, CancelledError);
  await tick();
  assert.equal(calls.length, 0, 'cancelled evaluation must not fetch irrelevant attributes');
});
