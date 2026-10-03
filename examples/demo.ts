import { Cluster } from '../src/cluster.ts';
import { InvariantMonitor } from '../src/monitor.ts';
import { inspect } from '../src/inspector.ts';

/**
 * End-to-end demo of the acceptance scenario:
 * 5-node cluster, network partition strands the old leader, a majority
 * leader takes over, repairs open slots via Phase 1 and safely overwrites
 * the minority's dirty log after healing.
 */
function show(c: Cluster, label: string): void {
  console.log(`\n--- ${label} ---`);
  for (const id of c.nodeIds) {
    const n = c.node(id);
    const role = n.role === 'leader' ? 'L' : '.';
    console.log(
      `  ${id} [${role}] commitIndex=${String(n.commitIndex).padStart(2)}  ` +
        `log=[${n.committedPrefix().join(', ')}]`,
    );
  }
}

const c = new Cluster(5);
const mon = new InvariantMonitor(c);

console.log('== 1. A wins leadership and commits entries under its lease ==');
c.elect('A');
c.runUntilIdle();
c.propose('A', 'set x=1');
c.propose('A', 'set y=2');
c.runUntilIdle();
show(c, 'A committed 2 entries on all 5 nodes');

console.log('\n== 2. Partition {A,B} | {C,D,E}; A keeps proposing (dirty minority) ==');
c.partition([['A', 'B'], ['C', 'D', 'E']]);
c.propose('A', 'dirty-1');
c.propose('A', 'dirty-2');
c.runUntilIdle();
show(c, 'dirty entries exist only in the minority partition');

console.log('\n== 3. C is elected by the majority; Phase 1 repairs open slots ==');
c.elect('C');
c.runUntilIdle();
c.propose('C', 'set z=3');
c.runUntilIdle();
show(c, 'majority partition keeps committing under the new lease');

console.log('\n== 4. Heal: heartbeats resync; dirty minority logs are overwritten ==');
c.heal();
c.tick(60);
c.runUntilIdle();
show(c, 'all nodes converge on one gap-free committed prefix');

console.log('\n== 5. Formal inspection of the final full state ==');
const report = inspect(c.snapshot());
console.log(`  dangling refs : ${report.danglingRefs.length}`);
console.log(`  orphan cycles : ${report.orphanCycles.length}`);
console.log(`  skew findings : ${report.skew.length}`);
console.log(`  invariants    : ${report.invariantViolations.length} violation(s)`);
console.log(`  result        : ${report.ok ? 'OK' : 'FAILED'}`);

mon.assertClean();
const leaks = c.findLeaks();
console.log(`  leak check    : ${leaks.length === 0 ? 'no leaks' : leaks.join('; ')}`);
console.log(`  monitor checks: ${mon.checksRun} invariant checks executed`);
mon.dispose();
c.dispose();
