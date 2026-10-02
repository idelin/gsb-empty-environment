import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { GossipNode } from '../src/gossip.ts';
import { InMemoryHub, InMemoryTransport } from '../src/memory-transport.ts';

const CLUSTER_SIZE = 50;
const ORPHAN_COUNT = 3;
const BOOTSTRAP_BUDGET_MS = 1000;
const hub = new InMemoryHub();

function makeNode(id: string, seeds: { host: string; port: number }[] = []): GossipNode {
  return new GossipNode({
    id,
    seeds,
    gossipIntervalMs: 50,
    fanout: 5,
    transport: new InMemoryTransport(hub),
  });
}

async function waitFor(cond: () => boolean, timeoutMs: number, label: string): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error(`timeout (${timeoutMs}ms) waiting for: ${label}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

const seed = makeNode('seed-0');
const cluster: GossipNode[] = [];
const orphans: GossipNode[] = [];

before(async () => {
  await seed.start();
  const seedAddress = seed.address!;
  const nodes = Array.from(
    { length: CLUSTER_SIZE - 1 },
    (_, i) => makeNode(`node-${i + 1}`, [seedAddress]),
  );
  await Promise.all(nodes.map((node) => node.start()));
  cluster.push(...nodes);

  await waitFor(
    () => [seed, ...cluster].every((node) => node.membership.size === CLUSTER_SIZE),
    15000,
    `cluster of ${CLUSTER_SIZE} to fully converge via gossip`,
  );
});

after(async () => {
  await Promise.all([...orphans, ...cluster, seed].map((node) => node.stop()));
});

test('3 orphan nodes bootstrap from a single seed and pull all 50 members within 1s', async () => {
  const seedAddress = seed.address!;
  const startedAt = performance.now();

  const fresh = Array.from(
    { length: ORPHAN_COUNT },
    (_, i) => makeNode(`orphan-${i + 1}`, [seedAddress]),
  );
  await Promise.all(fresh.map((node) => node.start()));
  orphans.push(...fresh);

  const elapsedMs = performance.now() - startedAt;

  const expectedIds = new Set([seed, ...cluster].map((node) => node.id));
  for (const orphan of orphans) {
    const report = orphan.bootstrapReport!;
    assert.equal(report.contactedSeeds, 1, `${orphan.id} contacted its single seed`);
    assert.ok(
      orphan.membership.size >= CLUSTER_SIZE,
      `${orphan.id} knows all ${CLUSTER_SIZE} cluster members (has ${orphan.membership.size})`,
    );
    for (const id of expectedIds) {
      assert.ok(orphan.membership.has(id), `${orphan.id} has metadata for ${id}`);
    }
  }
  assert.ok(
    elapsedMs < BOOTSTRAP_BUDGET_MS,
    `bootstrap of ${ORPHAN_COUNT} orphans took ${elapsedMs.toFixed(1)}ms (budget ${BOOTSTRAP_BUDGET_MS}ms)`,
  );
});

test('orphans are registered at the seed immediately after the handshake', () => {
  for (const orphan of orphans) {
    const meta = seed.membership.get(orphan.id);
    assert.ok(meta, `seed knows ${orphan.id} right after its bootstrap`);
    assert.equal(meta!.state, 'alive');
    assert.deepEqual(meta!.address, orphan.address);
  }
});

test('orphans immediately join the gossip shuffle ring', async () => {
  await waitFor(
    () => orphans.every((orphan) => orphan.gossipMessagesReceived > 0),
    3000,
    'orphans to receive gossip from the ring',
  );
  await waitFor(
    () =>
      orphans.every((orphan) =>
        cluster.some((node) => node.membership.has(orphan.id)),
      ),
    3000,
    'orphan metadata to propagate beyond the seed into the ring',
  );
  for (const orphan of orphans) {
    assert.ok(orphan.gossipRounds > 0, `${orphan.id} actively gossips`);
    assert.ok(
      orphan.shuffleRing().length >= CLUSTER_SIZE - 1,
      `${orphan.id} shuffle ring covers the whole cluster`,
    );
  }
});
