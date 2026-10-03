import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate } from '../src/validator.ts';
import { assertSupported, UnsupportedSchemaError } from '../src/schema.ts';
import type { Schema } from '../src/schema.ts';

test('错误同时携带实例路径与规则路径（嵌套）', () => {
  const schema: Schema = {
    type: 'object',
    properties: {
      a: {
        type: 'object',
        properties: { b: { type: 'string', minLength: 5 } },
      },
    },
  };
  const errors = validate({ a: { b: 'xy' } }, schema);
  assert.equal(errors.length, 1);
  assert.equal(errors[0].instancePath, '/a/b');
  assert.equal(errors[0].schemaPath, '#/properties/a/properties/b/minLength');
  assert.equal(errors[0].keyword, 'minLength');
});

test('数组元素的实例路径带下标', () => {
  const schema: Schema = {
    type: 'array',
    items: { type: 'integer', minimum: 10 },
  };
  const errors = validate([20, 3, 15, 7], schema);
  assert.deepEqual(
    errors.map((e) => e.instancePath),
    ['/1', '/3'],
  );
  assert.ok(errors.every((e) => e.schemaPath === '#/items/minimum'));
});

test('required / additionalProperties / dependentRequired 的规则路径', () => {
  const schema: Schema = {
    type: 'object',
    properties: { a: { type: 'string' }, vip: { type: 'boolean' } },
    required: ['a'],
    additionalProperties: false,
    dependentRequired: { vip: ['level'] },
  };
  const errors = validate({ vip: true, extra: 1 }, schema);
  const byKeyword = Object.fromEntries(errors.map((e) => [e.keyword, e]));
  assert.equal(byKeyword.required.instancePath, '');
  assert.equal(byKeyword.required.schemaPath, '#/required');
  assert.equal(byKeyword.additionalProperties.instancePath, '/extra');
  assert.equal(
    byKeyword.additionalProperties.schemaPath,
    '#/additionalProperties',
  );
  assert.equal(byKeyword.dependentRequired.schemaPath, '#/dependentRequired');
});

test('数值与字符串约束', () => {
  const schema: Schema = {
    type: 'number',
    exclusiveMinimum: 1,
    multipleOf: 0.5,
  };
  assert.equal(validate(2.5, schema).length, 0);
  assert.equal(validate(1, schema)[0].keyword, 'exclusiveMinimum');
  assert.equal(validate(2.3, schema)[0].keyword, 'multipleOf');

  const s: Schema = { type: 'string', pattern: '^[a-z]+$' };
  assert.equal(validate('abc', s).length, 0);
  assert.equal(validate('abC', s)[0].keyword, 'pattern');
});

test('uniqueItems 与 enum/const', () => {
  assert.equal(
    validate([1, 2, 1], { type: 'array', uniqueItems: true })[0].keyword,
    'uniqueItems',
  );
  assert.equal(validate('b', { enum: ['a', 'b'] }).length, 0);
  assert.equal(validate('c', { enum: ['a', 'b'] })[0].keyword, 'enum');
  assert.equal(validate(5, { const: 4 })[0].keyword, 'const');
});

test('超出承诺子集的关键字直接拒绝，不静默忽略', () => {
  for (const bad of [
    { type: 'string', format: 'email' },
    { anyOf: [{ type: 'string' }] },
    { $ref: '#/definitions/x' },
    { type: 'object', minProperties: 1 },
    { type: ['string', 'null'] },
  ]) {
    assert.throws(
      () => assertSupported(bad as unknown as Schema),
      (e: Error) => e instanceof UnsupportedSchemaError || e instanceof Error,
    );
  }
});
