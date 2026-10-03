/**
 * Acceptance scenario: 5-node cluster, network partition, old leader
 * replaced by a majority-backed new leader, gap repair via Phase 1, safe
 * overwrite of minority dirty logs, then heal and converge.
 *
 * Run: npm run demo
 */
import { ENTRY_NOOP } from '../src/codec/logEntry.ts';
import { Cluster } from '../src/paxos/cluster.ts';
import { NoMajorityError } from '../src/paxos/types.ts';
import { resources } from '../src/storage/resources.ts';

const dir = new URL('../.demo-data', import.meta.url).pathname;
const cluster = await Cluster.start([1, 2, 3, 4, 5], { rootDir: dir, leaseMs: 5_000 });
const v = (s: string) => Buffer.from(s, 'utf8');

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
  for (const [id, n] of cluster.nodes) {
    const tag = n.proposer.isLeader ? 'LEADER ' : 'follower';
    console.log(
      `  node ${id} [${tag}] promised=${n.store.promised.term}:${n.store.promised.nodeId}` +
        ` commitIndex=${n.commitIndex} log=[${n.committedSummary()}]`,
    );
  }
}

// 1. Node 1 becomes leader and commits two entries (lease skips Phase 1).
const i1 = await cluster.node(1).proposer.propose(v('set x=1'));
const i2 = await cluster.node(1).proposer.propose(v('set y=2'));
console.log(`node 1 elected leader; committed slots ${i1},${i2} (Phase 1 ran once, lease covered the rest)`);

// 2. A dirty entry lands on a MINORITY {1,2} only: cut 1,2 from 3,4,5 first,
//    then let node 1 try to replicate slot 3 (it can never reach majority).
cluster.partition([1, 2], [3, 4, 5]);
await cluster
  .node(1)
  .proposer.propose(v('set z=dirty'))
  .catch((e) => {
    if (!(e instanceof NoMajorityError)) throw e;
    console.log(`\nold leader 1 cannot commit slot 3 under partition (${e.message})`);
  });
console.log(`slot 3 on node 1 (dirty, minority): "${cluster.node(1).store.get(3)?.value}"`);
show('partition {1,2} | {3,4,5}: old leader holds an uncommitted dirty slot 3');

// 3. The old leader's lease expires (logical time passes), then the majority
//    side elects node 3: Phase 1 promises from {3,4,5} show slot 3 has no
//    accepted value on any majority acceptor -> safe to overwrite.
cluster.clock.advance(6_000);
console.log('\nlease expired (logical clock advanced past 5s lease); majority side elects a new leader');
const i3 = await cluster.node(3).proposer.propose(v('set z=9'));
const i4 = await cluster.node(3).proposer.propose(v('set w=8'));
console.log(`\nnew leader 3 filled slot 3 and committed slots ${i3},${i4} under its lease`);
show('new leader 3 repaired slot 3 (no-op/overwrite) and extended the log');

// 4. Heal: node 1 and 2 accept the higher-term entries; dirty slot 3 is
//    safely overwritten because it was never majority-committed.
cluster.heal();
await cluster.node(3).proposer.propose(v('set k=5'));
show('healed: dirty slot 3 on nodes 1,2 overwritten by the majority log');

const { commitIndex, summary } = cluster.assertConverged();
console.log(`\nCONVERGED commitIndex=${commitIndex}`);
console.log(`final log on every node: ${summary}`);
console.log(`slot 3 is now "${cluster.node(1).store.get(3)?.value}" (term ${cluster.node(1).store.get(3)?.term}) on all 5 nodes`);
console.log(`no-op fill check: any noop slots? ${cluster.node(1).committedLog().some((e) => e.type === ENTRY_NOOP)}`);

await cluster.close();
resources.assertDrained();
console.log('\nresource check: all stores/segments closed, zero leaks');
