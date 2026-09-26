import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, type Schema } from '../src/index.ts';

test('type error carries both instance path and rule path', () => {
  const schema: Schema = { type: 'object', properties: { age: { type: 'integer' } } };
  const errors = validate({ age: 1.5 }, schema);
  assert.equal(errors.length, 1);
  assert.equal(errors[0].instancePath, '/age');
  assert.equal(errors[0].rulePath, '#/properties/age/type');
  assert.equal(errors[0].keyword, 'type');
});

test('nested rule paths accumulate', () => {
  const schema: Schema = {
    type: 'object',
    properties: { a: { type: 'object', properties: { b: { type: 'string' } } } },
  };
  const errors = validate({ a: { b: 3 } }, schema);
  assert.equal(errors[0].instancePath, '/a/b');
  assert.equal(errors[0].rulePath, '#/properties/a/properties/b/type');
});

test('required, range, length and items keywords', () => {
  const schema: Schema = {
    type: 'object',
    required: ['id'],
    properties: {
      id: { type: 'integer', minimum: 1, maximum: 10 },
      name: { type: 'string', minLength: 2, maxLength: 3 },
      tags: { type: 'array', items: { type: 'string' } },
    },
  };
  assert.equal(validate({ id: 5, name: 'ab', tags: ['x'] }, schema).length, 0);
  assert.equal(validate({}, schema)[0].keyword, 'required');
  assert.equal(validate({ id: 0 }, schema)[0].keyword, 'minimum');
  assert.equal(validate({ id: 11 }, schema)[0].keyword, 'maximum');
  assert.equal(validate({ id: 5, name: 'a' }, schema)[0].keyword, 'minLength');
  assert.equal(validate({ id: 5, name: 'abcd' }, schema)[0].keyword, 'maxLength');
  const itemError = validate({ id: 5, tags: ['x', 2] }, schema)[0];
  assert.equal(itemError.instancePath, '/tags/1');
  assert.equal(itemError.rulePath, '#/properties/tags/items/type');
});

test('integer rejects decimals, number accepts them', () => {
  const wrap = (t: string): Schema => ({ type: 'object', properties: { v: { type: t } } });
  assert.equal(validate({ v: 1.5 }, wrap('integer')).length, 1);
  assert.equal(validate({ v: 1.5 }, wrap('number')).length, 0);
  assert.equal(validate({ v: 2 }, wrap('integer')).length, 0);
});

test('nullable union types and root-level paths', () => {
  const schema: Schema = { type: 'object', properties: { x: { type: ['string', 'null'] } } };
  assert.equal(validate({ x: null }, schema).length, 0);
  assert.equal(validate({ x: 'a' }, schema).length, 0);
  assert.equal(validate({ x: 1 }, schema).length, 1);
  const rootError = validate('nope', { type: 'object' })[0];
  assert.equal(rootError.instancePath, '');
  assert.equal(rootError.rulePath, '#/type');
});
