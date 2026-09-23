import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  Migrator,
  SchemaRegistry,
  Store,
  UnknownMigrationChainError,
} from '../src/index.ts';
import type { State } from '../src/index.ts';

function buildRegistry(): SchemaRegistry {
  const registry = new SchemaRegistry();
  for (const version of ['v1', 'v2', 'v3', 'v4']) {
    registry.registerVersion(version);
  }
  registry.addMigration({
    from: 'v1',
    to: 'v2',
    renames: { 'user.fullName': 'user.name' },
    up(state: State) {
      const user = state.user as Record<string, unknown>;
      user.name = user.fullName;
      delete user.fullName;
      user.email = 'unknown@example.com';
      return state;
    },
  });
  registry.addMigration({
    from: 'v2',
    to: 'v3',
    up(state: State) {
      state.settings = { theme: 'light' };
      return state;
    },
  });
  return registry;
}

test('explicit migration chain v1 -> v3 runs every registered step', () => {
  const store = new Store({ user: { fullName: 'Ada' } }, 'v1');
  const migrator = new Migrator(buildRegistry());

  const result = migrator.migrate(store, 'v3');

  assert.equal(result.status, 'migrated');
  assert.equal(result.steps, 2);
  assert.equal(store.schemaVersion, 'v3');
  assert.deepEqual(store.getState(), {
    user: { name: 'Ada', email: 'unknown@example.com' },
    settings: { theme: 'light' },
  });
});

test('subscribers stay paused until the full new state is published', () => {
  const store = new Store({ user: { fullName: 'Ada' }, counter: 0 }, 'v1');

  const observer: string[] = [];
  store.subscribe(
    (s) => (s.settings as Record<string, unknown> | undefined)?.theme,
    (next, previous) => observer.push(`theme: ${String(previous)} -> ${String(next)}`),
  );

  // The v2 -> v3 step observes the store mid-migration: readers must still
  // see the old committed state, and subscriptions must be paused.
  const registry = new SchemaRegistry();
  for (const version of ['v1', 'v2', 'v3']) {
    registry.registerVersion(version);
  }
  registry.addMigration(buildRegistry().findPath('v1', 'v2')![0]);
  registry.addMigration({
    from: 'v2',
    to: 'v3',
    up(state: State) {
      observer.push(
        `mid-migration paused=${String(store.subscriptionsPaused)} version=${store.schemaVersion} theme=${String(store.get('settings.theme'))}`,
      );
      state.settings = { theme: 'light' };
      return state;
    },
  });

  const migrator = new Migrator(registry);
  const notificationsBefore = observer.length;
  migrator.migrate(store, 'v3');

  assert.equal(notificationsBefore, 0);
  assert.ok(observer.some((line) => line.includes('paused=true')));
  assert.ok(observer.some((line) => line.includes('version=v1')));
  assert.ok(observer.some((line) => line.includes('theme=undefined')));
  assert.deepEqual(observer.slice(-1), ['theme: undefined -> light']);
});

test('field rename updates existing selector bindings', () => {
  const store = new Store({ user: { fullName: 'Ada' } }, 'v1');
  const events: unknown[] = [];
  store.bind('user.fullName', (next) => events.push(next));

  const migrator = new Migrator(buildRegistry());
  migrator.migrate(store, 'v2');

  // Old field is gone; the binding now tracks user.name.
  assert.equal(store.get('user.fullName'), undefined);
  store.transaction((tx) => tx.set('user.name', 'Grace'));
  assert.deepEqual(events, ['Grace']);
  assert.equal(store.get('user.name'), 'Grace');
});

test('failed migration restores old schema, state and bindings', () => {
  const registry = buildRegistry();
  registry.addMigration({
    from: 'v3',
    to: 'v4',
    up(state: State) {
      state.poisoned = true;
      throw new Error('v4 migration exploded');
    },
  });
  const store = new Store({ user: { fullName: 'Ada' }, counter: 1 }, 'v1');
  const events: string[] = [];
  store.bind('user.fullName', () => events.push('name'));
  store.subscribe(
    (s) => s.counter,
    () => events.push('counter'),
  );
  const migrator = new Migrator(registry);
  migrator.migrate(store, 'v3');
  events.length = 0;

  assert.throws(
    () => migrator.migrate(store, 'v4'),
    /v4 migration exploded/,
  );

  assert.equal(store.schemaVersion, 'v3');
  assert.equal(store.get('poisoned'), undefined);
  assert.deepEqual(store.get('settings'), { theme: 'light' });
  const failed = migrator.audit
    .entries()
    .filter((entry) => entry.kind === 'migrate-failed');
  assert.equal(failed.length, 1);
  assert.equal(failed[0].fromVersion, 'v3');
  assert.equal(failed[0].toVersion, 'v4');

  // Bindings still follow v3 fields; no spurious notifications occurred.
  store.transaction((tx) => tx.set('user.name', 'Grace'));
  assert.deepEqual(events, ['name']);
});

test('unknown migration chain is rejected without touching state', () => {
  const store = new Store({ user: { fullName: 'Ada' } }, 'v1');
  const migrator = new Migrator(buildRegistry());

  assert.throws(
    () => migrator.migrate(store, 'v9'),
    UnknownMigrationChainError,
  );
  assert.equal(store.schemaVersion, 'v1');
  assert.deepEqual(store.getState(), { user: { fullName: 'Ada' } });
  assert.equal(store.journal.length, 0);

  const rejected = migrator.audit
    .entries()
    .filter((entry) => entry.kind === 'migrate-rejected');
  assert.equal(rejected.length, 1);
  assert.equal(rejected[0].toVersion, 'v9');
});

test('version mapping is fully auditable', () => {
  const store = new Store({ user: { fullName: 'Ada' } }, 'v1');
  const migrator = new Migrator(buildRegistry());
  migrator.migrate(store, 'v3');

  const steps = migrator.audit
    .entries()
    .filter((entry) => entry.kind === 'migrate-step');
  assert.deepEqual(
    steps.map((entry) => [entry.fromVersion, entry.toVersion]),
    [
      ['v1', 'v2'],
      ['v2', 'v3'],
    ],
  );
  assert.deepEqual(steps[0].renames, { 'user.fullName': 'user.name' });
  assert.ok(steps.every((entry) => typeof entry.seq === 'number' && entry.at));
});
