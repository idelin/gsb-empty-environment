import { Store } from '../src/store.ts';

const Phase = { Idle: 'idle', Loading: 'loading', Ready: 'ready' } as const;
type Phase = (typeof Phase)[keyof typeof Phase];

type AppState = {
  phase: Phase;
  balance: number;
  escrow: number;
  total: number;
  ops: number;
};

const initial: AppState = { phase: Phase.Idle, balance: 100, escrow: 0, total: 100, ops: 0 };
const store = new Store(initial);

let notifications = 0;
store.subscribe((s) => {
  notifications += 1;
  console.log(`  [notify #${notifications}]`, JSON.stringify(s));
});

console.log('== 1. one scheduling cycle: mixed ops merged into a single commit ==');
store.batch((tx) => {
  tx.set('phase', Phase.Loading); // enum assignment
  tx.update('balance', (b) => b + 1); // functional update
  tx.update('balance', (b) => b + 1); // consecutive x=>x+1 adds 2 in total
  tx.update('total', (t) => t + 2); // keep the conservation invariant true
  tx.update('escrow', (e, d) => e + Math.min(40, d.balance)); // read-then-write across fields
  tx.update('balance', (b) => b - 40);
  tx.updateIf('ops', (d) => d.escrow > 0, (n) => n + 1); // conditional update on the draft
  tx.set('phase', Phase.Ready); // enum assignment interleaved
  tx.assert((d) => d.balance + d.escrow === d.total, 'funds conserved'); // cross-field invariant
});
console.log('  state:', JSON.stringify(store.getState()), '| notifications:', notifications);

console.log('\n== 2. nested batches: only the outermost notifies ==');
const beforeNested = notifications;
store.batch((tx) => {
  tx.update('ops', (n) => n + 1);
  store.batch((inner) => inner.update('ops', (n) => n + 1));
});
console.log('  ops:', store.getState().ops, '| notifications in this step:', notifications - beforeNested);

console.log('\n== 3. cancel one op; later independent updates survive ==');
store.batch((tx) => {
  const doomed = tx.set('balance', 999);
  tx.update('ops', (n) => n + 10); // independent, must still apply
  doomed.cancel();
});
console.log('  balance:', store.getState().balance, '(cancelled, unchanged) | ops:', store.getState().ops);

console.log('\n== 4. idempotent retry by operation ID ==');
const pay = () =>
  store.batch((tx) => tx.update('balance', (b) => b - 5, { opId: 'payment-42' }));
pay();
pay(); // retry: skipped
pay(); // retry: skipped
console.log('  balance after 3 identical retries:', store.getState().balance, '(debited once)');

console.log('\n== 5. failed batch: invariant violated, no partial values ==');
try {
  store.batch((tx) => {
    tx.set('escrow', 1000);
    tx.update('balance', (b) => b - 1000);
    tx.assert((d) => d.balance >= 0, 'no overdraft');
  });
} catch (err) {
  console.log('  rolled back:', (err as Error).message);
}
console.log('  state still:', JSON.stringify(store.getState()));

console.log('\n== 6. replay the operation log from the initial snapshot ==');
const replayed = Store.replay(initial, store.log);
console.log('  replayed:', JSON.stringify(replayed));
console.log('  matches live state:', JSON.stringify(replayed) === JSON.stringify(store.getState()));
console.log('  log entries:', store.log.length, '| commits:', store.commits, '| notifications:', notifications);
