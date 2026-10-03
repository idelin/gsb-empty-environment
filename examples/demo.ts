/**
 * End-to-end demo:
 *  1. 5-node Multi-Paxos cluster, leader lease fast path.
 *  2. Network partition: old leader stranded with a minority, its dirty
 *     entry is safely overwritten by the majority's new leader.
 *  3. A saga (transfer order) fails mid-flight on the recovered cluster
 *     and is rolled back by reverse compensation.
 */
import { Cluster } from '../src/paxos.ts';
import { SagaOrchestrator, IdempotentEffects } from '../src/saga.ts';
import type { StepDef } from '../src/saga.ts';

const IDS = ['n1', 'n2', 'n3', 'n4', 'n5'];
const cluster = new Cluster(IDS);
const n1 = cluster.node('n1');

console.log('=== Phase 1: normal operation (lease fast path) ===');
n1.propose({ kind: 'app', payload: 'set x=1' });
n1.propose({ kind: 'app', payload: 'set y=2' });
console.log(`n1 leader=${n1.isLeader} commitIndex=${n1.commitIndex} prepareRounds=${n1.prepareCount}`);
console.log(`n5 applied: ${JSON.stringify(cluster.node('n5').applied.map((e) => e.payload))}`);

console.log('\n=== Phase 2: partition {n1,n2} | {n3,n4,n5} ===');
cluster.partition([
  ['n1', 'n2'],
  ['n3', 'n4', 'n5'],
]);
try {
  n1.propose({ kind: 'app', payload: 'DIRTY' });
} catch (err) {
  console.log(`old leader cannot commit DIRTY: ${(err as Error).name}`);
}
console.log(`n1 log slot 3 (dirty): ${JSON.stringify(n1.log.get(3)?.entry.payload)}`);

cluster.clock.advance(1000); // leases expire
const n3 = cluster.node('n3');
n3.propose({ kind: 'app', payload: 'set z=3' });
console.log(`new leader n3 commitIndex=${n3.commitIndex} applied=${JSON.stringify(n3.applied.map((e) => e.payload))}`);

console.log('\n=== Phase 3: heal, minority converges, dirty entry overwritten ===');
cluster.heal();
n3.propose({ kind: 'app', payload: 'set w=4' });
for (const id of IDS) {
  const node = cluster.node(id);
  console.log(`${id}: applied=${JSON.stringify(node.applied.map((e) => e.payload))}`);
}

console.log('\n=== Phase 4: saga with reverse compensation ===');
const accounts = new Map([
  ['alice', 500],
  ['bob', 100],
]);
const effects = new IdempotentEffects();
const orch = new SagaOrchestrator(n3, effects);
const calls: string[] = [];

const transfer: StepDef[] = [
  {
    name: 'debit-alice',
    execute: () => {
      accounts.set('alice', accounts.get('alice')! - 200);
      calls.push('debit alice -200');
    },
    compensate: () => {
      accounts.set('alice', accounts.get('alice')! + 200);
      calls.push('refund alice +200');
    },
  },
  {
    name: 'credit-bob',
    execute: () => {
      throw new Error('bob account frozen');
    },
    compensate: () => {
      accounts.set('bob', accounts.get('bob')! - 200);
      calls.push('revoke bob -200');
    },
  },
];

const status = await orch.runSaga('order-1', transfer);
console.log(`saga status: ${status}`);
console.log(`effect order: ${calls.join(' -> ')}`);
console.log(`accounts: alice=${accounts.get('alice')} bob=${accounts.get('bob')} (unchanged)`);
console.log(`orchestrator quiescent: ${orch.isQuiescent}, network in-flight: ${cluster.net.inFlight}`);
