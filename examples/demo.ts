import {
  Migrator,
  SchemaRegistry,
  Store,
  UnknownMigrationChainError,
  replayJournal,
  replayVersion,
} from '../src/index.ts';
import type { State } from '../src/index.ts';

function line(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// --- Schema registry: explicit migrations v1 -> v2 -> v3 -------------------
const registry = new SchemaRegistry();
for (const version of ['v1', 'v2', 'v3']) registry.registerVersion(version);

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

// --- Store + subscriptions --------------------------------------------------
const genesis = { user: { fullName: 'Ada Lovelace' }, counter: 0 };
const store = new Store(genesis, 'v1');

store.bind('user.fullName', (next, previous) => {
  console.log(`[binding user name] ${String(previous)} -> ${String(next)}`);
});
store.subscribe(
  (s) => s.counter,
  (next, previous) => console.log(`[counter] ${String(previous)} -> ${String(next)}`),
);
store.subscribe(
  (s) => (s.settings as Record<string, unknown> | undefined)?.theme,
  (next, previous) => console.log(`[theme] ${String(previous)} -> ${String(next)}`),
);

// --- Transactional writes: readers never see intermediate states ------------
line('transaction (reader sees only committed state)');
store.transaction((tx) => {
  tx.set('counter', 1);
  console.log(`  reader inside tx sees counter=${String(store.get('counter'))}`);
  tx.set('counter', 2);
});
console.log(`  committed counter=${String(store.get('counter'))}`);

// --- Explicit migration with paused subscriptions ---------------------------
line('migrate v1 -> v3 (subscriptions paused, single publish)');
const migrator = new Migrator(registry);
const result = migrator.migrate(store, 'v3');
console.log(`  result: ${JSON.stringify(result)}`);
console.log(`  schema=${store.schemaVersion} state=${JSON.stringify(store.getState())}`);

line('renamed field binding still works');
store.transaction((tx) => tx.set('user.name', 'Grace Hopper'));

// --- Failed migration rolls back --------------------------------------------
line('failed migration v3 -> v4 (rollback)');
registry.registerVersion('v4');
registry.addMigration({
  from: 'v3',
  to: 'v4',
  up(state: State) {
    state.poisoned = true;
    throw new Error('simulated v4 failure');
  },
});
try {
  migrator.migrate(store, 'v4');
} catch (error) {
  console.log(`  caught: ${(error as Error).message}`);
}
console.log(`  schema=${store.schemaVersion} poisoned=${String(store.get('poisoned'))}`);

// --- Unknown migration chain is rejected ------------------------------------
line('unknown chain v3 -> v9 (rejected)');
try {
  migrator.migrate(store, 'v9');
} catch (error) {
  if (error instanceof UnknownMigrationChainError) {
    console.log(`  rejected: ${error.message}`);
  }
}
console.log(`  schema=${store.schemaVersion} state=${JSON.stringify(store.getState())}`);

// --- Audit trail -------------------------------------------------------------
line('audit trail (version mapping)');
for (const entry of migrator.audit.entries()) {
  const renames = Object.entries(entry.renames)
    .map(([from, to]) => ` ${from}=>${to}`)
    .join('');
  console.log(
    `  #${entry.seq} ${entry.kind} ${entry.fromVersion}->${entry.toVersion}${renames}${entry.detail ? ` (${entry.detail})` : ''}`,
  );
}

// --- Replay ------------------------------------------------------------------
line('journal replay');
const replayed = replayJournal(genesis, store.journal.entries());
const version = replayVersion('v1', store.journal.entries());
const equal = JSON.stringify(replayed) === JSON.stringify(store.getState());
console.log(`  journal entries: ${store.journal.length}`);
console.log(`  replayed version: ${version} (live: ${store.schemaVersion})`);
console.log(`  replayed state equals live state: ${String(equal)}`);
