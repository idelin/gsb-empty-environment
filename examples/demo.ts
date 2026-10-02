import { performance } from 'node:perf_hooks';
import { GossipNode } from '../src/gossip.ts';
import { InMemoryHub, InMemoryTransport } from '../src/memory-transport.ts';
import type { NodeAddress } from '../src/types.ts';

const CLUSTER_SIZE = 50;
const ORPHAN_COUNT = 3;
const hub = new InMemoryHub();

function makeNode(id: string, seeds: NodeAddress[] = []): GossipNode {
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
    if (Date.now() - start > timeoutMs) throw new Error(`timeout waiting for: ${label}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

async function main(): Promise<void> {
  console.log(`[demo] starting seed node...`);
  const seed = makeNode('seed-0');
  await seed.start();
  console.log(`[demo] seed-0 listening on ${seed.address!.host}:${seed.address!.port}`);

  console.log(`[demo] starting ${CLUSTER_SIZE - 1} cluster nodes (seed = their only entry point)...`);
  const cluster = Array.from(
    { length: CLUSTER_SIZE - 1 },
    (_, i) => makeNode(`node-${i + 1}`, [seed.address!]),
  );
  await Promise.all(cluster.map((node) => node.start()));

  await waitFor(
    () => [seed, ...cluster].every((node) => node.membership.size === CLUSTER_SIZE),
    15000,
    'cluster convergence',
  );
  console.log(`[demo] cluster converged: every node knows all ${CLUSTER_SIZE} members`);

  console.log(`[demo] cold-starting ${ORPHAN_COUNT} orphan nodes with only the seed address...`);
  const startedAt = performance.now();
  const orphans = Array.from(
    { length: ORPHAN_COUNT },
    (_, i) => makeNode(`orphan-${i + 1}`, [seed.address!]),
  );
  await Promise.all(orphans.map((node) => node.start()));
  const elapsedMs = performance.now() - startedAt;

  for (const orphan of orphans) {
    const report = orphan.bootstrapReport!;
    console.log(
      `[demo] ${orphan.id}: bootstrap handshake ${report.durationMs.toFixed(1)}ms, ` +
        `learned ${report.learnedMembers} members, now knows ${report.knownMembers}/${CLUSTER_SIZE}`,
    );
  }
  console.log(
    `[demo] all ${ORPHAN_COUNT} orphans pulled the full ${CLUSTER_SIZE}-node snapshot in ` +
      `${elapsedMs.toFixed(1)}ms (budget: 1000ms) -> ${elapsedMs < 1000 ? 'PASS' : 'FAIL'}`,
  );

  await waitFor(
    () => orphans.every((orphan) => orphan.gossipMessagesReceived > 0),
    3000,
    'orphans to receive gossip',
  );
  console.log(`[demo] orphans are receiving gossip and have joined the shuffle ring:`);
  for (const orphan of orphans) {
    console.log(
      `[demo] ${orphan.id}: gossip rounds=${orphan.gossipRounds}, ` +
        `received=${orphan.gossipMessagesReceived}, shuffle ring size=${orphan.shuffleRing().length}`,
    );
  }

  await Promise.all([...orphans, ...cluster, seed].map((node) => node.stop()));
  console.log(`[demo] done, all nodes stopped`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
