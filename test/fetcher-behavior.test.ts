import { test } from 'node:test';
import assert from 'node:assert/strict';
import { and, eq } from '../src/policy.ts';
import {
  AttributeScheduler,
  AttributeUnavailableError,
  FetchTimeoutError,
  ReentrantFetchError,
  VersionConflictError,
  type AttrSource,
  type FetchResult,
} from '../src/scheduler.ts';
import { MemoryEvidenceStore } from '../src/evidence.ts';

const tick = (): Promise<void> => new Promise((r) => setImmediate(r));

test('reentrant request for an in-flight attribute fails deterministically', async () => {
  let scheduler!: AttributeScheduler;
  const source: AttrSource = {
    async fetch(subject, attrs, scope): Promise<FetchResult> {
      if (attrs.includes('a')) {
        await scheduler.get(subject, scope, 'a'); // reentrant on the in-flight batch
      }
      return { version: 1, values: Object.fromEntries(attrs.map((x) => [x, 1])) };
    },
  };
  scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), { costs: { a: 1 } });
  await assert.rejects(scheduler.evaluate(eq('a', 1), 's', 'sc'), ReentrantFetchError);
});

test('reentrant request for an unrelated attribute is served normally', async () => {
  let scheduler!: AttributeScheduler;
  const calls: string[][] = [];
  const source: AttrSource = {
    async fetch(subject, attrs, scope): Promise<FetchResult> {
      calls.push([...attrs]);
      if (attrs.includes('a')) {
        const z = await scheduler.get(subject, scope, 'z'); // different attribute: allowed
        return { version: 1, values: { a: z.value + 1 } };
      }
      return { version: 1, values: { z: 5 } };
    },
  };
  scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), { costs: { a: 1, z: 1 } });
  const d = await scheduler.evaluate(eq('a', 6), 's', 'sc');
  assert.equal(d.allow, true);
  assert.deepEqual(calls, [['a'], ['z']]);
});

test('a fetch that never resolves fails with FetchTimeoutError', async () => {
  const source: AttrSource = { fetch: () => new Promise<FetchResult>(() => {}) };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), {
    costs: { a: 1 },
    fetchTimeoutMs: 25,
  });
  await assert.rejects(scheduler.evaluate(eq('a', 1), 's', 'sc'), (e: unknown) => {
    assert.ok(e instanceof FetchTimeoutError);
    assert.deepEqual((e as FetchTimeoutError).attrs, ['a']);
    return true;
  });
});

test('mixed-version reads are retried until the read set is version-consistent', async () => {
  let version = 1;
  const calls: string[][] = [];
  const source: AttrSource = {
    async fetch(_s, attrs): Promise<FetchResult> {
      calls.push([...attrs]);
      const v = version;
      if (attrs.includes('a')) version = 2; // version switches between the two reads
      return { version: v, values: Object.fromEntries(attrs.map((x) => [x, 1])) };
    },
  };
  const evidence = new MemoryEvidenceStore();
  const scheduler = new AttributeScheduler(source, evidence, { costs: { a: 1, b: 1 } });

  const d = await scheduler.evaluate(and(eq('a', 1), eq('b', 1)), 's', 'sc');

  assert.equal(d.allow, true);
  assert.deepEqual(calls, [['a'], ['b'], ['a']], 'stale attribute is re-fetched once');
  assert.deepEqual(
    [...new Set(d.accesses.map((x) => x.version))],
    [2],
    'committed evidence is version-consistent',
  );
  assert.equal(evidence.records.length, 1, 'only the final consistent decision is committed');
});

test('a version that keeps switching fails deterministically with VersionConflictError', async () => {
  let version = 0;
  const calls: string[][] = [];
  const source: AttrSource = {
    async fetch(_s, attrs): Promise<FetchResult> {
      calls.push([...attrs]);
      version += 1;
      return { version, values: Object.fromEntries(attrs.map((x) => [x, 1])) };
    },
  };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), {
    costs: { a: 1, b: 1 },
    maxVersionRetries: 2,
  });
  await assert.rejects(scheduler.evaluate(and(eq('a', 1), eq('b', 1)), 's', 'sc'), VersionConflictError);
  const aFetches = calls.filter((c) => c.includes('a')).length;
  const bFetches = calls.filter((c) => c.includes('b')).length;
  // Deterministic ping-pong: each retry re-reads only the attributes left stale
  // by the previous attempt, so a and b alternate; attempts = 1 + maxVersionRetries.
  assert.deepEqual([aFetches, bFetches], [2, 2]);
  assert.equal(calls.length, 4);
});

test('partial batch failure retries only the failed attributes, then succeeds', async () => {
  let bFailures = 1;
  const calls: string[][] = [];
  const source: AttrSource = {
    async fetch(_s, attrs): Promise<FetchResult> {
      calls.push([...attrs]);
      const values: Record<string, number | { error: string }> = {};
      for (const a of attrs) {
        if (a === 'b' && bFailures-- > 0) values[a] = { error: 'flaky' };
        else values[a] = 1;
      }
      return { version: 1, values };
    },
  };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), { costs: { a: 1, b: 1 } });
  // Two concurrent evaluations force a and b into one batch.
  const [d1, d2] = await Promise.all([
    scheduler.evaluate(eq('a', 1), 's', 'sc'),
    scheduler.evaluate(eq('b', 1), 's', 'sc'),
  ]);
  assert.equal(d1.allow, true);
  assert.equal(d2.allow, true);
  assert.deepEqual(calls, [['a', 'b'], ['b']], 'only the failed attribute is retried');
});

test('a permanently failing attribute yields AttributeUnavailableError without affecting others', async () => {
  const source: AttrSource = {
    async fetch(_s, attrs): Promise<FetchResult> {
      const values: Record<string, number | { error: string }> = {};
      for (const a of attrs) values[a] = a === 'b' ? { error: 'down' } : 1;
      return { version: 1, values };
    },
  };
  const scheduler = new AttributeScheduler(source, new MemoryEvidenceStore(), {
    costs: { a: 1, b: 1 },
    maxPartialRetries: 1,
  });
  const [r1, r2] = await Promise.allSettled([
    scheduler.evaluate(eq('a', 1), 's', 'sc'),
    scheduler.evaluate(eq('b', 1), 's', 'sc'),
  ]);
  assert.equal(r1.status, 'fulfilled');
  assert.equal(r2.status, 'rejected');
  assert.ok(r2.status === 'rejected' && r2.reason instanceof AttributeUnavailableError);
  assert.deepEqual(r2.status === 'rejected' ? (r2.reason as AttributeUnavailableError).attrs : [], ['b']);
  await tick();
});
