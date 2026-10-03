/**
 * Runnable demo of the acceptance scenario:
 * 5-node cluster, network partition, old leader replaced by a majority-side
 * leader, Phase-1 repair of uncommitted slots, safe overwrite of the
 * minority's dirty log, and final reconvergence.
 *
 * Run with: node examples/demo.ts   (Node >= 23.6, or 22.6+ with --experimental-strip-types)
 */
import { Cluster } from '../src/cluster.ts';
import { SafetyOracle } from '../src/invariants.ts';

const IDS = ['n1', 'n2', 'n3', 'n4', 'n5'];
const oracle = new SafetyOracle();

function show(cluster: Cluster, title: string): void {
  console.log(`\n=== ${title} (t=${cluster.now}) ===`);
  for (const n of cluster.nodes) {
    const ballot = n.leaderBallot ? `${n.leaderBallot.round}@${n.leaderBallot.node}` : '-';
    console.log(
      `  ${n.id}  role=${n.role.padEnd(8)} ballot=${ballot.padEnd(5)} ` +
        `commitIndex=${String(n.commitIndex).padStart(2)} applied=${JSON.stringify(n.applied)}`,
    );
  }
  const violations = oracle.check(cluster);
  console.log(`  invariants: ${violations.length === 0 ? 'OK' : violations.join('; ')}`);
}

const cluster = new Cluster(IDS, {
  trace: (line) => console.log(`    · ${line}`),
});

console.log('--- phase 0: boot, n1 wins the election ---');
cluster.runUntil(() => cluster.leader()?.id === 'n1', 100);
cluster.write('n1', 'cmd-1');
cluster.run(5);
cluster.write('n1', 'cmd-2');
cluster.run(5);
show(cluster, 'cmd-1, cmd-2 committed everywhere');

console.log('\n--- phase 1: partition {n1,n2} | {n3,n4,n5} ---');
cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
console.log('--- phase 2: dirty write on the isolated old leader ---');
console.log(`    write("dirty") -> ${JSON.stringify(cluster.write('n1', 'dirty'))}`);
cluster.run(4);
show(cluster, 'dirty slot accepted by minority only, uncommitted');

console.log('\n--- phase 3: majority side elects n3 via Prepare/Promise ---');
cluster.runUntil(() => cluster.node('n3').isLeader, 300);
cluster.run(40);
show(cluster, 'n3 leads the majority; the dirty slot 2 was never seen by its quorum');

console.log('\n--- phase 4: new leader reuses slot 2 for cmd-3 under its lease (no Phase 1) ---');
cluster.write('n3', 'cmd-3');
cluster.run(10);
show(cluster, 'cmd-3 committed on the majority side');

console.log('\n--- phase 5: heal; old leader deposed, dirty log overwritten ---');
cluster.healAll();
cluster.run(200);
show(cluster, 'cluster reconverged');

const violations = oracle.check(cluster);
console.log(`\nfinal invariant check: ${violations.length === 0 ? 'ALL OK' : violations.join('; ')}`);
console.log(`oracle committed log: ${JSON.stringify(oracle.committedLog())}`);
console.log(`in-flight messages leaked: ${cluster.network.pending}`);
