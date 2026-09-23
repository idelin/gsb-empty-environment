import { DefinitionRegistry, Store, save, restore, replay } from '../src/index.ts';

const registry = new DefinitionRegistry();
registry.register({ id: 'sum', version: 1, compute: ([a, b]) => (a as number) + (b as number) });
registry.register({ id: 'double', version: 1, compute: ([x]) => (x as number) * 2 });

// 1. Build a graph and transact.
const store = new Store(registry);
store.defineCell('a', 1);
store.defineCell('b', 2);
store.defineComputed('total', { id: 'sum', version: 1 }, ['a', 'b']);
store.defineComputed('grand', { id: 'double', version: 1 }, ['total']);

store.subscribe('grand', (value) => console.log(`  [subscriber] grand -> ${value}`));

console.log('initial grand =', store.read('grand'));
console.log('commit tx: a=10, b=20 (atomic, one notification)');
store.beginTx().set('a', 10).set('b', 20).commit();
console.log('grand after tx =', store.read('grand'));

// 2. Save: pure state + dependency evidence, no function code.
const envelope = save(store);
const json = JSON.stringify(envelope, null, 2);
console.log('\nsaved envelope (no function source inside):');
console.log(json);

// 3. Restore into a fresh store.
const restored = new Store(registry);
const result = restore(restored, JSON.parse(json), registry);
console.log('restore:', result.ok ? 'OK' : result);
console.log('restored grand =', restored.read('grand'));

// 4. Tamper with a dependency edge -> restore rejected, old state intact.
const tampered = JSON.parse(json);
tampered.nodes.find((n: { id: string }) => n.id === 'grand').deps = ['missing'];
const rejected = restore(restored, tampered, registry);
console.log('\ntampered restore:', rejected.ok ? 'unexpectedly OK' : `rejected (${rejected.code})`);
console.log('old state still readable, grand =', restored.read('grand'));

// 5. Replay the operation log into a new store -> identical state.
const clone = replay(store.log, registry);
console.log('\nreplay: grand =', clone.read('grand'), '(matches:', clone.read('grand') === store.read('grand'), ')');
