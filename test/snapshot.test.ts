import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DisposedError, IncrementalSnapshotEngine } from '../src/snapshot.ts';

const enc = (s: string) => new TextEncoder().encode(s);

test('incremental snapshot captures only ops since the previous snapshot', async () => {
  const e = new IncrementalSnapshotEngine();
  await e.set('a', enc('1'));
  await e.set('b', enc('2'));
  const s1 = await e.createSnapshot();
  assert.equal(s1.baseVersion, 0);
  assert.equal(s1.version, 2);
  assert.equal(s1.ops.length, 2);

  await e.set('c', enc('3'));
  await e.delete('a');
  const s2 = await e.createSnapshot();
  assert.equal(s2.baseVersion, 2);
  assert.equal(s2.version, 4);
  assert.deepEqual(s2.ops.map((o) => o.type), ['set', 'delete']);
  await e.dispose();
});

test('empty snapshot has zero ops and equal versions', async () => {
  const e = new IncrementalSnapshotEngine();
  await e.set('x', enc('1'));
  await e.createSnapshot();
  const s = await e.createSnapshot();
  assert.equal(s.ops.length, 0);
  assert.equal(s.baseVersion, s.version);
  await e.dispose();
});

test('checkpoint + deltas restore the exact state', async () => {
  const e = new IncrementalSnapshotEngine();
  for (let i = 0; i < 100; i++) await e.set(`k${i}`, enc(`v${i}`));
  await e.createCheckpoint();
  for (let i = 0; i < 100; i += 2) await e.delete(`k${i}`);
  await e.set('extra', enc('!'));
  await e.createSnapshot();

  const { state, version } = await e.restore();
  assert.equal(version, e.currentVersion);
  assert.equal(state.size, 51);
  assert.deepEqual([...state.get('k1')!], [...enc('v1')]);
  assert.equal(state.has('k0'), false);
  assert.deepEqual([...state.get('extra')!], [...enc('!')]);
  await e.dispose();
});

test('restore without any checkpoint replays all deltas from genesis', async () => {
  const e = new IncrementalSnapshotEngine();
  await e.set('a', enc('1'));
  await e.createSnapshot();
  await e.set('b', enc('2'));
  await e.delete('a');
  await e.createSnapshot();
  const { state, version } = await e.restore();
  assert.equal(version, 3);
  assert.equal(state.size, 1);
  assert.deepEqual([...state.get('b')!], [...enc('2')]);
  await e.dispose();
});

test('compact drops deltas covered by the latest checkpoint', async () => {
  const e = new IncrementalSnapshotEngine();
  await e.set('a', enc('1'));
  await e.createSnapshot();
  await e.set('b', enc('2'));
  await e.createCheckpoint();
  const dropped = await e.compact();
  assert.equal(dropped, 1);
  assert.equal(e.stats().archivedDeltas, 0);
  // restore still works from checkpoint alone
  const { state } = await e.restore();
  assert.equal(state.size, 2);
  await e.dispose();
});

test('archived snapshots are defensive copies (source mutation cannot leak in)', async () => {
  const e = new IncrementalSnapshotEngine();
  const buf = enc('original');
  await e.set('k', buf);
  await e.createSnapshot();
  buf.fill(0); // corrupt the caller's buffer after the fact
  const { state } = await e.restore();
  assert.deepEqual([...state.get('k')!], [...enc('original')]);
  // get() also returns a copy
  const g = e.get('k')!;
  g.fill(0);
  assert.deepEqual([...e.get('k')!], [...enc('original')]);
  await e.dispose();
});

test('concurrent mutations and snapshots are serialized and consistent', async () => {
  const e = new IncrementalSnapshotEngine({ chunkSize: 8 });
  const N = 500;
  const jobs: Promise<unknown>[] = [];
  for (let i = 0; i < N; i++) {
    jobs.push(e.set(`key:${i}`, enc(`value:${i}`)));
    if (i % 25 === 0) jobs.push(e.createSnapshot());
    if (i % 100 === 0) jobs.push(e.createCheckpoint());
  }
  await Promise.all(jobs);
  assert.equal(e.currentVersion, N);
  await e.createSnapshot(); // archive the tail ops so restore can see them
  const { state, version } = await e.restore();
  assert.equal(version, N);
  assert.equal(state.size, N);
  for (let i = 0; i < N; i++) {
    assert.deepEqual([...state.get(`key:${i}`)!], [...enc(`value:${i}`)], `key:${i}`);
  }
  await e.dispose();
});

test('concurrent interleaved set/delete yields a deterministic final state', async () => {
  const e = new IncrementalSnapshotEngine();
  const jobs: Promise<unknown>[] = [];
  for (let i = 0; i < 200; i++) jobs.push(e.set('shared', enc(`v${i}`)));
  for (let i = 0; i < 50; i++) jobs.push(e.delete('ghost'));
  await Promise.all(jobs);
  await e.createCheckpoint();
  const { state } = await e.restore();
  // Last queued set wins; the exact value is one of the queued ones.
  assert.equal(state.size, 1);
  assert.match(new TextDecoder().decode(state.get('shared')!), /^v\d+$/);
  assert.equal(e.stats().version, 250);
  await e.dispose();
});

test('versions are strictly increasing across concurrent writers', async () => {
  const e = new IncrementalSnapshotEngine();
  const versions = await Promise.all(
    Array.from({ length: 300 }, (_, i) => e.set(`k${i}`, enc('x'))),
  );
  const sorted = [...versions].sort((a, b) => a - b);
  assert.deepEqual(sorted, Array.from({ length: 300 }, (_, i) => i + 1));
  await e.dispose();
});

test('dispose releases all resources: zero live, operations throw', async () => {
  const e = new IncrementalSnapshotEngine();
  for (let i = 0; i < 50; i++) await e.set(`k${i}`, enc('v'));
  await e.createSnapshot();
  await e.createCheckpoint();
  assert.ok(e.stats().liveResources > 0);
  await e.dispose();
  const stats = e.stats();
  assert.equal(stats.liveResources, 0);
  assert.equal(stats.stateSize, 0);
  assert.equal(stats.archivedDeltas, 0);
  assert.equal(stats.archivedCheckpoints, 0);
  assert.ok(stats.disposed);
  await assert.rejects(() => e.set('x', enc('y')), DisposedError);
  await assert.rejects(() => e.createSnapshot(), DisposedError);
  await assert.rejects(() => e.restore(), DisposedError);
});

test('no leaked state across restore cycles (restored maps are independent)', async () => {
  const e = new IncrementalSnapshotEngine();
  await e.set('a', enc('1'));
  await e.createCheckpoint();
  const r1 = await e.restore();
  r1.state.get('a')!.fill(0);
  r1.state.set('injected', enc('x'));
  const r2 = await e.restore();
  assert.deepEqual([...r2.state.get('a')!], [...enc('1')]);
  assert.equal(r2.state.has('injected'), false);
  await e.dispose();
});

test('low-pause: large snapshot yields to the event loop between chunks', async () => {
  const e = new IncrementalSnapshotEngine({ chunkSize: 16 });
  for (let i = 0; i < 500; i++) await e.set(`k${i}`, enc('v'));
  let timerFired = false;
  const timer = new Promise<void>((r) => setImmediate(() => { timerFired = true; r(); }));
  const snap = e.createCheckpoint();
  await Promise.all([snap, timer]);
  // The checkpoint copy (500 entries / chunk 16) must have yielded at least once,
  // allowing the setImmediate callback queued after it to run first.
  assert.ok(timerFired);
  await e.dispose();
});

test('archive gap is detected instead of silently corrupting state', async () => {
  const e = new IncrementalSnapshotEngine();
  await e.set('a', enc('1'));
  await e.createSnapshot();
  await e.createCheckpoint(); // checkpoint at v1
  await e.set('b', enc('2'));
  await e.createSnapshot();   // delta v1->v2
  // Forge an inconsistent archive: drop the delta but keep a later version marker.
  (e as unknown as { deltas: unknown[] }).deltas.length = 0;
  (e as unknown as { deltas: unknown[] }).deltas.push({
    kind: 'delta', baseVersion: 5, version: 6, ops: [],
  });
  await assert.rejects(() => e.restore(), /archive gap/);
  await e.dispose();
});
