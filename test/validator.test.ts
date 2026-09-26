import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate } from '../src/index.ts';

test('type error carries instance path and rule path', () => {
  const schema = { type: 'object', properties: { age: { type: 'integer' } } };
  const errors = validate(schema, { age: 'x' });
  assert.equal(errors.length, 1);
  assert.equal(errors[0].keyword, 'type');
  assert.equal(errors[0].instancePath, '#/age');
  assert.equal(errors[0].schemaPath, '#/properties/age/type');
});

test('required / enum / const / pattern / range keywords', () => {
  const schema = {
    type: 'object',
    required: ['name'],
    properties: {
      name: { type: 'string', minLength: 2 },
      role: { enum: ['a', 'b'] },
      tag: { const: 1 },
      code: { type: 'string', pattern: '^[A-Z]+$' },
      n: { type: 'number', minimum: 0, maximum: 10 },
    },
  };
  const errors = validate(schema, { role: 'c', tag: 2, code: 'ab', n: 11 });
  const byKeyword = new Map(errors.map((e) => [e.keyword, e]));
  assert.deepEqual(
    [...byKeyword.keys()].sort(),
    ['const', 'enum', 'maximum', 'pattern', 'required'],
  );
  assert.equal(byKeyword.get('required')!.instancePath, '#');
  assert.equal(byKeyword.get('required')!.schemaPath, '#/required');
  assert.equal(byKeyword.get('pattern')!.schemaPath, '#/properties/code/pattern');
});

test('items and additionalProperties', () => {
  const schema = {
    type: 'object',
    properties: { list: { type: 'array', items: { type: 'string' } } },
    additionalProperties: false,
  };
  const errors = validate(schema, { list: ['a', 1], extra: true });
  assert.equal(errors.length, 2);
  const [item, additional] = errors;
  assert.equal(item.instancePath, '#/list/1');
  assert.equal(item.schemaPath, '#/properties/list/items/type');
  assert.equal(additional.keyword, 'additionalProperties');
  assert.equal(additional.instancePath, '#/extra');
});

test('boolean schemas', () => {
  assert.equal(validate(true, 42).length, 0);
  const errors = validate({ properties: { a: false } }, { a: 1 });
  assert.equal(errors.length, 1);
  assert.equal(errors[0].keyword, 'false');
  assert.equal(errors[0].schemaPath, '#/properties/a');
});

test('unknown keywords are ignored (only the implemented subset is promised)', () => {
  assert.equal(validate({ type: 'string', format: 'email', $ref: '#/x' }, 'not-an-email').length, 0);
});
