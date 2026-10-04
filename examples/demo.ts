/**
 * End-to-end demo of the acceptance scenario:
 *   5-node cluster -> leader elected -> network partition isolates the old
 *   leader -> majority elects a new leader -> Phase 1 repair -> minority
 *   dirty log safely overwritten after healing.
 */
import { Cluster, Client } from '../src/cluster.ts';
import type { AcceptReply, Command } from '../src/node.ts';

function show(cluster: Cluster, label: string): void {
  console.log(`\n== ${label} ==`);
  for (const n of cluster.nodes) {
    const role = n.isLeader ? 'LEADER  ' : 'follower';
    const log = n
      .committedValues()
      .map((v) => (v === null ? 'noop' : v.data))
      .join(', ');
    console.log(
      `  N${n.id} [${role}] ballot=${n.currentBallot.round}:${n.currentBallot.nodeId} ` +
        `committed=${n.commitIndex} log=[${log}]`,
    );
  }
}

const cluster = new Cluster({ size: 5, leaseMs: 60_000 });
const client = new Client('app', cluster);

console.log('--- 1. N1 wins Phase 1 and commits A, B under its lease ---');
await cluster.node(1).campaign();
await client.submit('A', { prefer: cluster.node(1) });
await client.submit('B', { prefer: cluster.node(1) });
await cluster.settle();
show(cluster, 'after A,B committed (note: no Phase 1 for B, lease bypass)');

console.log('\n--- 2. Partition {N1,N2} | {N3,N4,N5}; dirty value X lands on minority ---');
cluster.partition([1, 2], [3, 4, 5]);
const dirty: Command = { clientId: 'app', seq: 900, data: 'X-dirty' };
const b1 = cluster.node(1).currentBallot;
for (const target of [1, 2]) {
  await cluster.net.call<AcceptReply>(1, target, {
    type: 'accept',
    slot: 3,
    ballot: b1,
    value: dirty,
  });
}
try {
  await cluster.node(1).propose({ clientId: 'app', seq: 901, data: 'Y' });
} catch (e) {
  console.log(`  old leader N1 cannot commit in minority: ${(e as Error).name}`);
}
show(cluster, 'minority holds dirty X at slot 3, watermark stuck at 2');

console.log('\n--- 3. Majority elects N3 (Phase 1 + repair), commits C, D ---');
await cluster.node(3).campaign();
await client.submit('C', { prefer: cluster.node(3) });
await client.submit('D', { prefer: cluster.node(3) });
await cluster.settle();
show(cluster, 'majority progresses despite the partition');

console.log('\n--- 4. Heal: commit notices overwrite the dirty minority log ---');
cluster.heal();
await cluster.node(3).broadcastCommits();
await cluster.settle();
show(cluster, 'all 5 nodes converge; X-dirty is gone, never committed');

console.log('\n--- 5. Cluster keeps accepting writes through any node ---');
await client.submit('E', { prefer: cluster.node(1) });
for (const n of cluster.leaders()) await n.broadcastCommits();
await cluster.settle();
show(cluster, 'final state');

console.log(
  `\nnetwork: sent=${cluster.net.stats.sent} delivered=${cluster.net.stats.delivered} ` +
    `dropped=${cluster.net.stats.dropped} inFlight=${cluster.net.inFlight} ` +
    `pendingTimers=${cluster.net.pendingTimers}`,
);
