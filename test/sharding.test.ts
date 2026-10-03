import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ShardRing } from '../src/sharding/ring.ts';
import { LocalityAwareRouter, RoutingError } from '../src/sharding/router.ts';
import { fnv1a32 } from '../src/sharding/hash.ts';
import { localityCost, zoneKey, type Location, type NodeInfo } from '../src/sharding/topology.ts';

function makeNode(id: string, region: string, zone: string, capacity = 1): NodeInfo {
  return { id, region, zone, capacity };
}

function gridNodes(regions: string[], zones: string[], perZone: number): NodeInfo[] {
  const nodes: NodeInfo[] = [];
  for (const region of regions) {
    for (const zone of zones) {
      for (let i = 0; i < perZone; i++) {
        nodes.push(makeNode(`${region}-${zone}-n${i}`, region, zone));
      }
    }
  }
  return nodes;
}

async function buildRouter(nodes: NodeInfo[], shards = 256, replicas = 2): Promise<LocalityAwareRouter> {
  const router = new LocalityAwareRouter(shards, replicas);
  for (const node of nodes) await router.addNode(node);
  return router;
}

test('hash and shard assignment are deterministic across instances', async () => {
  assert.equal(fnv1a32('hello'), fnv1a32('hello'));
  assert.notEqual(fnv1a32('hello'), fnv1a32('hellp'));
  const nodes = gridNodes(['r1', 'r2'], ['z1', 'z2'], 2);
  const a = await buildRouter(nodes);
  const b = await buildRouter(nodes);
  const client: Location = { region: 'r1', zone: 'z1' };
  for (let i = 0; i < 200; i++) {
    const key = `key-${i}`;
    assert.equal(a.route(key, client).nodeId, b.route(key, client).nodeId);
  }
});

test('ring validates constructor arguments', () => {
  assert.throws(() => new ShardRing(0), RangeError);
  assert.throws(() => new ShardRing(16, 0), RangeError);
  assert.throws(() => new ShardRing(2.5), RangeError);
});

test('empty topology and missing nodes raise RoutingError', async () => {
  const router = new LocalityAwareRouter(64, 2);
  assert.throws(() => router.route('k', { region: 'r1', zone: 'z1' }), RoutingError);
  await assert.rejects(router.removeNode('ghost'), RoutingError);
  await router.addNode(makeNode('n1', 'r1', 'z1'));
  await assert.rejects(router.addNode(makeNode('n1', 'r1', 'z1')), RoutingError);
  assert.throws(() => router.setNodeHealth('ghost', false), RoutingError);
});

test('single node owns every shard and every key', async () => {
  const router = await buildRouter([makeNode('solo', 'r1', 'z1')], 128, 3);
  for (let i = 0; i < 500; i++) {
    assert.equal(router.route(`k${i}`, { region: 'r9', zone: 'z9' }).nodeId, 'solo');
  }
  const ring = new ShardRing(128, 3);
  ring.rebuild([makeNode('solo', 'r1', 'z1')]);
  for (let shard = 0; shard < 128; shard++) {
    assert.deepEqual(ring.holdersOf(shard), ['solo']);
  }
});

test('virtual shards distribute keys near-uniformly', () => {
  const ring = new ShardRing(256, 2);
  const counts = new Array(256).fill(0);
  const total = 20000;
  for (let i = 0; i < total; i++) counts[ring.shardOf(`key-${i}`)]++;
  const mean = total / 256;
  for (const count of counts) {
    assert.ok(count > 0, 'no empty shard at this volume');
    assert.ok(count < mean * 3, `shard count ${count} deviates too far from mean ${mean}`);
  }
});

test('replica placement is hierarchy-aware: region first, then zone', () => {
  const nodes = gridNodes(['r1', 'r2'], ['z1', 'z2'], 2);
  const ring2 = new ShardRing(512, 2);
  ring2.rebuild(nodes);
  const byId = new Map(nodes.map((n) => [n.id, n]));
  for (let shard = 0; shard < 512; shard++) {
    const holders = ring2.holdersOf(shard).map((id) => byId.get(id) as NodeInfo);
    assert.equal(holders.length, 2);
    assert.notEqual(holders[0].region, holders[1].region, 'replicas span regions');
  }
  const ring4 = new ShardRing(512, 4);
  ring4.rebuild(nodes);
  for (let shard = 0; shard < 512; shard++) {
    const zones = new Set(ring4.holdersOf(shard).map((id) => zoneKey(byId.get(id) as NodeInfo)));
    assert.equal(zones.size, 4, 'replicas cover every zone when replicaCount == zone count');
  }
});

test('locality-aware routing always picks a zone-local replica when one exists', async () => {
  const nodes = gridNodes(['r1', 'r2'], ['z1', 'z2'], 2);
  const router = await buildRouter(nodes, 512, 4);
  for (const region of ['r1', 'r2']) {
    for (const zone of ['z1', 'z2']) {
      const client: Location = { region, zone };
      for (let i = 0; i < 300; i++) {
        const decision = router.route(`k${i}`, client);
        assert.equal(decision.locality, 'zone');
        assert.equal(decision.cost, 1);
      }
    }
  }
  const stats = router.stats();
  assert.equal(stats.requests, 1200);
  assert.equal(stats.zoneLocal, 1200);
  assert.equal(stats.regionLocal + stats.remote, 0);
});

test('failover: down replica is skipped, recovery restores the cheapest route', async () => {
  const nodes = gridNodes(['r1', 'r2'], ['z1', 'z2'], 2);
  const router = await buildRouter(nodes, 512, 4);
  const client: Location = { region: 'r1', zone: 'z1' };
  const before = router.route('hot-key', client);
  router.setNodeHealth(before.nodeId, false);
  const degraded = router.route('hot-key', client);
  assert.notEqual(degraded.nodeId, before.nodeId);
  assert.ok(degraded.cost > before.cost, 'failover target is further away');
  assert.equal(degraded.locality, 'region');
  router.setNodeHealth(before.nodeId, true);
  const restored = router.route('hot-key', client);
  assert.deepEqual(restored, before);
});

test('all replicas down makes routing fail fast', async () => {
  const router = await buildRouter([makeNode('only', 'r1', 'z1')], 64, 1);
  router.setNodeHealth('only', false);
  assert.throws(() => router.route('k', { region: 'r1', zone: 'z1' }), RoutingError);
});

test('node add/remove moves a minimal, bounded share of shard owners', async () => {
  const nodes = gridNodes(['r1', 'r2', 'r3'], ['z1', 'z2'], 2);
  const router = await buildRouter(nodes, 1024, 2);
  const removed = await router.removeNode('r3-z2-n0');
  assert.ok(removed.moved > 0 && removed.moved <= 1024 / 6, `moved ${removed.moved} exceeds minimal bound`);
  const added = await router.addNode(makeNode('r3-z2-n9', 'r3', 'z2'));
  assert.ok(added.moved > 0 && added.moved <= 1024 / 6, `moved ${added.moved} exceeds minimal bound`);
  assert.equal(router.stats().nodes, 12);
  assert.equal(router.version, 14, '12 adds + 1 remove + 1 add');
});

test('batch routing groups keys per node and loses/duplicates nothing', async () => {
  const nodes = gridNodes(['r1', 'r2'], ['z1', 'z2'], 2);
  const router = await buildRouter(nodes, 512, 4);
  const client: Location = { region: 'r1', zone: 'z1' };
  const keys = Array.from({ length: 1000 }, (_, i) => `batch-${i}`);
  const plan = router.routeBatch(keys, client);
  assert.ok(plan.nodeHops <= 2, `zone-local routing needs <=2 nodes, got ${plan.nodeHops}`);
  assert.equal(plan.crossZoneKeys, 0);
  assert.equal(plan.crossRegionKeys, 0);
  const routed = [...plan.batches.values()].flat();
  assert.equal(routed.length, keys.length);
  assert.deepEqual([...routed].sort(), [...keys].sort());
  for (const [nodeId, bucket] of plan.batches) {
    for (const key of bucket) {
      assert.equal(router.route(key, client).nodeId, nodeId, 'batch agrees with point routing');
    }
  }
});

test('concurrent routing during rebalancing is safe and leak-free', async () => {
  const nodes = gridNodes(['r1', 'r2'], ['z1', 'z2', 'z3'], 1);
  const router = await buildRouter(nodes, 128, 3);
  const client: Location = { region: 'r1', zone: 'z1' };
  const versionBefore = router.version;
  const tasks: Promise<unknown>[] = [];
  for (let i = 0; i < 300; i++) {
    tasks.push(
      router.withRouted(`key-${i % 50}`, client, async (decision) => {
        await new Promise((resolve) => setImmediate(resolve));
        assert.ok(decision.nodeId.length > 0);
      }),
    );
  }
  for (let i = 0; i < 10; i++) {
    const id = `extra-${i}`;
    tasks.push(
      router
        .addNode(makeNode(id, 'r3', 'z1'))
        .then(() => router.removeNode(id)),
    );
  }
  await Promise.all(tasks);
  await router.drain();
  const stats = router.stats();
  assert.equal(stats.inFlight, 0, 'no leaked in-flight operations');
  assert.equal(stats.pendingMutations, 0, 'mutex fully released');
  assert.equal(stats.pendingDrainWaiters, 0, 'no leaked drain waiters');
  assert.equal(stats.requests, 300);
  assert.equal(stats.nodes, 6);
  assert.equal(router.version, versionBefore + 20, 'every mutation advanced the routing version exactly once');
});

test('withRouted propagates task errors without leaking in-flight count', async () => {
  const router = await buildRouter([makeNode('n1', 'r1', 'z1')], 64, 1);
  await assert.rejects(
    router.withRouted('k', { region: 'r1', zone: 'z1' }, () => {
      throw new Error('boom');
    }),
    /boom/,
  );
  assert.equal(router.inFlightCount, 0);
  await router.drain();
});

test('locality cost model matches the hierarchy', () => {
  const node = makeNode('n', 'r1', 'z1');
  assert.equal(localityCost({ region: 'r1', zone: 'z1' }, node), 1);
  assert.equal(localityCost({ region: 'r1', zone: 'z2' }, node), 5);
  assert.equal(localityCost({ region: 'r2', zone: 'z1' }, node), 20);
});
