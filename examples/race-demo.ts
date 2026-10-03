/**
 * Demo: two nodes race for the master lease within the same millisecond.
 * Run with:  node examples/race-demo.ts   (Node >= 23.6)
 */
import { simulateMasterLeaseRace } from '../src/index.ts';

const race = simulateMasterLeaseRace({
  nodeA: 'node-A',
  nodeB: 'node-B',
  leaseId: 'master',
  baseMs: Date.now(),
  gapMs: 0.42, // second request arrives 0.42 ms after the first
  ttlMs: 5_000,
});

console.log('=== Lease contention race replay (audit trail) ===\n');
for (const frame of race.trail.replay('master')) {
  console.log(frame.line);
}

console.log('\n=== Verdict ===');
console.log(`winner: ${race.winner}   loser: ${race.loser}`);
console.log(`rejection: ${race.rejectionEvent.id} reason=${race.rejectionEvent.details.reason}`);

console.log('\n=== Causal evidence chain for the rejection ===');
for (const e of race.evidenceChain) {
  const caused = e.causedBy.length ? `  <- caused by [${e.causedBy.join(', ')}]` : '';
  console.log(`${e.id} L#${e.lamport} ${e.type} (${e.nodeId ?? 'arbiter'})${caused}`);
}
