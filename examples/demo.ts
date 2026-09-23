import { Store, deepEqual } from '../src/index.ts';

interface AppState {
  count: number;
  notes: string[];
}

const store = new Store<AppState>({ count: 0, notes: [] }, { roundBudget: 16 });

console.log('--- subscribers (notified in subscription order) ---');

// alpha: reacts to count, writes reentrantly (next round), twice -> forces a merge
store.subscribe(
  (s) => s.count,
  (count, meta) => {
    console.log(`alpha  count=${count}  round=${meta.round}  v${meta.fromVersion}->v${meta.toVersion}`);
    if (count === 1) {
      store.transact('alpha-note', (tx) => tx.set(['notes'], ['alpha saw 1']));
      store.transact('alpha-bump', (tx) => tx.set(['count'], 2));
    }
  },
);

// beta: throws once; must not block gamma, error gets aggregated
store.subscribe(
  (s) => s.count,
  (count, meta) => {
    if (count === 2) throw new Error('beta exploded on 2');
    console.log(`beta   count=${count}  round=${meta.round}`);
  },
);

// gamma: depends on notes via deepEqual
store.subscribe(
  (s) => s.notes,
  (notes, meta) => console.log(`gamma  notes=${JSON.stringify(notes)}  round=${meta.round}  v${meta.toVersion}`),
  { equality: deepEqual },
);

console.log('\n--- transaction: count = 1 (cascades into further rounds) ---');
store.transact('seed', (tx) => tx.set(['count'], 1));

console.log('\n--- dynamic selector swap: delta switches dependency to parity ---');
const delta = store.subscribe(
  (s) => s.count,
  (v, meta) => console.log(`delta  selected=${v}  round=${meta.round}`),
);
delta.setSelector((s: unknown) => (s as AppState).count % 2);
store.transact('odd', (tx) => tx.set(['count'], 5));
store.transact('even', (tx) => tx.set(['count'], 8));

console.log('\n--- backpressure merge evidence ---');
for (const m of store.getMergeEvidence()) {
  console.log(`round ${m.round}: merged commits [${m.mergedCommits.join(', ')}]  range v${m.fromVersion}->v${m.toVersion}`);
}

console.log('\n--- aggregated subscriber errors ---');
for (const e of store.getSubscriberErrors()) {
  console.log(`round ${e.round} v${e.version} subscriber=${e.subscriberId} kind=${e.kind}: ${(e.error as Error).message}`);
}

console.log('\n--- replay verification (state exploration) ---');
const { state, versions } = store.replay();
const unique = new Set(versions).size === versions.length;
const finalKept = JSON.stringify(state) === JSON.stringify(store.getState());
console.log(`replayed versions: [${versions.join(', ')}]  unique=${unique}`);
console.log(`final state preserved=${finalKept}  state=${JSON.stringify(state)}`);
