import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Store,
  UnknownMigrationChainError,
  MigrationConflictError,
  deepEqual,
  type Migration,
  type State,
} from '../src/store.ts';
import { v1_to_v2, v2_to_v3, v3_to_v4_broken } from '../src/migrations.ts';

function makeStore(extra: Migration[] = []) {
  return new Store({
    schemaVersion: 1,
    initialState: { user: { name: 'Ada', age: 36 }, cart: [] as string[] },
    migrations: [v1_to_v2, v2_to_v3, ...extra],
  });
}

test('transaction commits atomically: readers never see intermediate state', () => {
  const store = makeStore();
  const seenDuring: unknown[] = [];
  const notifications: unknown[] = [];
  store.subscribe('user.age', (v) => notifications.push(v));

  store.transact(() => {
    seenDuring.push(store.select('user.age')); // reader inside transaction
    return [
      { type: 'set', path: ['user', 'age'], value: 37 },
      { type: 'set', path: ['cart'], value: ['book'] },
    ];
  });

  assert.deepEqual(seenDuring, [36]); // old snapshot until commit
  assert.equal(store.select('user.age'), 37);
  assert.deepEqual(notifications, [37]); // notified exactly once, after commit
});

test('throwing transaction changes nothing and notifies nobody', () => {
  const store = makeStore();
  let notified = 0;
  store.subscribe('user.age', () => notified++);
  const before = store.getState();
  const logBefore = store.getLog().length;

  assert.throws(() =>
    store.transact(() => {
      throw new Error('boom');
    }),
  );

  assert.equal(store.getState(), before); // same reference, untouched
  assert.equal(store.getLog().length, logBefore);
  assert.equal(notified, 0);
});

test('subscribers are paused during migration and flushed once with full state', () => {
  // chain where the watched field passes through an intermediate value
  const mid: Migration = {
    from: 3,
    to: 4,
    migrate: (s: State) => ({ ...s, marker: 'intermediate' }),
  };
  const fin: Migration = {
    from: 4,
    to: 5,
    migrate: (s: State) => ({ ...s, marker: 'final' }),
  };
  const store = makeStore([mid, fin]);
  const seen: unknown[] = [];
  store.subscribe('marker', (v) => seen.push(v));

  store.migrateTo(5);

  assert.deepEqual(seen, ['final']); // never 'intermediate'
  assert.equal(store.schemaVersion, 5);
});

test('failed migration restores old schema and state, audits rollback', () => {
  const store = makeStore([v3_to_v4_broken]);
  let notified = 0;
  store.subscribe('user.name', () => notified++);
  store.migrateTo(3);
  const stateBefore = structuredClone(store.getState());

  assert.throws(() => store.migrateTo(4), /simulated migration failure/);

  assert.equal(store.schemaVersion, 3);
  assert.ok(deepEqual(store.getState(), stateBefore));
  assert.equal(notified, 0); // no half-state notification
  const audit = store.getAuditLog().at(-1)!;
  assert.equal(audit.status, 'rolled-back');
  assert.equal(audit.requestedTo, 4);
  assert.match(audit.error!, /simulated migration failure/);
});

test('unknown migration chain is rejected before touching state', () => {
  const store = makeStore();
  const stateBefore = store.getState();

  assert.throws(() => store.migrateTo(99), UnknownMigrationChainError);
  assert.equal(store.getState(), stateBefore);
  assert.equal(store.schemaVersion, 1);
  const audit = store.getAuditLog().at(-1)!;
  assert.equal(audit.status, 'rejected');
});

test('field rename rebinds selector subscriptions', () => {
  const store = makeStore();
  const seen: unknown[] = [];
  store.subscribe('user.name', (v) => seen.push(v));

  store.migrateTo(2); // renames user.name -> user.fullName
  store.set('user.fullName', 'Ada Lovelace');

  assert.deepEqual(seen, ['Ada Lovelace']); // rebound selector fired
  assert.equal(store.select('user.name'), undefined);
  assert.equal(store.select('user.fullName'), 'Ada Lovelace');
});

test('rename rebinds nested subscription paths by prefix', () => {
  const renameRoot: Migration = {
    from: 1,
    to: 2,
    renames: { user: 'profile' },
    migrate: (s: State) => ({ profile: s.user }),
  };
  const store = new Store({
    schemaVersion: 1,
    initialState: { user: { name: 'Ada' } },
    migrations: [renameRoot],
  });
  const seen: unknown[] = [];
  store.subscribe('user.name', (v) => seen.push(v));
  store.migrateTo(2);
  store.set('profile.name', 'Grace');
  assert.deepEqual(seen, ['Grace']);
});

test('op log replays to the exact current state across migrations', () => {
  const store = makeStore();
  store.set('user.age', 37);
  store.set('cart', ['book', 'pen']);
  store.migrateTo(3);
  store.set('user.fullName', 'Ada Lovelace');
  store.set('settings.theme', 'dark');

  assert.ok(store.verifyReplay());
  assert.ok(deepEqual(store.replay(), store.getState()));
});

test('audit log records committed chain and renames (version mapping)', () => {
  const store = makeStore();
  store.migrateTo(3);
  const audit = store.getAuditLog().at(-1)!;

  assert.equal(audit.status, 'committed');
  assert.deepEqual(audit.chain, [1, 2, 3]);
  assert.deepEqual(audit.renames, { 'user.name': 'user.fullName' });
  assert.deepEqual(
    store.getVersionMap().map((e) => [e.from, e.to]),
    [
      [1, 2],
      [2, 3],
    ],
  );
});

test('transactions are rejected while a migration is in progress', () => {
  const reentrant: Migration = {
    from: 1,
    to: 2,
    migrate: (s: State) => {
      assert.throws(() => store.set('x', 1), MigrationConflictError);
      return s;
    },
  };
  const store = new Store({
    schemaVersion: 1,
    initialState: {},
    migrations: [reentrant],
  });
  store.migrateTo(2);
  assert.equal(store.schemaVersion, 2);
});

test('subscriber is not notified when selected value is unchanged', () => {
  const store = makeStore();
  let notified = 0;
  store.subscribe('user.name', () => notified++);
  store.set('user.age', 40); // unrelated change
  store.set('user.name', 'Ada'); // same value
  assert.equal(notified, 0);
});

test('duplicate or backwards migrations are rejected at registration', () => {
  const store = makeStore();
  assert.throws(() =>
    store.registerMigration({ from: 1, to: 2, migrate: (s) => s }),
  );
  assert.throws(() =>
    store.registerMigration({ from: 3, to: 2, migrate: (s) => s }),
  );
});
