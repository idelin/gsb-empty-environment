import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  Migrator,
  SchemaRegistry,
  Store,
  replayJournal,
  replayVersion,
} from '../src/index.ts';
import type { State } from '../src/index.ts';

test('journal replays every operation back to the live state', () => {
  const genesis = { user: { fullName: 'Ada' }, counter: 0, tags: ['a'] };
  const store = new Store(genesis, 'v1');

  const registry = new SchemaRegistry();
  registry.registerVersion('v1');
  registry.registerVersion('v2');
  registry.addMigration({
    from: 'v1',
    to: 'v2',
    renames: { 'user.fullName': 'user.name' },
    up(state: State) {
      const user = state.user as Record<string, unknown>;
      user.name = user.fullName;
      delete user.fullName;
      return state;
    },
  });

  store.transaction((tx) => {
    tx.set('counter', 1);
    tx.set('user.fullName', 'Ada Lovelace');
  });
  store.transaction((tx) => {
    tx.set('counter', 2);
    tx.delete('tags');
  });
  new Migrator(registry).migrate(store, 'v2');
  store.transaction((tx) => tx.set('user.name', 'Grace'));

  const replayed = replayJournal(genesis, store.journal.entries());
  assert.deepEqual(replayed, store.getState());
  assert.equal(replayVersion('v1', store.journal.entries()), 'v2');
});

test('replayed state is independent from the live store', () => {
  const genesis = { counter: 0 };
  const store = new Store(genesis, 'v1');
  store.transaction((tx) => tx.set('counter', 41));

  const replayed = replayJournal(genesis, store.journal.entries());
  store.transaction((tx) => tx.set('counter', 42));

  assert.deepEqual(replayed, { counter: 41 });
  assert.equal(store.get('counter'), 42);
});
