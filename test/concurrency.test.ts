import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ShardingEngine } from '../src/sharding/engine.ts';
import type { MigrationPlan } from '../src/sharding/engine.ts';
import { AsyncMutex } from '../src/sharding/mutex.ts';
import { Topology } from '../src/sharding/topology.ts';

test('async mutex serializes critical sections in FIFO order', async () => {
  const mutex = new AsyncMutex();
  let active = 0;
  let maxActive = 0;
  const order: number[] = [];
  const tasks = Array.from({ length: 50 }, (_, i) =>
    mutex.runExclusive(async () => {
      active++;
      maxActive = Math.max(maxActive, active);
      await new Promise((resolve) => setImmediate(resolve));
      order.push(i);
      active--;
    }),
  );
  await Promise.all(tasks);
  assert.equal(maxActive, 1, 'critical sections never overlap');
  assert.deepEqual(
    order,
    Array.from({ length: 50 }, (_, i) => i),
    'FIFO acquisition order',
  );
  assert.equal(mutex.pending, 0, 'mutex fully drained');
});

test('async mutex propagates errors without deadlocking', async () => {
  const mutex = new AsyncMutex();
  await assert.rejects(mutex.runExclusive(() => Promise.reject(new Error('boom'))), /boom/);
  const result = await mutex.runExclusive(() => 42);
  assert.equal(result, 42, 'mutex still usable after a rejection');
  assert.equal(mutex.pending, 0);
});

test('concurrent rebalances and reads: no lost updates, consistent snapshots', async () => {
  const topology = new Topology();
  for (let i = 0; i < 6; i++) {
    topology.addNode({ id: `base-${i}`, region: i % 2 === 0 ? 'east' : 'west', zone: `z${i % 3}` });
  }
  const engine = new ShardingEngine(topology, { vshardCount: 256, replicationFactor: 3 });
  const versions: number[] = [];
  engine.on('rebalanced', (plan: MigrationPlan) => versions.push(plan.toVersion));

  // 30 serialized topology mutations racing with 600 route lookups.
  const mutations: Promise<unknown>[] = [];
  for (let i = 0; i < 15; i++) {
    mutations.push(engine.addNode({ id: `extra-${i}`, region: 'east', zone: 'ze' }));
  }
  const reads: Promise<void>[] = [];
  for (let i = 0; i < 600; i++) {
    reads.push(
      (async () => {
        const route = engine.router.routeRead(`hot-${i % 97}`, 'base-0');
        const map = engine.getShardMap();
        assert.ok(map.replicasOf(route.key).includes(route.nodeId), 'route matches a snapshot');
      })(),
    );
  }
  await Promise.all([...mutations, ...reads]);
  for (let i = 0; i < 15; i++) {
    mutations.push(engine.removeNode(`extra-${i}`));
  }
  await Promise.all(mutations);

  assert.equal(engine.pendingUpdates, 0, 'update queue drained');
  // 15 adds + 15 removes + initial build (version 0) => 30 rebuilds.
  assert.equal(engine.version, 30, 'no lost updates under concurrency');
  assert.deepEqual(
    versions,
    Array.from({ length: 30 }, (_, i) => i + 1),
    'versions increase strictly by one',
  );

  // Final map is fully consistent with the final topology.
  const map = engine.getShardMap();
  const live = new Set(topology.list().map((n) => n.id));
  for (let v = 0; v < map.vshardCount; v++) {
    const replicas = map.replicasOfVshard(v);
    assert.equal(replicas.length, 3);
    assert.equal(new Set(replicas).size, 3);
    for (const id of replicas) assert.ok(live.has(id), `replica ${id} is live`);
  }

  engine.dispose();
  topology.dispose();
});

test('failed mutations do not corrupt the shard map or the version chain', async () => {
  const topology = new Topology();
  topology.addNode({ id: 'a', region: 'r', zone: 'z' });
  topology.addNode({ id: 'b', region: 'r', zone: 'z' });
  const engine = new ShardingEngine(topology, { vshardCount: 64, replicationFactor: 2 });

  await assert.rejects(engine.addNode({ id: 'a', region: 'r', zone: 'z' }), /duplicate/);
  await assert.rejects(engine.removeNode('ghost'), /unknown node/);
  assert.equal(engine.version, 0, 'failed updates never bump the version');
  assert.equal(engine.pendingUpdates, 0);

  const plan = await engine.addNode({ id: 'c', region: 'r', zone: 'z' });
  assert.equal(plan.toVersion, 1, 'engine recovers after rejected updates');
  engine.dispose();
  topology.dispose();
});

test('dispose leaves strictly no leaks: listeners, queues, stats, state', async () => {
  const topology = new Topology();
  for (let i = 0; i < 4; i++) topology.addNode({ id: `n${i}`, region: 'r', zone: 'z' });
  const engine = new ShardingEngine(topology, { vshardCount: 64, replicationFactor: 2 });
  engine.on('rebalanced', () => {});
  engine.on('error', () => {});
  await engine.addNode({ id: 'n4', region: 'r', zone: 'z' });
  for (let i = 0; i < 100; i++) engine.router.routeRead(`k${i}`, 'n0');

  assert.equal(engine.pendingUpdates, 0);
  engine.dispose();
  topology.dispose();

  assert.equal(engine.listenerCount('rebalanced'), 0, 'no dangling engine listeners');
  assert.equal(engine.listenerCount('error'), 0);
  assert.equal(topology.listenerCount('change'), 0, 'engine unsubscribed from topology');
  assert.equal(topology.size, 0, 'topology state cleared');
  assert.deepEqual(engine.router.stats, { localReads: 0, remoteReads: 0 }, 'stats reset');
  await assert.rejects(engine.addNode({ id: 'x', region: 'r', zone: 'z' }), /disposed/);
});

test('topology change events trigger serialized auto-rebalance without leaks', async () => {
  const topology = new Topology();
  topology.addNode({ id: 'seed', region: 'r', zone: 'z' });
  const engine = new ShardingEngine(topology, { vshardCount: 32, replicationFactor: 1 });
  const plans: MigrationPlan[] = [];
  engine.on('rebalanced', (plan: MigrationPlan) => plans.push(plan));

  // Direct topology mutations bypass engine.addNode: the engine must still
  // observe them and rebalance exactly once per change.
  topology.addNode({ id: 'm1', region: 'r', zone: 'z' });
  topology.addNode({ id: 'm2', region: 'r', zone: 'z' });
  topology.removeNode('m1');
  await engine.rebalance(); // flush point: waits for the mutex queue to drain
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(engine.pendingUpdates, 0);
  const finalMap = engine.getShardMap();
  const live = new Set(topology.list().map((n) => n.id));
  for (let v = 0; v < finalMap.vshardCount; v++) {
    for (const id of finalMap.replicasOfVshard(v)) assert.ok(live.has(id));
  }
  assert.ok(plans.length >= 3, 'every topology change produced a rebalance');
  engine.dispose();
  topology.dispose();
});
