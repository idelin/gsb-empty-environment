import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, SchemaError, type Schema } from '../src/index.ts';

test('exactlyOne: valid, missing, and excess cases', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b'] }] };
  assert.equal(validate({ a: 1 }, schema).valid, true);

  const missing = validate({}, schema);
  assert.equal(missing.status, 'missing');
  assert.deepEqual(missing.completions, [['a'], ['b']]);

  const excess = validate({ a: 1, b: 2 }, schema);
  assert.equal(excess.status, 'conflict');
  assert.deepEqual(excess.conflictCores, [{ facts: ['a', 'b'], rules: ['/groups/0'] }]);
});

test('atLeastOne: minimal completions are all singletons', () => {
  const schema: Schema = { groups: [{ kind: 'atLeastOne', fields: ['x', 'y', 'z'] }] };
  const result = validate({}, schema);
  assert.equal(result.status, 'missing');
  assert.deepEqual(result.completions, [['x'], ['y'], ['z']]);
});

test('atMostK: k+1 present fields form the minimal conflict core', () => {
  const schema: Schema = { groups: [{ kind: 'atMostK', k: 1, fields: ['x', 'y', 'z'] }] };
  assert.equal(validate({ x: 1 }, schema).valid, true);
  const result = validate({ x: 1, y: 1, z: 1 }, schema);
  assert.equal(result.status, 'conflict');
  // subset-minimal: any two of the three facts suffice; deletion keeps the first two
  assert.equal(result.conflictCores[0].facts.length, 2);
  assert.deepEqual(result.conflictCores[0].rules, ['/groups/0']);
});

test('issues carry both instancePath and rulePath', () => {
  const schema: Schema = { groups: [{ kind: 'atLeastOne', fields: ['x'] }] };
  const issue = validate({}, schema).issues.find((i) => i.type === 'missing');
  assert.ok(issue);
  assert.equal(issue.instancePath, '');
  assert.equal(issue.rulePath, '/groups/0');
  assert.deepEqual(issue.fields, ['x']);
});

test('schemas outside the promised subset are rejected with a schema path', () => {
  assert.throws(
    () => validate({}, { groups: [{ kind: 'allOf', fields: ['a'] }] } as never),
    (e: unknown) => e instanceof SchemaError && (e as SchemaError).schemaPath === '/groups/0/kind',
  );
  assert.throws(
    () => validate({}, { groups: [{ kind: 'atMostK', fields: ['a'] }] } as never),
    /groups\/0\/k/,
  );
  assert.throws(
    () =>
      validate(
        {},
        { groups: [{ kind: 'exactlyOne', fields: ['a'], when: { op: 'regex', field: 'a' } }] } as never,
      ),
    /groups\/0\/when\/op/,
  );
});

test('non-object instances are rejected', () => {
  const schema: Schema = { groups: [{ kind: 'atLeastOne', fields: ['a'] }] };
  assert.throws(() => validate([1, 2], schema), /plain object/);
  assert.throws(() => validate(null, schema), /plain object/);
});

test('field domain beyond the small-domain guard is refused', () => {
  const fields = Array.from({ length: 25 }, (_, i) => `f${i}`);
  const schema: Schema = { groups: [{ kind: 'atLeastOne', fields }] };
  assert.throws(() => validate({}, schema, { maxFields: 20 }), /small-domain/);
});
