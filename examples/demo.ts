/**
 * End-to-end demo:
 *  1. 5-node Multi-Paxos cluster elects a leader; entries commit via the
 *     lease fast path (no Phase 1 per entry).
 *  2. A saga runs its steps, journaling every transition into the log.
 *  3. The network partitions {n1,n2}|{n3,n4,n5}; the old leader keeps a dirty
 *     minority write while the majority elects a new leader that repairs the
 *     log through Phase 1.
 *  4. The saga fails on the majority side; reverse compensation restores the
 *     ledger; the partition heals and every node converges to the same log.
 */
import { Cluster, awaitCommit } from '../src/cluster.ts';
import { Effects, SagaOrchestrator, SagaStateMachine, type SagaStep } from '../src/saga.ts';
import { commandLabel, type Command } from '../src/types.ts';

const client = (data: string): Command => ({ kind: 'client', id: data, data });
const show = (label: string, c: Cluster) => {
  for (const n of c.nodes) {
    const log = n.applied.map((e) => commandLabel(e.value)).join(' ');
    console.log(`  n${n.id} [${n.role.padEnd(8)}] commit=${n.commitIndex} applied: ${log}`);
  }
  console.log(`  ^-- ${label}\n`);
};

const c = new Cluster(5);
const sms = c.nodes.map(() => new SagaStateMachine());
c.nodes.forEach((n, i) =>
  n.onCommit((_, entry) => {
    if (entry.value.kind === 'saga') sms[i].apply(entry.value.event);
  }),
);

console.log('=== 1. Election + lease fast path ===');
const n1 = c.electLeader();
n1.propose(client('init'));
show('n1 leads; entry committed on all 5 nodes without per-entry Phase 1', c);

console.log('=== 2. Saga orchestration on the replicated log ===');
const ledger = new Map([
  ['alice', 500],
  ['bob', 200],
]);
const fx = new Effects();
const pay = (saga: string, step: string, from: string, to: string, amt: number): SagaStep<unknown> => ({
  name: step,
  execute: () =>
    fx.once(`${saga}:${step}:exec`, () => {
      ledger.set(from, ledger.get(from)! - amt);
      ledger.set(to, ledger.get(to)! + amt);
      console.log(`  exec ${step}: ${from} -${amt} -> ${to}  ${JSON.stringify([...ledger])}`);
    }),
  compensate: () =>
    fx.once(`${saga}:${step}:comp`, () => {
      ledger.set(from, ledger.get(from)! + amt);
      ledger.set(to, ledger.get(to)! - amt);
      console.log(`  comp ${step}: rollback ${from} +${amt} <- ${to}`);
    }),
});

const sagaId = 'order-42';
const steps: SagaStep<unknown>[] = [
  pay(sagaId, 'reserve', 'alice', 'bob', 120),
  pay(sagaId, 'ship', 'bob', 'alice', 20),
  {
    name: 'charge',
    execute: () => {
      throw new Error('payment gateway rejected the charge');
    },
    compensate: () => {},
  },
];

// Orchestrator always runs against the current leader.
const orchFor = (id: number) =>
  new SagaOrchestrator<unknown>(
    async (event) => {
      const slot = c.node(id).propose({ kind: 'saga', event });
      await awaitCommit(c.node(id), slot);
    },
    sms[id - 1],
  );

console.log('=== 3. Partition: old leader isolated, saga fails on majority side ===');
c.partition([1, 2], [3, 4, 5]);
n1.propose(client('dirty-write')); // minority-only, can never commit
console.log('  n1 (isolated) wrote dirty entry; commitIndex stays', n1.commitIndex);
c.runUntil(() => c.node(3).role === 'leader');
console.log('  n3 elected by majority; Phase 1 repaired uncommitted slots');
c.heal();
c.runUntil(() => c.node(1).role === 'follower');

const rec = await orchFor(3).submit(sagaId, steps, {});
console.log(`  saga ${rec.id}: status=${rec.status} error=${rec.error}`);
console.log('  ledger after compensation:', JSON.stringify([...ledger]), '(back to initial state)');
c.runUntil(() => c.nodes.every((n) => n.commitIndex === c.node(3).commitIndex));
show('healed: dirty write overwritten, saga transitions committed everywhere', c);

console.log('=== 4. Idempotent re-submission ===');
const again = await orchFor(3).submit(sagaId, steps, {});
console.log(`  re-submit same sagaId -> status=${again.status}, ledger unchanged:`, JSON.stringify([...ledger]));
