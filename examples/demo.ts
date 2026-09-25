/**
 * Runnable sample: incremental interval policies over a finite lattice.
 * Run with: npm run demo   (or: node examples/demo.ts)
 */
import {
  PolicyEngine,
  verifyAgainstBruteForce,
  ALWAYS,
  and,
  eq,
  intCmp,
  not,
  type Schema,
} from '../src/index.ts';

const schema: Schema = {
  role: { kind: 'enum', values: ['admin', 'user', 'guest'] },
  level: { kind: 'int', min: 0, max: 5 },
};

const engine = new PolicyEngine({ schema, defaultDecision: 'deny' });
const show = (t: number, attrs = {}) => {
  const q = engine.query(t, attrs);
  const src = q.source ? `${q.source.ruleId}@v${q.source.version}` : 'default';
  console.log(`  query(t=${t}, ${JSON.stringify(attrs)}) -> ${q.decision}  [source=${src}, node=${q.nodeId ?? '-'}]`);
};

console.log('== 1. Half-open boundaries: [0,5) allow, [5,10) deny ==');
engine.insert({ id: 'biz-hours', version: 1, start: 0, end: 5, decision: 'allow', priority: 0, condition: ALWAYS });
engine.insert({ id: 'lockdown', version: 1, start: 5, end: 10, decision: 'deny', priority: 0, condition: ALWAYS });
show(4);
show(5);

console.log('\n== 2. Conditional rule with priority inside the window ==');
engine.insert({
  id: 'vip-admin', version: 1, start: 5, end: 10, decision: 'allow', priority: 10,
  condition: and(eq('role', 'admin'), not(intCmp('level', '<', 2))),
});
show(7, { role: 'admin', level: 3 });
show(7, { role: 'guest', level: 3 });
show(7, { level: 3 }); // missing 'role' attr -> condition false -> lockdown deny

console.log('\n== 3. Endpoint change retracts old evidence ==');
const report = engine.upsert({ id: 'lockdown', version: 2, start: 5, end: 8, decision: 'deny', priority: 0, condition: ALWAYS });
console.log('  touched segments:', JSON.stringify(report.touchedSegments));
console.log('  touched nodes:   ', report.touchedNodeIds.join(', '));
console.log('  retracted:       ', JSON.stringify(report.retracted));
show(9, { role: 'admin', level: 3 }); // vip-admin still covers [5,10)

console.log('\n== 4. Atomic batch: one bad op rejects everything ==');
try {
  engine.applyBatch([
    { type: 'remove', ruleId: 'biz-hours' },
    { type: 'insert', rule: { id: 'bad', version: 1, start: 3, end: 3, decision: 'allow', priority: 0, condition: ALWAYS } },
  ]);
} catch (err) {
  console.log('  rejected:', (err as Error).message);
}
show(2); // biz-hours still in place

console.log('\n== 5. Revoke rules -> default policy restored ==');
engine.applyBatch([{ type: 'remove', ruleId: 'biz-hours' }, { type: 'remove', ruleId: 'lockdown' }]);
show(1, { role: 'admin', level: 5 });

console.log('\n== 6. Compiled segments (shared decision nodes) ==');
for (const s of engine.getSegments()) {
  console.log(`  [${s.start}, ${s.end}) -> node ${s.node.id} rules=[${s.node.rules.map((r) => `${r.id}@v${r.version}`).join(', ')}]`);
}

console.log('\n== 7. Brute-force cross-check over the finite lattice ==');
const result = verifyAgainstBruteForce(engine, schema, 'deny', 0, 12);
console.log(`  checked ${result.checked} (time x attrs) points, mismatches: ${result.mismatches.length}`);
