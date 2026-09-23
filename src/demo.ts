import { Store, UnknownMigrationChainError } from './store.ts';
import { v1_to_v2, v2_to_v3, v3_to_v4_broken } from './migrations.ts';

const line = (title: string) => console.log(`\n=== ${title} ===`);

const store = new Store({
  schemaVersion: 1,
  initialState: { user: { name: 'Ada', age: 36 }, cart: [] },
  migrations: [v1_to_v2, v2_to_v3, v3_to_v4_broken],
});

line('1. Subscribe to user.name (selector binding)');
const seen: unknown[] = [];
store.subscribe('user.name', (value) => {
  seen.push(structuredClone(value));
  console.log('  subscriber notified, user.name =', JSON.stringify(value));
});

line('2. Atomic transaction (reader sees only committed state)');
store.transact(() => {
  console.log('  mid-transaction getState().user.age =',
    (store.getState().user as Record<string, unknown>).age, '(still old)');
  return [
    { type: 'set', path: ['user', 'age'], value: 37 },
    { type: 'set', path: ['cart'], value: ['book'] },
  ];
});
console.log('  after commit:', JSON.stringify(store.getState()));

line('3. Migrate v1 -> v3 (explicit chain, subscriptions paused)');
store.migrateTo(3);
console.log('  schemaVersion =', store.schemaVersion);
console.log('  state =', JSON.stringify(store.getState()));
console.log('  notifications during migration:', seen.length,
    '(paused; flush happens once, only if the selected value changed)');

line('4. Selector rebound by field rename');
store.set('user.fullName', 'Ada Lovelace'); // old path user.name is gone
console.log('  subscriber saw:', JSON.stringify(seen));

line('5. Failed migration rolls back schema + state');
try {
  store.migrateTo(4);
} catch (err) {
  console.log('  error:', (err as Error).message);
}
console.log('  schemaVersion =', store.schemaVersion, '(unchanged)');
console.log('  state =', JSON.stringify(store.getState()));

line('6. Unknown migration chain rejected');
try {
  store.migrateTo(99);
} catch (err) {
  console.log('  rejected:', (err as UnknownMigrationChainError).message);
}

line('7. Replayable op log');
console.log('  log entries:', store.getLog().length);
console.log('  replay matches current state:', store.verifyReplay());

line('8. Auditable version mapping');
console.log('  version map:', JSON.stringify(store.getVersionMap()));
console.log('  audit log:');
for (const entry of store.getAuditLog()) {
  console.log('   ', JSON.stringify(entry));
}
