import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ABSENT,
  Solver,
  jointSatisfiable,
  materialize,
  validate,
  type Schema,
} from '../src/index.ts';

test('per-group feasible does not imply joint feasible', () => {
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['a', 'b'] },
      { kind: 'atMost', k: 0, fields: ['a', 'b'] },
    ],
  };
  assert.equal(jointSatisfiable({ groups: [schema.groups![0]] }), true);
  assert.equal(jointSatisfiable({ groups: [schema.groups![1]] }), true);
  assert.equal(jointSatisfiable(schema), false);
});

test('overlapping conditional groups are solved jointly', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'], when: { op: 'present', field: 'c' } },
      { kind: 'atMost', k: 0, fields: ['a', 'b'], when: { op: 'present', field: 'c' } },
    ],
  };
  const r = new Solver(schema).solve();
  assert.equal(r.status, 'sat');
  if (r.status === 'sat') {
    assert.deepEqual(r.witness.present, []);
  }
  const forced: Schema = {
    groups: [...schema.groups!, { kind: 'atLeastOne', fields: ['c'] }],
  };
  assert.equal(jointSatisfiable(forced), false);
});

test('small-domain enumeration verifies every witness', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b', 'c'] },
      {
        kind: 'atLeastOne',
        fields: ['x'],
        when: { op: 'equals', field: 'mode', value: 'strict' },
      },
    ],
  };
  const solver = new Solver(schema);
  const witnesses = solver.enumerate();
  assert.ok(witnesses.length > 0);
  for (const w of witnesses) {
    const instance = materialize(w.assignment, solver);
    assert.equal(validate(instance, schema).valid, true, JSON.stringify(instance));
  }
  // brute-force cross-check over the full small-domain product
  const fields = solver.fields;
  const domains = fields.map((f) => solver.baseDomain(f));
  let expected = 0;
  const combo = new Map<string, (typeof domains)[number][number]>();
  const walk = (i: number): void => {
    if (i === fields.length) {
      const instance = materialize(new Map(combo), solver);
      if (validate(instance, schema).valid) expected += 1;
      return;
    }
    for (const v of domains[i]) {
      combo.set(fields[i], v);
      walk(i + 1);
    }
    combo.delete(fields[i]);
  };
  walk(0);
  assert.equal(witnesses.length, expected);
});

test('push/pop restores propagation state after local modification', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b', 'c'] }] };
  const solver = new Solver(schema);
  const baseline = solver.enumerate().length;
  assert.equal(baseline, 3);

  solver.push();
  assert.equal(solver.assume('a', ABSENT), true);
  assert.deepEqual(solver.domainOf('a'), [ABSENT]);
  const reduced = solver.enumerate().length;
  assert.equal(reduced, 2);
  solver.pop();

  assert.equal(solver.domainOf('a').length, 2);
  assert.equal(solver.enumerate().length, baseline);
  assert.equal(solver.depth, 0);
});

test('assume detects immediate contradiction', () => {
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['a'] },
      { kind: 'atMost', k: 0, fields: ['a'] },
    ],
  };
  const solver = new Solver(schema);
  solver.push();
  assert.equal(solver.assume('a', ABSENT), false);
  solver.pop();
  assert.equal(solver.depth, 0);
});

test('propagation forces the last open field', () => {
  const schema: Schema = { groups: [{ kind: 'atLeastOne', fields: ['a', 'b'] }] };
  const solver = new Solver(schema);
  solver.push();
  assert.equal(solver.assume('a', ABSENT), true);
  assert.deepEqual(solver.domainOf('b'), solver.domainOf('b').filter((v) => v !== ABSENT));
  solver.pop();
});
