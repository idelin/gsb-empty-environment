import assert from 'node:assert/strict';
import { test, sleep, waitFor } from './runner.js';
import { EventStore } from '../src/event-store.js';
import { ResumeServer } from '../src/server.js';
import { ResumeClient } from '../src/client.js';
import { Simulator } from '../src/simulator.js';

interface Setup {
  store: EventStore;
  server: ResumeServer;
  sim: Simulator;
}

function setup(options?: { snapshotChunkDelayMs?: number; snapshotChunkSize?: number; seed?: number }): Setup {
  const store = new EventStore();
  const server = new ResumeServer(store, {
    secret: 'test-secret',
    snapshotChunkDelayMs: options?.snapshotChunkDelayMs ?? 0,
    snapshotChunkSize: options?.snapshotChunkSize ?? 4,
  });
  const sim = new Simulator(store, options?.seed ?? 42);
  return { store, server, sim };
}

test('resume within retained log replays only the missed events', () => {
  const { server, sim } = setup();
  sim.run(10);
  const client = new ResumeClient(server, {});
  client.connect();
  sim.run(5);
  assert.equal(client.applied.length, 15);
  client.close();
  sim.run(7);
  client.connect();
  assert.equal(client.snapshotCount, 0);
  assert.equal(client.applied.length, 22);
  assert.deepEqual(client.anomalies, []);
  assert.deepEqual(client.duplicates, []);
  assert.deepEqual(client.state, server.currentState());
});

test('compacted cursor recovers via snapshot plus incremental start point', async () => {
  const { store, server, sim } = setup();
  sim.run(40);
  const client = new ResumeClient(server, {});
  client.connect();
  assert.equal(client.applied.length, 40);
  client.close();
  sim.run(30);
  store.compact(51);
  assert.equal(store.oldest(), 51);
  client.connect();
  await waitFor(() => client.snapshotCount === 1);
  assert.equal(client.lastSnapshotBaseSeq, 70);
  sim.run(5);
  assert.equal(client.applied.length, 45);
  assert.deepEqual(client.anomalies, []);
  assert.deepEqual(client.duplicates, []);
  assert.deepEqual(client.state, server.currentState());
});

test('events produced during snapshot generation are not lost at the switch point', async () => {
  const { store, server, sim } = setup({ snapshotChunkDelayMs: 10, snapshotChunkSize: 1 });
  sim.run(60);
  store.compact(55);
  const client = new ResumeClient(server, {});
  client.connect();
  const headAtConnect = store.head();
  await sleep(3);
  sim.run(10);
  await waitFor(() => client.snapshotCount === 1 && store.head() === 70 && client.applied.length === 10);
  assert.equal(client.lastSnapshotBaseSeq, headAtConnect);
  const expected = store.readFrom(headAtConnect, {});
  assert.equal(expected.length, 10);
  assert.deepEqual(client.applied, expected.map((e) => e.id));
  sim.run(5);
  assert.equal(client.applied.length, 15);
  assert.deepEqual(client.anomalies, []);
  assert.deepEqual(client.duplicates, []);
  assert.deepEqual(client.state, server.currentState());
});

test('compaction during snapshot generation cannot drop events pinned for replay', async () => {
  const { store, server, sim } = setup({ snapshotChunkDelayMs: 10, snapshotChunkSize: 1 });
  sim.run(30);
  store.compact(25);
  const client = new ResumeClient(server, {});
  client.connect();
  const headAtConnect = store.head();
  sim.run(10);
  store.compact(1000);
  assert.equal(store.oldest(), headAtConnect + 1);
  await waitFor(() => client.snapshotCount === 1 && client.applied.length === 10);
  assert.deepEqual(client.applied, store.readFrom(headAtConnect, {}).map((e) => e.id));
  assert.deepEqual(client.state, server.currentState());
  await waitFor(() => store.compact(1000) >= 0 && store.oldest() === 41);
});

test('events replayed from a stale cursor are identifiable as duplicates', () => {
  const { server, sim } = setup();
  sim.run(10);
  const client = new ResumeClient(server, {});
  client.connect();
  assert.equal(client.applied.length, 10);
  const staleCursor = client.cursors[4];
  client.connect(staleCursor);
  assert.equal(client.duplicates.length, 5);
  assert.deepEqual(client.duplicates.map((d) => d.eventId), ['evt-6', 'evt-7', 'evt-8', 'evt-9', 'evt-10']);
  assert.equal(client.applied.length, 10);
  assert.deepEqual(client.anomalies, []);
  assert.deepEqual(client.state, server.currentState());
});

test('filtered subscription keeps contiguous offsets across reconnect', () => {
  const { store, server, sim } = setup();
  sim.run(50);
  const filter = { kinds: ['trade'] };
  const client = new ResumeClient(server, filter);
  client.connect();
  client.close();
  sim.run(20);
  client.connect();
  assert.deepEqual(client.anomalies, []);
  assert.deepEqual(client.duplicates, []);
  const allTrades = store.readFrom(0, filter);
  assert.equal(client.applied.length, allTrades.length);
  assert.deepEqual(client.applied, allTrades.map((e) => e.id));
});

test('fresh subscribe on a compacted store starts from a snapshot', async () => {
  const { store, server, sim } = setup();
  sim.run(30);
  store.compact(20);
  const client = new ResumeClient(server, {});
  client.connect();
  await waitFor(() => client.snapshotCount === 1);
  assert.equal(client.lastSnapshotBaseSeq, 30);
  sim.run(4);
  assert.equal(client.applied.length, 4);
  assert.deepEqual(client.state, server.currentState());
});
