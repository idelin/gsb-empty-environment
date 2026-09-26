import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  jointSatisfiable,
  minimalCorrections,
  minimalUnsatCore,
  validate,
  type Schema,
} from '../src/index.ts';

const jointUnsat: Schema = {
  groups: [
    { kind: 'exactlyOne', fields: ['a', 'b'], when: { op: 'present', field: 'c' } },
    { kind: 'atMost', k: 0, fields: ['a', 'b'], when: { op: 'present', field: 'c' } },
    { kind: 'atLeastOne', fields: ['c'] },
  ],
};

test('minimal unsat core covers the original rule paths and is subset-minimal', () => {
  const core = minimalUnsatCore(jointUnsat);
  assert.ok(core);
  assert.deepEqual(core.rulePaths, ['/groups/0', '/groups/1', '/groups/2']);
  for (const path of core.rulePaths) {
    const rest = jointUnsat.groups!.filter((_, i) => `/groups/${i}` !== path);
    assert.equal(jointSatisfiable({ groups: rest }), true, `removing ${path} must restore satisfiability`);
  }
});

test('core excludes groups irrelevant to the conflict', () => {
  const schema: Schema = {
    groups: [...jointUnsat.groups!, { kind: 'atLeastOne', fields: ['z'] }],
  };
  const core = minimalUnsatCore(schema);
  assert.ok(core);
  assert.deepEqual(core.rulePaths, ['/groups/0', '/groups/1', '/groups/2']);
});

test('satisfiable schema has no core', () => {
  assert.equal(minimalUnsatCore({ groups: [{ kind: 'atLeastOne', fields: ['a'] }] }), null);
});

test('minimal correction: missing set for atLeastOne', () => {
  const schema: Schema = { groups: [{ kind: 'atLeastOne', fields: ['a', 'b'] }] };
  const fixes = minimalCorrections(schema, {});
  assert.equal(fixes.length, 2);
  for (const fix of fixes) {
    assert.equal(fix.size, 1);
    assert.deepEqual(fix.unset, []);
    assert.equal(Object.keys(fix.set).length, 1);
    assert.ok('a' in fix.set || 'b' in fix.set);
  }
});

test('minimal correction: conflict set for exactlyOne overflow', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b'] }] };
  const fixes = minimalCorrections(schema, { a: 1, b: 2 });
  assert.equal(fixes.length, 2);
  for (const fix of fixes) {
    assert.equal(fix.size, 1);
    assert.deepEqual(fix.set, {});
    assert.equal(fix.unset.length, 1);
  }
});

test('minimal correction may require simultaneous add and remove', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'] },
      { kind: 'atMost', k: 0, fields: ['b'] },
    ],
  };
  const fixes = minimalCorrections(schema, { b: 1 });
  assert.equal(fixes.length, 1);
  const fix = fixes[0];
  assert.equal(fix.size, 2);
  assert.deepEqual(fix.unset, ['b']);
  assert.ok('a' in fix.set);
  const repaired: Record<string, unknown> = { b: 1 };
  for (const f of fix.unset) delete repaired[f];
  Object.assign(repaired, fix.set);
  assert.equal(validate(repaired as Record<string, never>, schema).valid, true);
});

test('minimal correction respects conditional enablement', () => {
  const schema: Schema = {
    groups: [
      {
        kind: 'exactlyOne',
        fields: ['x', 'y'],
        when: { op: 'equals', field: 'mode', value: 'on' },
      },
    ],
  };
  const fixes = minimalCorrections(schema, { mode: 'on' });
  // add x / add y, or deactivate the condition by removing/changing `mode`
  assert.equal(fixes.length, 4);
  for (const fix of fixes) {
    assert.equal(fix.size, 1);
    const keys = [...Object.keys(fix.set), ...fix.unset];
    assert.ok(keys.some((k) => k === 'x' || k === 'y' || k === 'mode'));
    const repaired: Record<string, unknown> = { mode: 'on' };
    for (const f of fix.unset) delete repaired[f];
    Object.assign(repaired, fix.set);
    assert.equal(validate(repaired as Record<string, never>, schema).valid, true);
  }
  const off = minimalCorrections(schema, { mode: 'off' });
  assert.equal(off.length, 1);
  assert.equal(off[0].size, 0);
});

test('already valid instance yields the empty correction', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b'] }] };
  const fixes = minimalCorrections(schema, { a: false });
  assert.equal(fixes.length, 1);
  assert.equal(fixes[0].size, 0);
});
