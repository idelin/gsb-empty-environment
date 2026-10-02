import { Cluster } from '../src/cluster.ts';
import type { RaftNode } from '../src/raft.ts';

const cluster = new Cluster(
  [
    { id: 'A', electionTimeout: 25 },
    { id: 'B', electionTimeout: 40 },
    { id: 'C', electionTimeout: 10 },
  ],
  4,
);
const [a, b, c] = [cluster.node('A'), cluster.node('B'), cluster.node('C')];

function show(title: string): void {
  console.log(`\n=== ${title} (tick=${cluster.clock.now}) ===`);
  for (const node of [a, b, c]) {
    const log = node.log.map((e) => `${e.command}@t${e.term}`).join(' ');
    console.log(
      `  ${node.id}: ${node.role.padEnd(8)} term=${node.currentTerm} commit=${node.commitIndex} log=[${log}]`,
    );
  }
}

function leader(): RaftNode {
  const node = cluster.leader();
  if (!node) throw new Error('no leader');
  return node;
}

cluster.run(30);
show('Phase 1: C wins the term-1 election');

c.append('x1');
c.append('x2');
c.append('x3');
cluster.run(12);
show('Phase 1: 3 entries committed on A/B/C in term 1');

cluster.isolate('C');
c.append('u1'); // old leader keeps writing while cut off:
c.append('u2'); // entries stay local and uncommitted
cluster.run(80);
show('Phase 2: C isolated; A/B elected term-2 leader (pre-vote kept C at term 1)');

leader().append('y1');
leader().append('y2');
leader().append('y3');
leader().append('y4');
cluster.run(15);
show('Phase 2: majority A/B commits 4 more entries (indexes 4-7)');

cluster.heal('C');
cluster.run(40);
show('Phase 3: C healed; term-2 heartbeats force rollback of u1/u2, committed x1..x3 kept');
