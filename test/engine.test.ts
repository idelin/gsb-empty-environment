import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  PolicyEngine,
  verifyAgainstBruteForce,
  ALWAYS,
  and,
  eq,
  inSet,
  intCmp,
  not,
  or,
  type Rule,
  type Schema,
} from '../src/index.ts';

const schema: Schema = {
  role: { kind: 'enum', values: ['admin', 'user', 'guest'] },
  region: { kind: 'enum', values: ['eu', 'us'] },
  level: { kind: 'int', min: 0, max: 5 },
};

const makeEngine = (defaultDecision: 'allow' | 'deny' = 'deny') =>
  new PolicyEngine({ schema, defaultDecision });

const rule = (partial: Partial<Rule> & Pick<Rule, 'id' | 'start' | 'end'>): Rule => ({
  version: 1,
  decision: 'allow',
  priority: 0,
  condition: ALWAYS,
  ...partial,
});

test('half-open boundary: [0,5) allow and [5,10) deny decide deny at t=5', () => {
  const engine = makeEngine();
  engine.insert(rule({ id: 'a', start: 0, end: 5, decision: 'allow' }));
  engine.insert(rule({ id: 'b', start: 5, end: 10, decision: 'deny' }));

  assert.equal(engine.query(4).decision, 'allow');
  assert.equal(engine.query(4).source?.ruleId, 'a');
  assert.equal(engine.query(5).decision, 'deny');
  assert.equal(engine.query(5).source?.ruleId, 'b');
  assert.equal(engine.query(9).decision, 'deny');
  // Outside any rule the default policy applies.
  assert.equal(engine.query(10).decision, 'deny');
  assert.equal(engine.query(10).defaultApplied, true);
  assert.equal(engine.query(10).source, null);
});

test('revoking a rule restores the default policy', () => {
  const engine = makeEngine('deny');
  engine.insert(rule({ id: 'a', start: 0, end: 5, decision: 'allow' }));
  assert.equal(engine.query(2).decision, 'allow');

  const report = engine.remove('a');
  assert.equal(engine.query(2).decision, 'deny');
  assert.equal(engine.query(2).defaultApplied, true);
  assert.deepEqual(report.retracted, [{ ruleId: 'a', version: 1, start: 0, end: 5 }]);
  assert.equal(engine.getSegments().length, 0);
});

test('adjacent same-decision segments with different sources keep their evidence', () => {
  const engine = makeEngine();
  engine.insert(rule({ id: 'a', start: 0, end: 5, decision: 'allow' }));
  engine.insert(rule({ id: 'b', start: 5, end: 10, decision: 'allow' }));

  const segments = engine.getSegments();
  assert.equal(segments.length, 2, 'segments must not merge across different sources');
  assert.equal(engine.query(2).source?.ruleId, 'a');
  assert.equal(engine.query(7).source?.ruleId, 'b');
  assert.notEqual(segments[0]!.node.key, segments[1]!.node.key);
});

test('higher priority rule wins inside the overlap window', () => {
  const engine = makeEngine();
  engine.insert(rule({ id: 'base', start: 0, end: 10, decision: 'deny', priority: 0 }));
  engine.insert(rule({ id: 'vip', start: 3, end: 6, decision: 'allow', priority: 10 }));

  assert.equal(engine.query(1).decision, 'deny');
  assert.equal(engine.query(4).decision, 'allow');
  assert.equal(engine.query(4).source?.ruleId, 'vip');
  assert.equal(engine.query(8).decision, 'deny');
});

test('conditions: enum, int comparisons, boolean combinators, explicit attrs only', () => {
  const engine = makeEngine('deny');
  engine.insert(
    rule({
      id: 'admins-eu',
      start: 0,
      end: 100,
      decision: 'allow',
      condition: and(
        or(eq('role', 'admin'), inSet('role', ['user'])),
        eq('region', 'eu'),
        not(intCmp('level', '<', 2)),
      ),
    }),
  );

  assert.equal(engine.query(0, { role: 'admin', region: 'eu', level: 3 }).decision, 'allow');
  assert.equal(engine.query(0, { role: 'user', region: 'eu', level: 2 }).decision, 'allow');
  assert.equal(engine.query(0, { role: 'guest', region: 'eu', level: 3 }).decision, 'deny');
  assert.equal(engine.query(0, { role: 'admin', region: 'us', level: 3 }).decision, 'deny');
  assert.equal(engine.query(0, { role: 'admin', region: 'eu', level: 1 }).decision, 'deny');
  // Missing attribute reads as false: policy never sees implicit identity data.
  assert.equal(engine.query(0, { role: 'admin', level: 3 }).decision, 'deny');
});

test('endpoint change retracts old evidence and records touched segments/nodes', () => {
  const engine = makeEngine('deny');
  engine.insert(rule({ id: 'a', start: 0, end: 10, decision: 'allow' }));
  const nodeBefore = engine.query(8).nodeId;

  const report = engine.upsert(rule({ id: 'a', version: 2, start: 0, end: 4, decision: 'allow' }));

  // Old evidence [0,10) withdrawn, new evidence [0,4) added.
  assert.deepEqual(report.retracted, [{ ruleId: 'a', version: 1, start: 0, end: 10 }]);
  assert.deepEqual(report.added, [{ ruleId: 'a', version: 2, start: 0, end: 4 }]);
  // The retracted tail [4,10) falls back to the default policy.
  assert.equal(engine.query(8).decision, 'deny');
  assert.equal(engine.query(8).defaultApplied, true);
  assert.equal(engine.query(2).source?.ruleId, 'a');
  assert.equal(engine.query(2).source?.version, 2);
  // Touched segments and decision nodes were actually recorded.
  assert.ok(report.touchedSegments.length > 0);
  assert.ok(report.touchedNodeIds.length > 0);
  for (let t = 4; t < 10; t++) {
    assert.ok(
      report.touchedSegments.some((s) => t >= s.start && t < s.end),
      `t=${t} must be covered by a touched segment`,
    );
  }
  assert.ok(nodeBefore === null || report.touchedNodeIds.includes(nodeBefore));
});

test('stale versions are rejected, newer versions replace', () => {
  const engine = makeEngine();
  engine.insert(rule({ id: 'a', version: 3, start: 0, end: 5 }));
  assert.throws(() => engine.upsert(rule({ id: 'a', version: 2, start: 0, end: 5 })), /stale version/);
  engine.upsert(rule({ id: 'a', version: 4, start: 0, end: 5, decision: 'deny' }));
  assert.equal(engine.query(1).decision, 'deny');
  assert.equal(engine.query(1).source?.version, 4);
});

test('batch apply is atomic: one invalid op rejects the whole batch', () => {
  const engine = makeEngine('deny');
  engine.insert(rule({ id: 'keep', start: 0, end: 5, decision: 'allow' }));
  const before = engine.query(2);

  assert.throws(
    () =>
      engine.applyBatch([
        { type: 'insert', rule: rule({ id: 'x', start: 10, end: 20, decision: 'allow' }) },
        { type: 'remove', ruleId: 'keep' },
        { type: 'insert', rule: rule({ id: 'bad', start: 7, end: 7 }) }, // invalid interval
      ]),
    /start < end/,
  );

  // Nothing changed: results and sources are untouched.
  assert.deepEqual(engine.query(2), before);
  assert.equal(engine.getRule('x'), undefined);
  assert.equal(engine.getRule('keep')?.id, 'keep');
});

test('batch of insert+remove applies results and sources together', () => {
  const engine = makeEngine('deny');
  const report = engine.applyBatch([
    { type: 'insert', rule: rule({ id: 'a', start: 0, end: 5, decision: 'allow' }) },
    { type: 'insert', rule: rule({ id: 'b', start: 5, end: 8, decision: 'allow' }) },
    { type: 'remove', ruleId: 'a' },
  ]);
  assert.equal(report.ops, 3);
  assert.equal(engine.query(2).defaultApplied, true);
  assert.equal(engine.query(6).source?.ruleId, 'b');
});

test('segments sharing a rule set reuse one decision node (no Cartesian expansion)', () => {
  const engine = makeEngine();
  engine.insert(rule({ id: 'wide', start: 0, end: 10, decision: 'deny' }));
  engine.insert(rule({ id: 'hole', start: 3, end: 6, decision: 'allow', priority: 5 }));

  const segments = engine.getSegments();
  assert.equal(segments.length, 3);
  // [0,3) and [6,10) carry the identical rule set and share one node object.
  assert.equal(segments[0]!.node.id, segments[2]!.node.id);
  assert.equal(segments[0]!.node, segments[2]!.node);
  assert.notEqual(segments[0]!.node.id, segments[1]!.node.id);
});

test('invalid conditions and intervals are rejected at validation time', () => {
  const engine = makeEngine();
  assert.throws(
    () => engine.insert(rule({ id: 'r1', start: 0, end: 5, condition: eq('unknown', 'x') })),
    /unknown attribute/,
  );
  assert.throws(
    () => engine.insert(rule({ id: 'r2', start: 0, end: 5, condition: intCmp('role', '>', 1) })),
    /expected int/,
  );
  assert.throws(() => engine.insert(rule({ id: 'r3', start: 0, end: 0 })), /start < end/);
  assert.throws(() => engine.insert(rule({ id: 'r4', start: 0.5, end: 5 })), /integers/);
});

test('full enumeration cross-check: engine matches brute force on the finite lattice', () => {
  const engine = makeEngine('deny');
  engine.applyBatch([
    { type: 'insert', rule: rule({ id: 'r1', start: 0, end: 8, decision: 'allow', priority: 1, condition: eq('role', 'admin') }) },
    { type: 'insert', rule: rule({ id: 'r2', start: 2, end: 12, decision: 'deny', priority: 2, condition: intCmp('level', '>=', 3) }) },
    { type: 'insert', rule: rule({ id: 'r3', start: 4, end: 6, decision: 'allow', priority: 3, condition: and(eq('region', 'eu'), not(eq('role', 'guest'))) }) },
    { type: 'insert', rule: rule({ id: 'r4', start: 10, end: 14, decision: 'allow', priority: 0, condition: inSet('role', ['user', 'guest']) }) },
  ]);

  let result = verifyAgainstBruteForce(engine, schema, 'deny', 0, 16);
  assert.equal(result.mismatches.length, 0, JSON.stringify(result.mismatches.slice(0, 3)));
  assert.equal(result.checked, 16 * 3 * 2 * 6);

  // Incremental updates (endpoint move + delete) keep the engine consistent.
  engine.upsert(rule({ id: 'r2', version: 2, start: 1, end: 5, decision: 'deny', priority: 2, condition: intCmp('level', '>=', 3) }));
  engine.remove('r3');
  result = verifyAgainstBruteForce(engine, schema, 'deny', 0, 16);
  assert.equal(result.mismatches.length, 0, JSON.stringify(result.mismatches.slice(0, 3)));
});
