import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { Store, persist } from '../src/store.ts';

describe('reactive core', () => {
  it('propagates signal changes through computed chains into effects', () => {
    const store = new Store();
    const count = store.signal('count', 1);
    const doubled = store.computed(() => count.get() * 2);
    const quadrupled = store.computed(() => doubled.get() * 2);

    const seen: number[] = [];
    store.effect(() => seen.push(quadrupled.get()));

    assert.deepEqual(seen, [4]);
    count.set(2);
    count.set(3);
    assert.deepEqual(seen, [4, 8, 12]);
  });
});

describe('checkpoint', () => {
  it('serializes only @persist-annotated signals', () => {
    const store = new Store();
    const count = persist(store, 'count', 0);
    const user = store.persisted('user', { name: 'ada', tags: ['admin'] });
    const sessionId = store.signal('sessionId', 'tmp-123');
    const draft = store.signal('draft', 'unsaved');

    count.set(41);
    user.set({ name: 'grace', tags: ['dev', 'ops'] });
    sessionId.set('tmp-999');
    draft.set('still unsaved');

    const json = store.checkpointToJSON();
    const parsed = JSON.parse(json);

    assert.equal(parsed.version, 1);
    assert.ok(parsed.createdAt);
    assert.deepEqual(Object.keys(parsed.entries).sort(), ['count', 'user']);
    assert.deepEqual(parsed.entries.count, 41);
    assert.deepEqual(parsed.entries.user, { name: 'grace', tags: ['dev', 'ops'] });
    assert.ok(!('sessionId' in parsed.entries));
    assert.ok(!('draft' in parsed.entries));
  });
});

describe('hydration after process restart', () => {
  function buildStore() {
    const store = new Store();
    const count = persist(store, 'count', 0);
    const user = store.persisted('user', { name: 'anonymous' });
    const sessionId = store.signal('sessionId', 'fresh-session');
    const doubled = store.computed(() => count.get() * 2);
    const greeting = store.computed(() => `hello ${user.get().name}`);

    const effectLog: string[] = [];
    store.effect(() => {
      effectLog.push(`count=${count.get()} doubled=${doubled.get()} ${greeting.get()}`);
    });
    return { store, count, user, sessionId, doubled, greeting, effectLog };
  }

  it('restores persisted state, resets transient state, re-aligns effects', () => {
    // --- "process 1": mutate state, write a checkpoint ---
    const first = buildStore();
    first.count.set(21);
    first.user.set({ name: 'ada' });
    first.sessionId.set('live-session-abc');
    const checkpoint = JSON.parse(first.store.checkpointToJSON());
    assert.equal(first.doubled.get(), 42);

    // --- "process 2": fresh store (restart), hydrate from checkpoint ---
    const second = buildStore();
    assert.equal(second.effectLog.length, 1); // initial effect run only

    const report = second.store.hydrate(checkpoint);

    // Persisted state perfectly restored.
    assert.equal(second.count.get(), 21);
    assert.deepEqual(second.user.get(), { name: 'ada' });
    // Derived chain re-evaluated from restored values.
    assert.equal(second.doubled.get(), 42);
    assert.equal(second.greeting.get(), 'hello ada');
    // Transient state safely reset, never touched by the checkpoint.
    assert.equal(second.sessionId.get(), 'fresh-session');
    // Effects re-fired during hydration (once per applied entry).
    assert.equal(report.applied.length, 2);
    assert.ok(report.effectsReAligned >= 2);
    assert.deepEqual(report.resetTransient, ['sessionId']);
    assert.equal(
      second.effectLog.at(-1),
      'count=21 doubled=42 hello ada',
    );
  });

  it('skips unknown checkpoint entries without failing', () => {
    const first = buildStore();
    first.count.set(7);
    const checkpoint = JSON.parse(first.store.checkpointToJSON());
    checkpoint.entries.ghost = 'not-a-real-signal';
    checkpoint.entries.sessionId = 'malicious-override';

    const second = buildStore();
    const report = second.store.hydrate(checkpoint);

    assert.deepEqual(report.applied.sort(), ['count', 'user']);
    assert.deepEqual(report.skipped.sort(), ['ghost', 'sessionId']);
    assert.equal(second.count.get(), 7);
    // Transient signals cannot be overridden by a checkpoint.
    assert.equal(second.sessionId.get(), 'fresh-session');
  });

  it('round-trips through a checkpoint file on disk', () => {
    const dir = mkdtempSync(join(tmpdir(), 'persist-store-'));
    const file = join(dir, 'checkpoint.json');
    try {
      const first = buildStore();
      first.count.set(99);
      first.user.set({ name: 'grace' });
      first.store.saveCheckpoint(file);

      const onDisk = JSON.parse(readFileSync(file, 'utf8'));
      assert.deepEqual(Object.keys(onDisk.entries).sort(), ['count', 'user']);

      const second = buildStore();
      const report = second.store.hydrateFromFile(file);
      assert.equal(second.count.get(), 99);
      assert.equal(second.greeting.get(), 'hello grace');
      assert.deepEqual(report.applied.sort(), ['count', 'user']);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('rejects checkpoints with an unsupported version', () => {
    const second = buildStore();
    assert.throws(
      () => second.store.hydrate({ version: 999, createdAt: '', entries: {} }),
      /Unsupported checkpoint version/,
    );
  });
});
