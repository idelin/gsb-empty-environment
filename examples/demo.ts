import { Store } from '../src/store.ts';
import type { Notification } from '../src/store.ts';
import { formatPath } from '../src/path.ts';

function show(title: string, notifications: Notification[]): void {
  console.log(`\n== ${title} ==`);
  for (const n of notifications) {
    console.log(`  sub#${n.subId} ${n.pattern}  ops=[${n.opIds.join(',')}]`);
    for (const p of n.patches) {
      const extra =
        p.kind === 'splice'
          ? ` range=[${p.range![0]}..${p.range![1]}]`
          : p.movedTo
            ? ` movedTo=${formatPath(p.movedTo)}`
            : p.movedFrom
              ? ` movedFrom=${formatPath(p.movedFrom)}`
              : '';
      console.log(`    ${p.kind.padEnd(6)} ${formatPath(p.path)}${extra}`);
    }
  }
}

const initial = {
  users: { u1: { name: 'ada', email: 'a@x' } },
  queue: ['t1', 't2', 't3', 't4'],
};
const store = new Store(initial);

const inbox = new Map<string, Notification[]>();
const watch = (pattern: string): void => {
  inbox.set(pattern, []);
  store.subscribe(pattern, (n) => inbox.get(pattern)!.push(n));
};

watch('/users'); // ancestor: sees every change below /users
watch('/users/*/email'); // single-segment wildcard
watch('/queue'); // array subtree
watch('/archive'); // move target subtree

// One transaction: set + move + array delete (indices shift).
const r1 = store.transact((tx) => {
  tx.set('/users/u1/email', 'ada@new');
  tx.move('/users/u1/name', '/archive/u1-name');
  tx.delete('/queue/1'); // removes 't2', indices 2..3 shift down
});
console.log('state after tx1:', JSON.stringify(store.state));
for (const [pattern, list] of inbox) show(`tx1 -> ${pattern}`, list);
console.log(`tx1 automaton states visited: ${r1!.visited}`);

// Reentrant write from a callback applies at the transaction boundary.
store.subscribe('/users/u1/email', (_n, s) => {
  s.transact((tx) => tx.set('/users/u1/emailVerified', true));
});
store.transact((tx) => tx.set('/users/u1/email', 'ada@final'));
console.log('\nstate after reentrant tx:', JSON.stringify(store.state));

// Replay the journal into a fresh store with the same initial state.
const replica = new Store(initial);
replica.replay(store.ops);
console.log('\nreplay matches:', JSON.stringify(replica.state) === JSON.stringify(store.state));
console.log('replayed state:', JSON.stringify(replica.state));
console.log(`journal size: ${store.ops.length} ops, automaton states: ${store.automatonStates}`);
