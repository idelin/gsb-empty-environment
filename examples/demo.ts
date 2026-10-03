import { Cluster } from '../src/cluster.ts';
import type { Command } from '../src/types.ts';

const set = (key: string, value: string): Command => ({ kind: 'set', key, value });

function show(c: Cluster, label: string): void {
  console.log(`\n=== ${label} ===`);
  for (const node of c.nodes) {
    const log = node
      .getCommittedLog()
      .map((e) => `${e.slot}:${e.command.kind === 'set' ? `${e.command.key}=${e.command.value}` : 'noop'}`)
      .join(' ');
    console.log(
      `  node${node.id} [${node.getRole()}] commitIndex=${node.getCommitIndex()} log: ${log || '(empty)'}`,
    );
  }
}

const c = new Cluster(5, { leaseDuration: 50 });

// 1. Election + lease fast path.
c.elect(0);
console.log('node0 elected leader (Phase 1: Prepare/Promise), lease granted');
const since = c.sentLog.length;
c.submit(0, set('user:1', 'alice'));
c.submit(0, set('user:2', 'bob'));
c.submit(0, set('user:3', 'carol'));
c.stabilize();
console.log(`3 writes committed with ${c.countMessages('Prepare', since)} Prepare messages (lease bypasses Phase 1)`);
show(c, 'steady state: leader lease, contiguous commits');

// 2. Partition: old leader stranded with one minority node.
c.partition([0, 1], [2, 3, 4]);
console.log('\n--- network partition: {0,1} | {2,3,4} ---');
c.submit(0, set('dirty', 'B')); // slot 4: minority only, never committable
c.stabilize();
console.log(`old leader node0 proposed dirty entry, commitIndex still ${c.nodes[0].getCommitIndex()} (no majority)`);

// 3. Majority elects a new leader; Phase 1 recovers/fills slots.
c.nodes[2].startElection();
c.stabilize();
c.submit(2, set('user:4', 'dave'));
c.stabilize();
show(c, 'new leader node2 (majority side) commits with hole-free log');

// 4. Heal: minority converges, dirty entry safely overwritten.
c.heal();
c.submit(2, set('user:5', 'erin')); // forces Commit broadcast to all nodes
c.stabilize();
show(c, 'healed: all nodes converge, dirty minority log overwritten');

console.log(`\nnode0 role after heal: ${c.nodes[0].getRole()} (stepped down on higher term)`);
console.log(`dirty value applied anywhere: ${c.nodes.some((n) => n.getState().has('dirty'))}`);
c.assertConverged();
console.log('safety invariants hold: no divergence, hole-free committed prefixes, no leaks');
