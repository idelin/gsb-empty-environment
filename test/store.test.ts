import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store } from '../src/store.ts';
import type { Operation, CommitFailure } from '../src/store.ts';

const set = (field: string, value: unknown): Operation => ({ kind: 'set', field, value });
const del = (field: string): Operation => ({ kind: 'delete', field });

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Bank-style store: balance lower bound + constant total + conditional credit rule. */
function makeBankStore(counters: { totalEvals?: number[]; expensiveEvals?: number[] } = {}) {
  const store = new Store({
    'bal:a': 100,
    'bal:b': 200,
    mode: 'relaxed',
    used: 0,
    limit: 100,
    note: 'genesis',
    heavy: 0,
  });
  store.defineComputed('totalBalance', (read) => {
    counters.totalEvals?.push(1);
    return Number(read('bal:a')) + Number(read('bal:b'));
  });
  store.registerInvariant('balances-non-negative', (ctx) => {
    const bad = ['bal:a', 'bal:b'].filter((key) => Number(ctx.get(key)) < 0);
    if (bad.length > 0) return `${bad.join(', ')} negative`;
  });
  store.registerInvariant('total-preserved', (ctx) => {
    if (ctx.compute('totalBalance') !== 300) return 'total balance must stay 300';
  });
  store.registerInvariant('total-bounded', (ctx) => {
    if (Number(ctx.compute('totalBalance')) > 1000) return 'total balance too large';
  });
  store.registerInvariant('credit-limit', (ctx) => {
    if (ctx.get('mode') === 'strict' && Number(ctx.get('used')) > Number(ctx.get('limit'))) {
      return 'used exceeds limit';
    }
  });
  store.registerInvariant('expensive-report', (ctx) => {
    counters.expensiveEvals?.push(1);
    ctx.get('heavy');
    if (Number(ctx.get('heavy')) < 0) return 'heavy is negative';
  });
  return store;
}

test('registers invariants and commits a valid transaction', () => {
  const store = makeBankStore();
  const result = store
    .begin()
    .set('bal:a', 80)
    .set('bal:b', 220)
    .commit();
  assert.equal(result.ok, true);
  assert.equal(store.getState()['bal:a'], 80);
  assert.equal(store.getState()['bal:b'], 220);
  assert.equal(store.history().length, 1);
});

test('rejects invariant registered over an invalid initial state', () => {
  const store = new Store({ x: -1 });
  assert.throws(
    () => store.registerInvariant('positive', (ctx) => (Number(ctx.get('x')) > 0 ? undefined : 'x <= 0')),
    /does not hold/,
  );
});

test('commit only checks affected rules; unrelated fields skip expensive rules', () => {
  const expensiveEvals: number[] = [];
  const store = makeBankStore({ expensiveEvals });
  assert.equal(expensiveEvals.length, 1); // registration evaluates once

  const ok = store.commitOps([set('note', 'unrelated')]);
  assert.equal(ok.ok, true);
  assert.equal(expensiveEvals.length, 1, 'expensive rule must not run for unrelated fields');

  const ok2 = store.commitOps([set('heavy', 5)]);
  assert.equal(ok2.ok, true);
  assert.equal(expensiveEvals.length, 2, 'expensive rule runs when its dependency changes');
});

test('multiple invariants share one pure computation per commit', () => {
  const totalEvals: number[] = [];
  const store = makeBankStore({ totalEvals });
  totalEvals.length = 0; // ignore registration passes

  const ok = store.commitOps([set('bal:a', 90), set('bal:b', 210)]);
  assert.equal(ok.ok, true);
  assert.equal(totalEvals.length, 1, 'two rules using totalBalance share a single evaluation');
});

test('balance lower bound and sum constraint are checked together', () => {
  const store = makeBankStore();
  // Violates both: bal:a negative AND total 250 != 300.
  const failure = store.commitOps([set('bal:a', -50), set('bal:b', 300)]) as CommitFailure;
  assert.equal(failure.ok, false);
  const rules = failure.violations.map((v) => v.rule).sort();
  assert.deepEqual(rules, ['balances-non-negative', 'total-preserved']);
  assert.ok(failure.fields.includes('bal:a'));
  assert.ok(failure.fields.includes('bal:b'));
  // State untouched.
  assert.equal(store.getState()['bal:a'], 100);
  assert.equal(store.getState()['bal:b'], 200);
});

test('failure reports involved fields and a minimal replayable counterexample', () => {
  const store = makeBankStore();
  const ops = [
    set('note', 'irrelevant'), // unrelated noise
    set('bal:a', -20), // the actual culprit
    set('bal:b', 320), // keeps the total valid
  ];
  const failure = store.commitOps(ops) as CommitFailure;
  assert.equal(failure.ok, false);
  assert.deepEqual(failure.violations.map((v) => v.rule), ['balances-non-negative']);
  assert.deepEqual(failure.fields, ['bal:a', 'bal:b']);

  // Minimal subset: only the culprit operation remains.
  assert.deepEqual(failure.counterexample.ops, [set('bal:a', -20)]);
  assert.deepEqual(failure.counterexample.violates, ['balances-non-negative']);

  // 1-minimality: removing any remaining op no longer fails.
  for (let i = 0; i < failure.counterexample.ops.length; i++) {
    const rest = failure.counterexample.ops.filter((_, j) => j !== i);
    const check = store.dryRun(rest, 'full');
    assert.equal(
      check.violations.some((v) => v.rule === 'balances-non-negative'),
      false,
    );
  }

  // Replayable against the (unchanged) committed state: the original
  // violation is reproduced (the isolated op may trip further rules too).
  const replayed = failure.replay() as CommitFailure;
  assert.equal(replayed.ok, false);
  assert.ok(replayed.violations.some((v) => v.rule === 'balances-non-negative'));
});

test('rejected commit leaves no new dependency edges or half-updated state', () => {
  const store = makeBankStore();
  const depsBefore = Object.fromEntries(
    ['balances-non-negative', 'total-preserved', 'total-bounded', 'credit-limit', 'expensive-report']
      .map((n) => [n, store.depsOf(n)]),
  );
  const stateBefore = store.getState();
  const historyBefore = store.history().length;

  const failure = store.commitOps([set('mode', 'strict'), set('used', 150)]);
  assert.equal(failure.ok, false);

  assert.equal(store.getState(), stateBefore, 'state object identity preserved on rejection');
  assert.equal(store.history().length, historyBefore);
  for (const [name, deps] of Object.entries(depsBefore)) {
    assert.deepEqual(store.depsOf(name), deps, `deps of ${name} unchanged`);
  }
});

test('conditional branches update actual dependencies on the draft before validation', () => {
  const store = makeBankStore();
  // In relaxed mode the credit rule only depends on `mode`.
  assert.deepEqual(store.depsOf('credit-limit'), ['mode']);

  // Flipping to strict mode with used <= limit commits; deps are refreshed.
  const ok = store.commitOps([set('mode', 'strict'), set('used', 50)]);
  assert.equal(ok.ok, true);
  assert.deepEqual(store.depsOf('credit-limit'), ['limit', 'mode', 'used']);

  // With stale deps ({mode}) this change would be missed; updated deps catch it.
  const failure = store.commitOps([set('used', 150)]) as CommitFailure;
  assert.equal(failure.ok, false);
  assert.deepEqual(failure.violations.map((v) => v.rule), ['credit-limit']);
  assert.ok(failure.fields.includes('used'));
  assert.ok(failure.fields.includes('limit'));

  // Deps survive the rejection unchanged (still the committed strict-mode set).
  assert.deepEqual(store.depsOf('credit-limit'), ['limit', 'mode', 'used']);
});

test('a transaction that flips the branch and violates the rule is rejected atomically', () => {
  const store = makeBankStore();
  const failure = store.commitOps([set('mode', 'strict'), set('used', 150)]) as CommitFailure;
  assert.equal(failure.ok, false);
  assert.deepEqual(failure.violations.map((v) => v.rule), ['credit-limit']);
  assert.equal(store.getState().mode, 'relaxed');
  assert.deepEqual(store.depsOf('credit-limit'), ['mode']);
});

test('a throwing rule fails the commit and leaves state untouched', () => {
  const store = new Store({ trigger: 'safe', other: 1 });
  store.registerInvariant('may-throw', (ctx) => {
    if (ctx.get('trigger') === 'bang') throw new Error('rule exploded');
  });
  const failure = store.commitOps([set('trigger', 'bang')]) as CommitFailure;
  assert.equal(failure.ok, false);
  assert.equal(failure.violations.length, 1);
  assert.equal(failure.violations[0].rule, 'may-throw');
  assert.equal(failure.violations[0].message, 'rule exploded');
  assert.ok(failure.violations[0].error instanceof Error);
  assert.equal(store.getState().trigger, 'safe');
});

test('readers never observe transaction intermediate state', () => {
  const store = new Store({ x: 1, y: 1 });
  const observedDuringRule: unknown[] = [];
  store.registerInvariant('spy', (ctx) => {
    ctx.get('x');
    observedDuringRule.push(store.getState().x); // external read during validation
  });
  observedDuringRule.length = 0; // ignore registration pass

  const snapshots: unknown[] = [];
  store.subscribe((s) => snapshots.push([s.x, s.y]));

  const tx = store.begin().set('x', 10).set('y', 20);
  assert.equal(store.getState().x, 1, 'uncommitted ops are invisible');
  const result = tx.commit();
  assert.equal(result.ok, true);

  assert.deepEqual(observedDuringRule, [1], 'rules see committed state via store, draft via ctx');
  assert.deepEqual(snapshots, [[10, 20]], 'subscribers see exactly one final snapshot');
  assert.equal(store.getState().x, 10);
});

test('committed snapshots are frozen for readers', () => {
  const store = new Store({ x: 1 });
  store.commitOps([set('x', 2)]);
  assert.throws(() => {
    (store.getState() as Record<string, unknown>).x = 99;
  }, TypeError);
  assert.equal(store.getState().x, 2);
});

test('subscribers cannot mask illegal commits with post-hoc fixes', () => {
  const store = new Store({ x: 1 });
  store.registerInvariant('positive', (ctx) =>
    Number(ctx.get('x')) > 0 ? undefined : 'x must be positive',
  );
  const seen: number[] = [];
  store.subscribe((s) => {
    seen.push(Number(s.x));
    // A "fix-up" commit from inside a subscriber is forbidden.
    assert.throws(() => store.commitOps([set('x', 999)]), /subscriber/);
  });

  const bad = store.commitOps([set('x', -5)]) as CommitFailure;
  assert.equal(bad.ok, false);
  assert.equal(store.getState().x, 1);
  assert.deepEqual(seen, [], 'rejected commits never notify');

  const good = store.commitOps([set('x', 2)]);
  assert.equal(good.ok, true);
  assert.deepEqual(seen, [2]);
});

test('operation log replays to the same state', () => {
  const configure = (s: Store) => {
    s.registerInvariant('non-negative', (ctx) =>
      Number(ctx.get('n') ?? 0) >= 0 ? undefined : 'n < 0',
    );
  };
  const store = new Store({ n: 0, tag: 'a' });
  configure(store);
  assert.equal(store.commitOps([set('n', 5)]).ok, true);
  assert.equal(store.commitOps([set('tag', 'b'), set('n', 7)]).ok, true);
  assert.equal(store.commitOps([del('tag')]).ok, true);
  assert.equal(store.commitOps([set('n', -1)]).ok, false); // rejected: not logged

  const replayed = Store.replay(store.history(), configure, { n: 0, tag: 'a' });
  assert.deepEqual(replayed.getState(), store.getState());
  assert.equal(replayed.history().length, 3);
});

test('small transactions: incremental checking matches full re-validation (exhaustive)', () => {
  const store = makeBankStore();
  const fields: [string, unknown[]][] = [
    ['bal:a', [0, 50, 100, -10]],
    ['bal:b', [0, 200, 300]],
    ['mode', ['relaxed', 'strict']],
    ['used', [0, 50, 150]],
    ['limit', [100, 200]],
  ];
  const singles: Operation[][] = [];
  for (const [field, values] of fields) {
    for (const value of values) singles.push([set(field, value)]);
  }
  const cases: Operation[][] = [...singles];
  for (const a of singles) for (const b of singles) cases.push([...a, ...b]);

  const names = (r: { violations: readonly { rule: string }[] }) =>
    r.violations.map((v) => v.rule).sort();

  for (const ops of cases) {
    const inc = names(store.dryRun(ops, 'incremental'));
    const full = names(store.dryRun(ops, 'full'));
    assert.deepEqual(inc, full, `mismatch for ${JSON.stringify(ops)}`);
  }
});

test('small transactions: randomized differential run with evolving state', () => {
  const store = makeBankStore();
  const rand = mulberry32(42);
  const pick = <T>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)];
  const names = (r: { violations: readonly { rule: string }[] }) =>
    r.violations.map((v) => v.rule).sort();

  for (let i = 0; i < 500; i++) {
    const size = 1 + Math.floor(rand() * 4);
    const ops: Operation[] = [];
    for (let j = 0; j < size; j++) {
      const field = pick(['bal:a', 'bal:b', 'mode', 'used', 'limit', 'note', 'heavy'] as const);
      let value: unknown;
      if (field === 'mode') value = pick(['relaxed', 'strict'] as const);
      else if (field === 'note') value = `n${i}`;
      else value = pick([-50, 0, 25, 50, 100, 150, 200, 300] as const);
      ops.push(set(field, value));
    }
    const inc = names(store.dryRun(ops, 'incremental'));
    const full = names(store.dryRun(ops, 'full'));
    assert.deepEqual(inc, full, `mismatch at iteration ${i}: ${JSON.stringify(ops)}`);

    if (inc.length === 0) {
      const committed = store.commitOps(ops);
      assert.equal(committed.ok, true);
      // Dependency edges must always equal a fresh full tracking of the state.
      const fresh = store.freshDeps();
      for (const [rule, deps] of Object.entries(fresh)) {
        assert.deepEqual(store.depsOf(rule), deps, `stale deps for ${rule} at iteration ${i}`);
      }
    } else {
      const rejected = store.commitOps(ops);
      assert.equal(rejected.ok, false);
    }
  }
});
