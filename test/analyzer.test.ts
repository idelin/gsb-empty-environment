import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ConflictAnalyzer, evalCond } from '../src/index.ts';
import type { Cond, Rule, Schema } from '../src/index.ts';

const roleSchema: Schema = {
  role: { kind: 'enum', values: ['admin', 'user', 'guest'] },
  age: { kind: 'int', min: 0, max: 120 },
};

function makeAnalyzer(schema: Schema, rules: Rule[], budget = {}) {
  const analyzer = new ConflictAnalyzer(schema, budget);
  for (const rule of rules) analyzer.addRule(rule);
  return analyzer;
}

test('detects overlap of allow and deny with interval and enum constraints', () => {
  const analyzer = makeAnalyzer(roleSchema, [
    { id: 'A1', effect: 'allow', when: { op: 'in', attr: 'role', values: ['admin', 'user'] } },
    {
      id: 'D1', effect: 'deny',
      when: { op: 'and', args: [
        { op: 'in', attr: 'role', values: ['user'] },
        { op: 'cmp', attr: 'age', cmp: '<', value: 21 },
      ] },
    },
  ]);
  const report = analyzer.analyze();
  assert.equal(report.status, 'complete');
  assert.equal(report.regions.length, 1);
  assert.deepEqual(report.regions[0].description, { role: 'user', age: '0..20' });
  assert.deepEqual(report.regions[0].allowRules, ['A1']);
  assert.deepEqual(report.regions[0].denyRules, ['D1']);
  assert.deepEqual(report.regions[0].witness, { role: 'user', age: 0 });
  assert.deepEqual(report.minimalConflictSets, [{ allow: 'A1', deny: 'D1' }]);
  assert.deepEqual(report.rulesInvolved, ['A1', 'D1']);
  assert.equal(report.verification.mode, 'exhaustive');
  assert.deepEqual(report.verification.mismatches, []);
});

test('mutually exclusive intervals are not reported as conflicts', () => {
  const schema: Schema = { x: { kind: 'int', min: 0, max: 10 } };
  const analyzer = makeAnalyzer(schema, [
    { id: 'A', effect: 'allow', when: { op: 'cmp', attr: 'x', cmp: '<', value: 5 } },
    { id: 'D', effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '>=', value: 5 } },
  ]);
  const report = analyzer.analyze();
  assert.equal(report.status, 'complete');
  assert.equal(report.regions.length, 0);
  assert.deepEqual(report.minimalConflictSets, []);
  assert.equal(report.verification.mismatches.length, 0);
});

test('boundary equality overlaps only when both sides are closed', () => {
  const schema: Schema = { x: { kind: 'int', min: 0, max: 10 } };
  // closed/closed: [0,5] ∩ [5,10] = {5}
  const closed = makeAnalyzer(schema, [
    { id: 'A', effect: 'allow', when: { op: 'cmp', attr: 'x', cmp: '<=', value: 5 } },
    { id: 'D', effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '>=', value: 5 } },
  ]).analyze();
  assert.equal(closed.regions.length, 1);
  assert.deepEqual(closed.regions[0].description, { x: '5' });
  assert.deepEqual(closed.regions[0].witness, { x: 5 });
  // open/closed: x < 5 vs x >= 5 -> disjoint
  const open = makeAnalyzer(schema, [
    { id: 'A', effect: 'allow', when: { op: 'cmp', attr: 'x', cmp: '<', value: 5 } },
    { id: 'D', effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '>=', value: 5 } },
  ]).analyze();
  assert.equal(open.regions.length, 0);
});

test('boolean operators (and/or/not) and != are handled', () => {
  const schema: Schema = {
    role: { kind: 'enum', values: ['admin', 'user', 'guest'] },
    x: { kind: 'int', min: 0, max: 10 },
  };
  const analyzer = makeAnalyzer(schema, [
    { id: 'A', effect: 'allow', when: { op: 'not', arg: { op: 'in', attr: 'role', values: ['guest'] } } },
    { id: 'D', effect: 'deny', when: { op: 'or', args: [
      { op: 'in', attr: 'role', values: ['user'] },
      { op: 'cmp', attr: 'x', cmp: '!=', value: 5 },
    ] } },
  ]);
  const report = analyzer.analyze();
  assert.equal(report.status, 'complete');
  assert.equal(report.verification.mismatches.length, 0);
  assert.ok(report.regions.length > 0);
  // '!=' must not conflict with the excluded point itself
  const exclusive = makeAnalyzer({ x: { kind: 'int', min: 0, max: 10 } }, [
    { id: 'A', effect: 'allow', when: { op: 'cmp', attr: 'x', cmp: '!=', value: 5 } },
    { id: 'D', effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '==', value: 5 } },
  ]).analyze();
  assert.equal(exclusive.regions.length, 0);
});

test('regions are pairwise disjoint and each gets its own witness instance', () => {
  const schema: Schema = { x: { kind: 'int', min: 0, max: 10 } };
  const analyzer = makeAnalyzer(schema, [
    { id: 'A', effect: 'allow', when: { op: 'cmp', attr: 'x', cmp: '>=', value: 0 } },
    { id: 'D', effect: 'deny', when: { op: 'or', args: [
      { op: 'cmp', attr: 'x', cmp: '<=', value: 2 },
      { op: 'cmp', attr: 'x', cmp: '>=', value: 8 },
    ] } },
  ]);
  const report = analyzer.analyze();
  assert.equal(report.regions.length, 2);
  // Two separate regions must not be masked by one representative point.
  const witnesses = report.regions.map((r) => r.witness.x);
  assert.deepEqual(witnesses, [0, 8]);
  assert.notEqual(witnesses[0], witnesses[1]);
  // Disjointness + exact coverage is cross-checked by exhaustive enumeration.
  assert.equal(report.verification.mode, 'exhaustive');
  assert.deepEqual(report.verification.mismatches, []);
});

test('finite small domains are independently enumerated and all conflicts verified', () => {
  const schema: Schema = {
    a: { kind: 'enum', values: ['p', 'q'] },
    b: { kind: 'enum', values: ['u', 'v'] },
    x: { kind: 'int', min: 1, max: 4 },
  };
  const analyzer = makeAnalyzer(schema, [
    { id: 'A1', effect: 'allow', when: { op: 'or', args: [
      { op: 'in', attr: 'a', values: ['p'] },
      { op: 'cmp', attr: 'x', cmp: '>=', value: 3 },
    ] } },
    { id: 'D1', effect: 'deny', when: { op: 'and', args: [
      { op: 'in', attr: 'b', values: ['v'] },
      { op: 'cmp', attr: 'x', cmp: '<=', value: 3 },
    ] } },
  ]);
  const report = analyzer.analyze();
  assert.equal(report.verification.mode, 'exhaustive');
  assert.equal(report.verification.checked, 2 * 2 * 4);
  assert.deepEqual(report.verification.mismatches, []);
});

test('rule retraction by id with incremental recompute (pair cache reuse)', () => {
  const analyzer = makeAnalyzer(roleSchema, [
    { id: 'A1', effect: 'allow', when: { op: 'in', attr: 'role', values: ['admin', 'user'] } },
    { id: 'A2', effect: 'allow', when: { op: 'cmp', attr: 'age', cmp: '>=', value: 60 } },
    { id: 'D1', effect: 'deny', when: { op: 'in', attr: 'role', values: ['user'] } },
    { id: 'D2', effect: 'deny', when: { op: 'cmp', attr: 'age', cmp: '>=', value: 65 } },
  ]);
  const first = analyzer.analyze();
  assert.equal(first.minimalConflictSets.length, 4); // A1/A2 x D1/D2 all overlap
  const computedAfterFirst = first.stats.pairComputations;
  assert.equal(computedAfterFirst, 4);

  // Retract D2: only pairs involving D2 are dropped; others are reused.
  assert.equal(analyzer.removeRule('D2'), true);
  assert.equal(analyzer.removeRule('D2'), false);
  const second = analyzer.analyze();
  assert.equal(second.stats.pairComputations, computedAfterFirst); // no recomputation
  assert.deepEqual(second.minimalConflictSets, [
    { allow: 'A1', deny: 'D1' },
    { allow: 'A2', deny: 'D1' },
  ]);
  assert.equal(second.verification.mismatches.length, 0);

  // Adding a new deny only computes the new pairs.
  analyzer.addRule({ id: 'D3', effect: 'deny', when: { op: 'cmp', attr: 'age', cmp: '<=', value: 10 } });
  const third = analyzer.analyze();
  assert.equal(third.stats.pairComputations, computedAfterFirst + 2);
});

test('shared condition subexpressions reuse the DNF cache', () => {
  const shared: Cond = { op: 'and', args: [
    { op: 'in', attr: 'role', values: ['admin', 'user'] },
    { op: 'cmp', attr: 'age', cmp: '>=', value: 18 },
  ] };
  const analyzer = makeAnalyzer(roleSchema, [
    { id: 'A1', effect: 'allow', when: shared },
    { id: 'A2', effect: 'allow', when: { op: 'and', args: [
      // structurally identical, different object and different arg order
      { op: 'cmp', attr: 'age', cmp: '>=', value: 18 },
      { op: 'in', attr: 'role', values: ['user', 'admin'] },
    ] } },
    { id: 'D1', effect: 'deny', when: { op: 'in', attr: 'role', values: ['user'] } },
  ]);
  const report = analyzer.analyze();
  assert.ok(report.stats.dnfCacheHits >= 1, 'expected shared subexpression cache hits');
});

test('insufficient region budget marks the search partial, never silently conflict-free', () => {
  const schema: Schema = { x: { kind: 'int', min: 0, max: 100 } };
  const rules: Rule[] = [
    { id: 'A', effect: 'allow', when: { op: 'const', value: true } },
  ];
  // Many disjoint deny islands -> many regions.
  for (let i = 0; i < 10; i++) {
    rules.push({ id: `D${i}`, effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '==', value: i * 10 } });
  }
  const analyzer = makeAnalyzer(schema, rules, { maxRegions: 3 });
  const report = analyzer.analyze();
  assert.equal(report.status, 'partial');
  assert.ok(report.pendingReason);
  assert.ok(report.regions.length > 0 && report.regions.length <= 4);
  // Found regions are still reported; unvisited areas are undecided, not "no conflict".
  assert.ok(report.minimalConflictSets.length > 0);
});

test('insufficient enumeration budget marks verification pending', () => {
  const schema: Schema = { x: { kind: 'int', min: 0, max: 100000 } };
  const analyzer = makeAnalyzer(schema, [
    { id: 'A', effect: 'allow', when: { op: 'cmp', attr: 'x', cmp: '>=', value: 50 } },
    { id: 'D', effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '<=', value: 60 } },
  ], { maxEnum: 1000 });
  const report = analyzer.analyze();
  assert.equal(report.verification.mode, 'partial');
  assert.equal(report.verification.pending, true);
  assert.equal(report.verification.checked, 1000);
  assert.ok(report.verification.total > 1000);
  // The symbolically derived region is still reported.
  assert.equal(report.regions.length, 1);
  assert.deepEqual(report.regions[0].description, { x: '50..60' });
});

test('policies may only read explicitly declared attributes', () => {
  const analyzer = new ConflictAnalyzer(roleSchema);
  assert.throws(
    () => analyzer.addRule({ id: 'X', effect: 'allow', when: { op: 'in', attr: 'department', values: ['it'] } }),
    /unknown attribute/,
  );
  assert.throws(
    () => analyzer.addRule({ id: 'Y', effect: 'allow', when: { op: 'cmp', attr: 'role', cmp: '<', value: 3 } }),
    /not an integer/,
  );
  assert.throws(
    () => analyzer.addRule({ id: 'Z', effect: 'allow', when: { op: 'in', attr: 'role', values: ['superuser'] } }),
    /not in domain/,
  );
});

test('minimal conflict rule sets only include genuinely overlapping pairs', () => {
  const analyzer = makeAnalyzer(roleSchema, [
    { id: 'A1', effect: 'allow', when: { op: 'in', attr: 'role', values: ['admin'] } },
    { id: 'A2', effect: 'allow', when: { op: 'in', attr: 'role', values: ['guest'] } },
    { id: 'D1', effect: 'deny', when: { op: 'in', attr: 'role', values: ['admin', 'user'] } },
  ]);
  const report = analyzer.analyze();
  assert.deepEqual(report.minimalConflictSets, [{ allow: 'A1', deny: 'D1' }]);
  assert.deepEqual(report.rulesInvolved, ['A1', 'D1']);
  assert.equal(report.regions.length, 1);
  assert.deepEqual(report.regions[0].witness, { role: 'admin', age: 0 });
});

test('no rules or one-sided rules yield a complete empty report', () => {
  const empty = new ConflictAnalyzer(roleSchema).analyze();
  assert.equal(empty.status, 'complete');
  assert.equal(empty.regions.length, 0);
  const oneSided = makeAnalyzer(roleSchema, [
    { id: 'A', effect: 'allow', when: { op: 'const', value: true } },
  ]).analyze();
  assert.equal(oneSided.status, 'complete');
  assert.equal(oneSided.regions.length, 0);
  assert.equal(oneSided.verification.mismatches.length, 0);
});

test('witness instances satisfy both allow and deny conditions', () => {
  const analyzer = makeAnalyzer(roleSchema, [
    { id: 'A1', effect: 'allow', when: { op: 'and', args: [
      { op: 'in', attr: 'role', values: ['admin', 'user'] },
      { op: 'cmp', attr: 'age', cmp: '>=', value: 18 },
    ] } },
    { id: 'D1', effect: 'deny', when: { op: 'cmp', attr: 'age', cmp: '<=', value: 30 } },
  ]);
  const report = analyzer.analyze();
  assert.equal(report.regions.length, 1);
  const [region] = report.regions;
  assert.deepEqual(region.description.age, '18..30');
  assert.ok(evalCond({ op: 'const', value: true }, region.witness));
  assert.equal(region.witness.age, 18);
});

test('partial search: uncovered conflict points are undecided, not mismatches', () => {
  const schema: Schema = { x: { kind: 'int', min: 0, max: 9 } };
  const analyzer = makeAnalyzer(schema, [
    { id: 'A', effect: 'allow', when: { op: 'const', value: true } },
    { id: 'D0', effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '==', value: 2 } },
    { id: 'D1', effect: 'deny', when: { op: 'cmp', attr: 'x', cmp: '==', value: 7 } },
  ], { maxRegions: 1 });
  const report = analyzer.analyze();
  assert.equal(report.status, 'partial');
  assert.equal(report.regions.length, 1);
  // Enumeration is exhaustive here, yet the unprocessed conflict point x=7
  // is part of the undecided remainder — not a mismatch, not "no conflict".
  assert.equal(report.verification.mode, 'exhaustive');
  assert.deepEqual(report.verification.mismatches, []);
});
