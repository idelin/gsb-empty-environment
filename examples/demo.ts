// Runnable demo of the acceptance scenario:
//   5-node cluster -> network partition isolates the old leader ->
//   majority elects a new leader -> Phase 1 repairs slots ->
//   heal -> minority dirty log is safely overwritten.
//
// Run: npm run demo   (or: node examples/demo.ts)

import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { Cluster } from '../src/cluster.ts';
import { EntryType } from '../src/codec.ts';

function show(cluster: Cluster, label: string): void {
  console.log(`\n== ${label} ==`);
  for (const n of cluster.nodes) {
    const entries: string[] = [];
    for (let i = 1; i <= n.lastIndex; i++) {
      const v = n.entryAt(i);
      entries.push(v ? (v.type === EntryType.Noop ? 'noop' : v.payload.toString()) : '·');
    }
    console.log(
      `  node${n.id} [${n.currentRole.padEnd(8)}] commit=${n.commitIndex} log=[${entries.join(', ')}]`,
    );
  }
}

const cluster = Cluster.create(5, { leaseMs: 60_000, rpcTimeoutMs: 150 });
try {
  const oldLeader = cluster.node(1);
  await oldLeader.campaign();
  await oldLeader.propose(Buffer.from('A'));
  await oldLeader.propose(Buffer.from('B'));
  await cluster.settle();
  show(cluster, 'node1 elected; A and B committed on all 5 nodes');

  console.log('\n-- network partition: {1,2} isolated from {3,4,5} --');
  cluster.partition([1, 2]);
  await oldLeader.propose(Buffer.from('C-dirty')).catch(() => console.log('   old leader write C rejected: no quorum'));
  await oldLeader.propose(Buffer.from('D-dirty')).catch(() => console.log('   old leader write D rejected: no quorum'));
  show(cluster, 'old leader keeps dirty uncommitted entries on the minority side');

  const newLeader = cluster.node(4);
  await newLeader.campaign();
  await newLeader.propose(Buffer.from('X'));
  await newLeader.propose(Buffer.from('Y'));
  await cluster.settle();
  show(cluster, 'node4 elected by majority (Phase 1 repaired slots); X, Y committed');

  console.log('\n-- partition healed --');
  cluster.heal();
  await newLeader.propose(Buffer.from('Z'));
  await cluster.settle();
  show(cluster, 'minority dirty log truncated and overwritten by majority log');

  for (const n of cluster.nodes) {
    const committed: string[] = [];
    for (let i = 1; i <= n.commitIndex; i++) committed.push(n.entryAt(i)?.payload.toString() ?? '?');
    assert.deepEqual(committed, ['A', 'B', 'X', 'Y', 'Z'], `node ${n.id} diverged`);
    assert.equal(n.activeTimers, 0);
  }
  console.log('\nOK: all 5 nodes converged to [A, B, X, Y, Z]; no committed value lost; no leaks.');
} finally {
  await cluster.stop();
}
