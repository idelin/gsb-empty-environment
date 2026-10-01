import test from 'node:test';
import assert from 'node:assert/strict';
import { validate } from '../src/validator.ts';
import type { Schema } from '../src/types.ts';

test('type error carries instance path and rule path', () => {
  const schema: Schema = { type: 'object', properties: { a: { type: 'integer' } } };
  const errors = validate({ a: 'x' }, schema, 'v1');
  assert.equal(errors.length, 1);
  assert.deepEqual(errors[0].instancePath, ['a']);
  assert.equal(errors[0].schemaPath, '#/properties/a/type');
  assert.equal(errors[0].keyword, 'type');
  assert.equal(errors[0].schemaVersion, 'v1');
});

test('array item errors use numeric instance path segments', () => {
  const schema: Schema = { type: 'array', items: { type: 'number', minimum: 2 } };
  const errors = validate([1, 5, 0], schema, 'v1');
  assert.deepEqual(errors.map((e) => e.instancePath), [[0], [2]]);
  assert.ok(errors.every((e) => e.schemaPath === '#/items/minimum'));
});

test('required, enum, pattern, minLength, maxItems are reported', () => {
  const schema: Schema = {
    type: 'object',
    required: ['id'],
    properties: {
      name: { type: 'string', minLength: 2, pattern: '^[a-z]+$' },
      tag: { enum: ['a', 'b'] },
      list: { type: 'array', maxItems: 1 },
    },
  };
  const errors = validate({ name: 'A', tag: 'z', list: [1, 2] }, schema, 'v1');
  const byKeyword = new Map(errors.map((e) => [e.keyword, e]));
  assert.deepEqual([...byKeyword.keys()].sort(), ['enum', 'maxItems', 'minLength', 'pattern', 'required']);
  assert.equal(byKeyword.get('required')!.schemaPath, '#/required');
  assert.deepEqual(byKeyword.get('maxItems')!.instancePath, ['list']);
});

test('integer is a number, but a float is not an integer', () => {
  const schema: Schema = { type: 'integer' };
  assert.equal(validate(3, schema, 'v1').length, 0);
  assert.equal(validate(3.5, schema, 'v1').length, 1);
  assert.equal(validate(3, { type: 'number' }, 'v1').length, 0);
});

test('type mismatch suppresses type-specific keyword checks', () => {
  const schema: Schema = { type: 'string', minLength: 100 };
  const errors = validate(5, schema, 'v1');
  assert.deepEqual(errors.map((e) => e.keyword), ['type']);
});

test('additionalProperties: false reports unknown keys', () => {
  const schema: Schema = {
    type: 'object',
    properties: { a: {} },
    additionalProperties: false,
  };
  const errors = validate({ a: 1, b: 2 }, schema, 'v1');
  assert.equal(errors.length, 1);
  assert.equal(errors[0].keyword, 'additionalProperties');
  assert.deepEqual(errors[0].instancePath, ['b']);
});

test('nested schemaPath is escaped like a JSON pointer', () => {
  const schema: Schema = {
    type: 'object',
    properties: { 'a/b': { type: 'object', properties: { c: { type: 'boolean' } } } },
  };
  const errors = validate({ 'a/b': { c: 1 } }, schema, 'v1');
  assert.equal(errors[0].schemaPath, '#/properties/a~1b/properties/c/type');
  assert.deepEqual(errors[0].instancePath, ['a/b', 'c']);
});
