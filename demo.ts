import { Store } from './src/store.ts';
import type { CommitResult } from './src/store.ts';

let totalEvals = 0;
let expensiveEvals = 0;

const store = new Store({
  'bal:alice': 100,
  'bal:bob': 200,
  mode: 'relaxed',
  'used:alice': 0,
  'limit:alice': 100,
  note: 'genesis',
  heavy: 0,
});

// Shared pure computation, memoized once per validation pass.
store.defineComputed('totalBalance', (read) => {
  totalEvals += 1;
  return Number(read('bal:alice')) + Number(read('bal:bob'));
});

// Composite numeric lower bound on every balance.
store.registerInvariant('balances-non-negative', (ctx) => {
  const bad = ['bal:alice', 'bal:bob'].filter((key) => Number(ctx.get(key)) < 0);
  if (bad.length > 0) return `${bad.join(', ')} negative`;
});
// Sum constraint over the same fields, sharing totalBalance.
store.registerInvariant('total-preserved', (ctx) => {
  if (ctx.compute('totalBalance') !== 300) return 'total balance must stay 300';
});
store.registerInvariant('total-bounded', (ctx) => {
  if (Number(ctx.compute('totalBalance')) > 1000) return 'total balance too large';
});
// Conditional dependencies: only reads used/limit in strict mode.
store.registerInvariant('credit-limit', (ctx) => {
  if (ctx.get('mode') === 'strict' && Number(ctx.get('used:alice')) > Number(ctx.get('limit:alice'))) {
    return 'used exceeds limit';
  }
});
// "Expensive" rule that must not run for unrelated fields.
store.registerInvariant('expensive-report', (ctx) => {
  expensiveEvals += 1;
  if (Number(ctx.get('heavy')) < 0) return 'heavy is negative';
});
// A rule that throws when triggered.
store.registerInvariant('no-boom', (ctx) => {
  if (ctx.get('note') === 'boom') throw new Error('rule exploded');
});

const show = (label: string, result: CommitResult) => {
  if (result.ok) {
    console.log(`${label}: OK`);
  } else {
    console.log(`${label}: REJECTED`);
    for (const v of result.violations) {
      console.log(`  violation[${v.rule}]: ${v.message} (fields: ${v.fields.join(', ')})`);
    }
    console.log(`  involved fields: ${result.fields.join(', ')}`);
    console.log(`  minimal counterexample: ${JSON.stringify(result.counterexample.ops)}`);
    console.log(`  counterexample replays: ${result.replay().ok === false ? 'reproduced' : 'NOT reproduced'}`);
  }
};

console.log('--- 1. valid transfer (both balance rules checked together) ---');
totalEvals = 0;
show('transfer 50 alice->bob', store.begin().set('bal:alice', 50).set('bal:bob', 250).commit());
console.log(`  totalBalance evaluations for 2 rules in one commit: ${totalEvals}`);

console.log('\n--- 2. illegal commit: negative balance + noisy unrelated op ---');
show(
  'overdraft with noise',
  store.commitOps([
    { kind: 'set', field: 'note', value: 'noise' },
    { kind: 'set', field: 'bal:alice', value: -20 },
    { kind: 'set', field: 'bal:bob', value: 320 },
  ]),
);
console.log(`  state after rejection: ${JSON.stringify(store.getState())}`);

console.log('\n--- 3. dynamic dependencies: stale deps must not miss checks ---');
console.log(`  deps[credit-limit] in relaxed mode: ${store.depsOf('credit-limit')}`);
show('switch to strict, used=50', store.commitOps([
  { kind: 'set', field: 'mode', value: 'strict' },
  { kind: 'set', field: 'used:alice', value: 50 },
]));
console.log(`  deps[credit-limit] after branch flip: ${store.depsOf('credit-limit')}`);
show('used=150 (only caught via refreshed deps)', store.commitOps([
  { kind: 'set', field: 'used:alice', value: 150 },
]));

console.log('\n--- 4. unrelated field does not trigger the expensive rule ---');
const before = expensiveEvals;
show('touch note', store.commitOps([{ kind: 'set', field: 'note', value: 'hello' }]));
console.log(`  expensive-report evaluations: ${before} -> ${expensiveEvals}`);

console.log('\n--- 5. a throwing rule fails the commit ---');
show('note=boom', store.commitOps([{ kind: 'set', field: 'note', value: 'boom' }]));

console.log('\n--- 6. subscribers cannot patch over illegal commits ---');
store.subscribe((snapshot) => {
  console.log(`  subscriber sees committed snapshot: note=${snapshot.note}`);
  try {
    store.commitOps([{ kind: 'set', field: 'note', value: 'sneaky-fix' }]);
  } catch (err) {
    console.log(`  fix-up commit from subscriber blocked: ${(err as Error).message}`);
  }
});
show('valid note update (notifies once)', store.commitOps([{ kind: 'set', field: 'note', value: 'final' }]));

console.log('\n--- 7. replay the committed operation log ---');
const replayed = Store.replay(store.history(), (s) => {
  s.defineComputed('totalBalance', (read) => Number(read('bal:alice')) + Number(read('bal:bob')));
  s.registerInvariant('balances-non-negative', (ctx) => {
    const bad = ['bal:alice', 'bal:bob'].filter((key) => Number(ctx.get(key)) < 0);
    if (bad.length > 0) return `${bad.join(', ')} negative`;
  });
  s.registerInvariant('total-preserved', (ctx) => {
    if (ctx.compute('totalBalance') !== 300) return 'total balance must stay 300';
  });
  s.registerInvariant('total-bounded', (ctx) => {
    if (Number(ctx.compute('totalBalance')) > 1000) return 'total balance too large';
  });
  s.registerInvariant('credit-limit', (ctx) => {
    if (ctx.get('mode') === 'strict' && Number(ctx.get('used:alice')) > Number(ctx.get('limit:alice'))) {
      return 'used exceeds limit';
    }
  });
  s.registerInvariant('expensive-report', (ctx) => {
    if (Number(ctx.get('heavy')) < 0) return 'heavy is negative';
  });
  s.registerInvariant('no-boom', (ctx) => {
    if (ctx.get('note') === 'boom') throw new Error('rule exploded');
  });
}, {
  'bal:alice': 100,
  'bal:bob': 200,
  mode: 'relaxed',
  'used:alice': 0,
  'limit:alice': 100,
  note: 'genesis',
  heavy: 0,
});
console.log(`  replayed ${replayed.history().length} transactions, state matches: ${
  JSON.stringify(replayed.getState()) === JSON.stringify(store.getState())
}`);
