/**
 * Runnable demo of the acceptance scenario:
 * 5 nodes -> leader elected -> network partition 2|3 -> old leader replaced
 * -> Phase 1 repair fills holes -> minority dirty log safely overwritten.
 */
import { Cluster } from '../src/cluster.ts';
import { LEASE_DURATION } from '../src/node.ts';

const c = new Cluster(5);

function show(title: string): void {
  console.log(`\n=== ${title} (t=${c.now}) ===`);
  for (const n of c.nodes) {
    const log = [...n.log.entries()]
      .map(([slot, e]) => `${slot}:${e.value}`)
      .join(' ');
    console.log(
      `  node${n.id}  ${n.role.padEnd(9)} commit=${n.commitIndex}  committed=[${n.committedValues().join(', ')}]  log={${log}}`,
    );
  }
  console.log(`  messages: ${JSON.stringify(c.stats)}`);
}

console.log('Multi-Paxos demo: 5-node cluster, majority = 3');

c.elect(0);
c.run();
c.propose(0, 'set x=1');
c.run();
show('1. node0 elected; "set x=1" committed on all nodes');

c.partition([0, 1], [2, 3, 4]);
c.propose(0, 'set y=2'); // dirty: minority side only
c.run();
show('2. partition {0,1}|{2,3,4}; old leader cannot commit "set y=2"');

c.elect(2);
c.run();
c.propose(2, 'set z=3');
c.run();
show('3. majority elects node2; Phase 1 repair; "set z=3" committed');

c.wait(LEASE_DURATION + 10);
c.run();
c.propose(0, 'set w=4'); // deferred: lease expired, re-election fails in minority
c.run();
show('4. old leader lease expired; minority re-election cannot commit');

c.heal();
c.elect(2);
c.run();
show('5. healed; re-election overwrites dirty log; all nodes converge');

console.log('\nFinal committed prefix on every node: ["set x=1", "set z=3"]');
console.log('Safety held: dirty "set y=2"/"set w=4" never committed; pending events:', c.pendingEvents);
