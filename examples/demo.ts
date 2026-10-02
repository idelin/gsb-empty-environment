import { HierarchicalQuorumArbiter } from '../src/arbiter.ts';
import { Region } from '../src/region.ts';
import type { Decision } from '../src/types.ts';

function printDecision(label: string, decision: Decision, verified: boolean): void {
  console.log(`\n=== ${label} ===`);
  console.log(
    `proposal=${decision.proposalId} epoch=${decision.epoch} mode=${decision.mode} ` +
      `outcome=${decision.outcome} verified=${verified}`,
  );
  console.log(
    `tally: approve=${decision.tally.approveWeight} reject=${decision.tally.rejectWeight} ` +
      `quorum=${decision.tally.quorumRequired}/${decision.tally.quorumWeightBasis}`,
  );
  for (const vote of decision.evidence) {
    console.log(
      `  vote ${vote.regionId}: approve=${vote.approve} weight=${vote.weight} ` +
        `local=${vote.localApprovals}/${vote.localReplicas} sig=${vote.signature.slice(0, 16)}...`,
    );
  }
  console.log(`  arbiter sig=${decision.decisionSignature.slice(0, 16)}...`);
}

const arbiter = new HierarchicalQuorumArbiter();
arbiter.registerRegion(
  new Region({ id: 'IDC-A', weight: 2, localThreshold: 0.5, replicaCount: 4 }),
  { primary: true },
);
arbiter.registerRegion(
  new Region({ id: 'IDC-B', weight: 1, localThreshold: 0.5, replicaCount: 3 }),
);
arbiter.registerRegion(
  new Region({ id: 'IDC-C', weight: 1, localThreshold: 0.5, replicaCount: 3 }),
);

console.log('cluster: IDC-A(w=2, primary), IDC-B(w=1), IDC-C(w=1)');

const r1 = arbiter.propose('txn-001', 'INSERT INTO t VALUES (1)');
printDecision('1. Normal hierarchical commit (A+B+C)', r1.decision, r1.verified);

console.log('\n--- IDC-A loses connectivity (network flap) ---');
arbiter.setReachable('IDC-A', false);
console.log(`topology degraded, epoch bumped to ${arbiter.currentEpoch}`);

const r2 = arbiter.propose('txn-002', 'INSERT INTO t VALUES (2)');
printDecision('2. Degraded commit by temporary B+C majority', r2.decision, r2.verified);

console.log('\n--- IDC-A recovers and presents a higher epoch ---');
arbiter.setReachable('IDC-A', true);
const synced = arbiter.rejoinPrimary(arbiter.currentEpoch + 1);
console.log(
  `primary rejoined at epoch ${arbiter.currentEpoch}, synced ${synced.length} ` +
    `committed decision(s): [${synced.map((d) => d.proposalId).join(', ')}]`,
);

const r3 = arbiter.propose('txn-003', 'INSERT INTO t VALUES (3)');
printDecision('3. Hierarchical control restored', r3.decision, r3.verified);

console.log('\n--- stale-epoch rejoin is rejected (no state conflict) ---');
try {
  arbiter.rejoinPrimary(arbiter.currentEpoch);
} catch (error) {
  console.log(`rejected: ${(error as Error).message}`);
}
