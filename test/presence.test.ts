import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isPresent } from '../src/presence.ts';
import { validate, type Schema } from '../src/index.ts';

test('presence is own-property based, independent of the value', () => {
  const instance = { a: false, b: null, c: 0, d: '', e: undefined };
  for (const f of ['a', 'b', 'c', 'd', 'e']) {
    assert.equal(isPresent(instance, f), true, `${f} must count as present`);
  }
  assert.equal(isPresent(instance, 'missing'), false);
});

test('value false still counts as field present (presence != truthiness)', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b'] }] };
  const result = validate({ a: false }, schema);
  assert.equal(result.status, 'valid');
  assert.equal(result.valid, true);
});

test('value null counts as present (presence != non-null)', () => {
  const schema: Schema = { groups: [{ kind: 'atLeastOne', fields: ['a', 'b'] }] };
  assert.equal(validate({ a: null }, schema).valid, true);
});

test('false and null together saturate exactlyOne -> conflict, not "absent"', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b'] }] };
  const result = validate({ a: false, b: null }, schema);
  assert.equal(result.status, 'conflict');
  assert.deepEqual(result.conflictCores[0].facts, ['a', 'b']);
});
