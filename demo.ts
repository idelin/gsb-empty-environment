import assert from 'node:assert/strict';
import { Store, type Notification } from './src/store.ts';
import { Automaton } from './src/automaton.ts';
import { matchPatternRefCounted } from './src/reference.ts';

function show(label: string, n: Notification): void {
  const ops = n.ops.map(o => `#${o.id}:${o.kind}`).join(', ');
  const paths = n.changes.map(c =>
    c.oldPath ? `${c.kind} /${c.oldPath.join('/')} -> /${c.path.join('/')}` : `${c.kind} /${c.path.join('/')}`,
  );
  console.log(`  -> [${label}] pattern '${n.pattern}'`);
  console.log(`     ops:     [${ops}]  (de-duplicated, commit order)`);
  console.log(`     changes: ${paths.join(' | ')}`);
}

console.log('=== 1. Subscriptions: exact (subtree), single-segment *, multi-level ** ===');
const initial = {
  users: { alice: { name: 'Alice', tags: ['red', 'green', 'blue'] }, bob: { name: 'Bob' } },
  config: { theme: 'dark' },
};
const store = new Store(initial);
store.subscribe('/users/alice', n => show('alice subtree', n));
store.subscribe('/users/*/name', n => show('any user name', n));
store.subscribe('/config/**', n => show('config', n));

console.log('\n=== 2. Parent path receives child changes ===');
store.tx(t => t.set('/users/alice/name', 'Alicia'));

console.log('\n=== 3. Move notifies old AND new paths; duplicate matches notify once ===');
store.subscribe('/users', n => show('all users', n)); // matches both ends of the move
store.tx(t => t.move('/users/bob', '/users/alice/friend'));

console.log('\n=== 4. Array element delete: explicit index-shift scope ===');
store.subscribe('/users/alice/tags/1', n => show('tag[1]', n));
store.tx(t => t.delete('/users/alice/tags/0'));
console.log(`     tags now: ${JSON.stringify(store.get('/users/alice/tags'))}`);

console.log('\n=== 5. Reentrant write from a notification callback (commits after delivery) ===');
const audit = new Store({ doc: { v: 1 }, log: [] as string[] });
audit.subscribe('/doc', () => {
  console.log(`  -> [/doc] fired, /log length=${(audit.get('/log') as unknown[]).length}`);
  if ((audit.get('/log') as unknown[]).length === 0) {
    audit.tx(t => t.set('/log/0', 'doc changed')); // queued, commits after this round
  }
  console.log(`  -> [/doc] during callback /log length still=${(audit.get('/log') as unknown[]).length}`);
});
audit.subscribe('/log', n => show('audit log', n));
audit.tx(t => t.set('/doc/v', 2));

console.log('\n=== 6. Reference verification (independent per-subscription matcher) ===');
const errors = store.verifyLastCommit();
console.log(`  last commit of main store: ${errors.length === 0 ? 'OK — automaton and reference agree' : errors.join('; ')}`);

console.log('\n=== 7. Shared automaton vs per-subscription matching: traversed states ===');
const shared = Array.from({ length: 100 }, (_, i) => `seg${i}`);
const auto = new Automaton();
const patterns: string[][] = [];
for (let k = 0; k < 50; k++) {
  const p = [...shared, `leaf${k}`];
  patterns.push(p);
  auto.add(k, p);
}
const longPath = [...shared, 'leaf7', 'extra', 'deep'];
const r = auto.match(longPath);
let refVisited = 0;
for (const p of patterns) refVisited += matchPatternRefCounted(p, longPath).visited;
console.log(`  path depth=${longPath.length}, patterns=${patterns.length} (100 shared prefix segments)`);
console.log(`  automaton: ${auto.stateCount} states total, ${r.visited} state visits for this path`);
console.log(`  reference: ${refVisited} visits (sum of independent per-subscription walks)`);
console.log(`  -> shared automaton is ${(refVisited / r.visited).toFixed(1)}x cheaper`);

console.log('\n=== 8. Op log replay reproduces state ===');
const replica = Store.replay(store.opLog, initial); // same base state + op log
assert.deepEqual(replica.snapshot(), store.snapshot());
console.log(`  replayed ${store.opLog.length} ops -> snapshot identical: true`);
console.log(`  automaton states in main store: ${store.automatonStates}, total match visits: ${store.stats.totalMatchVisited}`);
