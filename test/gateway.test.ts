import assert from 'node:assert/strict';
import { test } from 'node:test';
import { HierarchicalGateway } from '../src/gateway.ts';

function buildGateway(): HierarchicalGateway {
  const gateway = new HierarchicalGateway();
  gateway.addCluster({ id: 'cluster-a' });
  gateway.addCluster({ id: 'cluster-b' });
  gateway.addCluster({ id: 'cluster-c' });
  gateway.provisionTenant('100', [
    { id: 't100-node-1' },
    { id: 't100-node-2' },
    { id: 't100-node-3' },
  ]);
  gateway.provisionTenant('200', [
    { id: 't200-node-1' },
    { id: 't200-node-2' },
    { id: 't200-node-3' },
  ]);
  return gateway;
}

function devices(prefix: string, count: number): string[] {
  const list: string[] = [];
  for (let i = 0; i < count; i += 1) {
    list.push(`${prefix}-${i}`);
  }
  return list;
}

test('acceptance: Tenant=100 Device=ABC cascades through both rings', () => {
  const gateway = buildGateway();
  const route = gateway.route({ tenant: 100, device: 'ABC' });
  assert.equal(route.tenant, '100');
  assert.equal(route.device, 'ABC');
  assert.ok(['cluster-a', 'cluster-b', 'cluster-c'].includes(route.cluster));
  assert.ok(route.node.startsWith('t100-node-'));
  assert.deepEqual(
    route.hops.map((hop) => hop.layer),
    [1, 2],
  );
  assert.equal(route.hops[0].target, route.cluster);
  assert.equal(route.hops[1].target, route.node);
  for (let i = 0; i < 50; i += 1) {
    assert.deepEqual(gateway.route({ tenant: 100, device: 'ABC' }), route);
  }
});

test('scaling tenant 100 does not disturb tenant 200 hash mapping', () => {
  const gateway = buildGateway();
  const beforeCluster = gateway.clusterOf('200');
  const sample = devices('mac', 2000);
  const beforeNodes = new Map(sample.map((d) => [d, gateway.nodeOf('200', d)]));

  gateway.addNode('100', { id: 't100-node-4' });
  gateway.addNode('100', { id: 't100-node-5' });
  gateway.addNode('100', { id: 't100-node-6' });

  assert.equal(gateway.clusterOf('200'), beforeCluster);
  for (const device of sample) {
    assert.equal(
      gateway.nodeOf('200', device),
      beforeNodes.get(device),
      `device ${device} of tenant 200 must not be remapped`,
    );
  }
});

test('tenant scale-out keeps key movement near the consistent-hash bound', () => {
  const gateway = buildGateway();
  const sample = devices('mac', 20000);
  const before = new Map(sample.map((d) => [d, gateway.nodeOf('100', d)]));
  gateway.addNode('100', { id: 't100-node-4' });
  let moved = 0;
  for (const device of sample) {
    if (gateway.nodeOf('100', device) !== before.get(device)) moved += 1;
  }
  const ratio = moved / sample.length;
  assert.ok(ratio > 0, 'new node should take over some keys');
  assert.ok(ratio < 0.45, `moved ratio ${ratio} should stay near 1/4`);
});

test('node failover: dead node is skipped, mapping restored after recovery', () => {
  const gateway = buildGateway();
  const route = gateway.route({ tenant: 100, device: 'ABC' });
  gateway.setNodeHealth('100', route.node, false);
  const rerouted = gateway.route({ tenant: 100, device: 'ABC' });
  assert.notEqual(rerouted.node, route.node);
  assert.equal(rerouted.cluster, route.cluster);
  assert.ok(rerouted.node.startsWith('t100-node-'));
  gateway.setNodeHealth('100', route.node, true);
  assert.deepEqual(gateway.route({ tenant: 100, device: 'ABC' }), route);
});

test('cluster failover: dead cluster is bypassed at layer 1', () => {
  const gateway = buildGateway();
  const route = gateway.route({ tenant: 100, device: 'ABC' });
  gateway.setClusterHealth(route.cluster, false);
  const rerouted = gateway.route({ tenant: 100, device: 'ABC' });
  assert.notEqual(rerouted.cluster, route.cluster);
  assert.equal(rerouted.node, route.node);
  gateway.setClusterHealth(route.cluster, true);
  assert.deepEqual(gateway.route({ tenant: 100, device: 'ABC' }), route);
});

test('layer-1 scale-out moves only a minority of tenants', () => {
  const gateway = new HierarchicalGateway();
  gateway.addCluster({ id: 'cluster-a' });
  gateway.addCluster({ id: 'cluster-b' });
  gateway.addCluster({ id: 'cluster-c' });
  const tenants: string[] = [];
  for (let i = 0; i < 2000; i += 1) tenants.push(`tenant-${i}`);
  const before = new Map(tenants.map((t) => [t, gateway.clusterOf(t)]));
  gateway.addCluster({ id: 'cluster-d' });
  let moved = 0;
  for (const tenant of tenants) {
    if (gateway.clusterOf(tenant) !== before.get(tenant)) moved += 1;
  }
  const ratio = moved / tenants.length;
  assert.ok(ratio > 0, 'new cluster should absorb some tenants');
  assert.ok(ratio < 0.45, `moved ratio ${ratio} should stay near 1/4`);
});

test('removing a cluster only remaps tenants that were on it', () => {
  const gateway = buildGateway();
  const tenants: string[] = [];
  for (let i = 0; i < 2000; i += 1) tenants.push(`tenant-${i}`);
  const before = new Map(tenants.map((t) => [t, gateway.clusterOf(t)]));
  gateway.removeCluster('cluster-b');
  for (const tenant of tenants) {
    const prev = before.get(tenant);
    const next = gateway.clusterOf(tenant);
    if (prev !== 'cluster-b') {
      assert.equal(next, prev, `tenant ${tenant} should not move`);
    } else {
      assert.ok(next === 'cluster-a' || next === 'cluster-c');
    }
  }
});
