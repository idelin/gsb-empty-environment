import { makeCluster } from './cluster.ts';
import { inspectCluster, snapshotFromNodes } from './inspector.ts';
import { cmd, valueToString } from './types.ts';
import { LEASE_MS } from './node.ts';

function show(cluster: ReturnType<typeof makeCluster>, label: string): void {
  console.log(`\n== ${label} ==`);
  for (const node of cluster.nodes) {
    const committed = [...node.committed.values()]
      .sort((a, b) => a.slot - b.slot)
      .map((e) => `${e.slot}:${valueToString(e.value)}`)
      .join(' ');
    const dirty = [...node.log.values()].filter((e) => !node.committed.has(e.slot));
    const dirtyStr = dirty.map((e) => `${e.slot}:${valueToString(e.value)}`).join(' ');
    console.log(
      `  ${node.id} commitIndex=${node.commitIndex} committed=[${committed}]` +
        (dirtyStr ? ` uncommitted=[${dirtyStr}]` : '') +
        (node.leaseValid() ? ' LEADER(lease)' : ''),
    );
  }
}

async function main(): Promise<void> {
  const cluster = makeCluster(5);
  const { nodes, net, clock, monitor, quorum } = cluster;
  const [a, , , d] = nodes;

  console.log('--- 5-node Multi-Paxos cluster, quorum =', quorum, '---');

  await a.propose(cmd('set x=1'));
  await a.propose(cmd('set y=2'));
  await net.flush();
  show(cluster, 'N1 elected leader, two entries committed on majority');

  // Dirty minority log: N1 keeps serving under its lease after the
  // partition; its accepts land only on N1/N2 and can never commit.
  net.cut(['N1', 'N2'], ['N3', 'N4', 'N5']);
  try {
    await a.propose(cmd('set z=3'));
  } catch {
    console.log('\nN1 (old leader, minority side) cannot commit: accept phase failed as expected');
  }
  show(cluster, 'partition {N1,N2} | {N3,N4,N5}; slot 3 dirty on minority only');

  clock.advance(LEASE_MS + 1); // old lease expires
  await d.propose(cmd('set w=4'));
  await net.flush();
  show(cluster, 'N4 won Phase 1 in majority partition, committed slot 3');

  net.heal();
  await net.flush();
  // Let the healed minority catch up on anything it missed.
  await d.propose(cmd('set v=5'));
  await net.flush();
  show(cluster, 'partition healed; minority dirty log safely overwritten');

  const report = inspectCluster(snapshotFromNodes(nodes, quorum));
  console.log('\n== offline integrity inspection ==');
  console.log(`  ok=${report.ok} dangling=${report.danglingRefs.length} orphanLoops=${report.orphanLoops.length} skew=${report.dataSkew.length} conflicts=${report.committedConflicts.length}`);

  monitor.checkCluster();
  monitor.assertNoLeaks();
  monitor.assertClean();
  console.log('== invariant monitor: clean, no leaks ==');
  console.log(`\nmessage stats: delivered=${net.stats.delivered} dropped=${net.stats.dropped}`);
}

await main();
