import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { decodeLogEntryView, encodeLogEntryPayload, ENTRY_VALUE, makeEntry } from '../src/codec/logEntry.ts';
import { encodeFrame } from '../src/codec/frame.ts';
import { Cluster } from '../src/paxos/cluster.ts';
import { MappedSegment } from '../src/storage/mappedSegment.ts';
import { resources } from '../src/storage/resources.ts';

async function withCluster<T>(ids: number[], fn: (c: Cluster, dir: string) => Promise<T>): Promise<T> {
  const dir = await mkdtemp(join(tmpdir(), 'conc-'));
  const cluster = await Cluster.start(ids, { rootDir: dir, leaseMs: 60_000 });
  try {
    return await fn(cluster, dir);
  } finally {
    await cluster.close();
    await rm(dir, { recursive: true, force: true });
    resources.assertDrained();
  }
}

test('64 concurrent proposals get unique contiguous indexes and all commit', () =>
  withCluster([1, 2, 3], async (c) => {
    const N = 64;
    const results = await Promise.all(
      Array.from({ length: N }, (_, i) => c.node(1).proposer.propose(Buffer.from(`cmd-${i}`))),
    );
    // Indexes are handed out under the proposer's serialization chain.
    assert.deepEqual([...results].sort((a, b) => a - b), Array.from({ length: N }, (_, i) => i + 1));
    assert.equal(c.node(1).commitIndex, N);

    // No holes in the committed prefix on any node.
    for (const id of [1, 2, 3]) {
      const log = c.node(id).committedLog();
      assert.equal(log.length, N);
      for (let i = 0; i < N; i++) assert.equal(log[i].index, i + 1);
    }
    // Every committed value is one of the proposed commands, exactly once.
    const values = c
      .node(1)
      .committedLog()
      .map((e) => e.value.toString())
      .sort();
    assert.deepEqual(values, Array.from({ length: N }, (_, i) => `cmd-${i}`).sort());
    assert.equal(c.network.inFlight, 0, 'network leak');
    assert.equal(c.node(1).proposer.inFlight, 0, 'proposer leak');
  }));

test('concurrent proposals from two would-be leaders converge to one log', () =>
  withCluster([1, 2, 3, 4, 5], async (c) => {
    // Both nodes race; the serialization chain + ballot ordering decide a
    // winner per slot, but the committed log must stay consistent.
    const a = Array.from({ length: 10 }, (_, i) => c.node(1).proposer.propose(Buffer.from(`A${i}`)));
    const b = Array.from({ length: 10 }, (_, i) => c.node(2).proposer.propose(Buffer.from(`B${i}`)));
    const settled = await Promise.allSettled([...a, ...b]);
    const ok = settled.filter((r) => r.status === 'fulfilled').length;
    assert.ok(ok >= 10, `at least one proposer must make progress, got ${ok}`);

    // Let the current leader drain anything pending, then verify agreement.
    const leader = c.leader();
    assert.ok(leader, 'a leader must exist');
    const { summary } = c.assertConverged();
    const committed = c.node(leader.id).committedLog();
    for (let i = 0; i < committed.length; i++) assert.equal(committed[i].index, i + 1, 'no holes');
    assert.ok(summary.length > 0);
  }));

test('concurrent zero-copy decodes from one shared mapping stay independent', async () => {
  const entries = Array.from({ length: 500 }, (_, i) =>
    makeEntry(i + 1, 1, 1, ENTRY_VALUE, Buffer.from(`value-${i}`)),
  );
  const segment = Buffer.concat(entries.map((e) => encodeFrame(encodeLogEntryPayload(e))));
  const seg = MappedSegment.fromBuffer(segment);

  const decoded = await Promise.all(
    [...seg.scan()].map(async (frame, i) => {
      // Interleave microtasks to simulate concurrent parsing.
      await Promise.resolve();
      const e = decodeLogEntryView(frame.payload);
      assert.equal(e.value.buffer, seg.buffer.buffer, 'zero-copy view into the shared mapping');
      return { i, text: e.value.toString(), index: e.index };
    }),
  );
  for (const { i, text, index } of decoded) {
    assert.equal(text, `value-${i}`);
    assert.equal(index, i + 1);
  }
  seg.close();
  assert.equal(resources.outstanding('segment'), 0, 'segment leak');
});

test('store write queue drains to zero under concurrent load (no pending-write leak)', () =>
  withCluster([1, 2, 3], async (c) => {
    await Promise.all(Array.from({ length: 50 }, (_, i) => c.node(1).proposer.propose(Buffer.from(`w${i}`))));
    for (const id of [1, 2, 3]) {
      assert.equal(c.node(id).store.pendingWrites, 0, `node ${id} pending writes`);
    }
    assert.equal(c.network.inFlight, 0);
  }));
