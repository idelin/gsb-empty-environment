import assert from 'node:assert/strict';
import { test } from 'node:test';

import { UnsupportedKeywordError, validate } from '../src/validator.ts';

const userSchema = {
  type: 'object',
  required: ['id', 'name'],
  additionalProperties: false,
  properties: {
    id: { type: 'integer', minimum: 1 },
    name: { type: 'string', minLength: 1 },
    email: { type: 'string', pattern: '^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$' },
    role: { enum: ['user', 'admin'] },
    tags: { type: 'array', items: { type: 'string' }, uniqueItems: true },
  },
};

test('合法实例通过', () => {
  const result = validate(userSchema, {
    id: 1,
    name: 'Ada',
    email: 'ada@example.com',
    role: 'user',
    tags: ['dev'],
  });
  assert.equal(result.valid, true);
  assert.deepEqual(result.errors, []);
});

test('type 失败同时给出实例路径与规则路径', () => {
  const result = validate({ type: 'integer' }, 1.5);
  assert.equal(result.valid, false);
  assert.equal(result.errors.length, 1);
  assert.deepEqual(
    {
      instancePath: result.errors[0].instancePath,
      schemaPath: result.errors[0].schemaPath,
      keyword: result.errors[0].keyword,
    },
    { instancePath: '', schemaPath: '/type', keyword: 'type' },
  );
});

test('number 类型也接受 integer', () => {
  assert.equal(validate({ type: 'number' }, 3).valid, true);
  assert.equal(validate({ type: 'integer' }, 3).valid, true);
  assert.equal(validate({ type: 'integer' }, 3.1).valid, false);
});

test('嵌套 pattern 失败的双路径', () => {
  const result = validate(userSchema, { id: 1, name: 'A', email: 'bad' });
  const error = result.errors.find((item) => item.keyword === 'pattern');
  assert.ok(error);
  assert.equal(error.instancePath, '/email');
  assert.equal(error.schemaPath, '/properties/email/pattern');
});

test('required 失败定位到根实例与 /required', () => {
  const result = validate(userSchema, { id: 1 });
  const error = result.errors.find((item) => item.keyword === 'required');
  assert.ok(error);
  assert.equal(error.instancePath, '');
  assert.equal(error.schemaPath, '/required');
  assert.match(error.message, /name/);
});

test('minimum 失败定位', () => {
  const result = validate(userSchema, { id: 0, name: '' });
  const error = result.errors.find((item) => item.keyword === 'minimum');
  assert.ok(error);
  assert.equal(error.instancePath, '/id');
  assert.equal(error.schemaPath, '/properties/id/minimum');
});

test('enum 与 additionalProperties 定位', () => {
  const enumError = validate(userSchema, { id: 1, name: 'A', role: 'root' }).errors.find(
    (item) => item.keyword === 'enum',
  );
  assert.ok(enumError);
  assert.equal(enumError.instancePath, '/role');
  assert.equal(enumError.schemaPath, '/properties/role/enum');

  const extraError = validate(userSchema, { id: 1, name: 'A', hack: true }).errors.find(
    (item) => item.keyword === 'additionalProperties',
  );
  assert.ok(extraError);
  assert.equal(extraError.instancePath, '');
  assert.equal(extraError.schemaPath, '/additionalProperties');
});

test('uniqueItems 失败定位到第二个重复元素', () => {
  const result = validate(userSchema, { id: 1, name: 'A', tags: ['x', 'x'] });
  const error = result.errors.find((item) => item.keyword === 'uniqueItems');
  assert.ok(error);
  assert.equal(error.instancePath, '/tags/1');
  assert.equal(error.schemaPath, '/properties/tags/uniqueItems');
});

test('数组元素 schema 失败路径带下标', () => {
  const result = validate(userSchema, { id: 1, name: 'A', tags: ['ok', 2] });
  const error = result.errors.find((item) => item.schemaPath === '/properties/tags/items/type');
  assert.ok(error);
  assert.equal(error.instancePath, '/tags/1');
});

test('布尔 schema', () => {
  assert.equal(validate(false, {}).valid, false);
  assert.equal(validate(true, {}).valid, true);
  assert.equal(validate(false, {}).errors[0].schemaPath, '');
});

test('组合关键字 anyOf / oneOf / not', () => {
  const union = { anyOf: [{ type: 'string' }, { type: 'integer' }] };
  assert.equal(validate(union, 's').valid, true);
  assert.equal(validate(union, true).valid, false);
  assert.equal(validate(union, true).errors[0].schemaPath, '/anyOf');

  const exactly = { oneOf: [{ type: 'integer' }, { minimum: 0 }] };
  assert.equal(validate(exactly, -1).valid, true);
  assert.equal(validate(exactly, 1).valid, false);
  assert.equal(validate(exactly, 1).errors[0].schemaPath, '/oneOf');

  const forbidden = { not: { type: 'null' } };
  assert.equal(validate(forbidden, null).valid, false);
  assert.equal(validate(forbidden, null).errors[0].schemaPath, '/not');
  assert.equal(validate(forbidden, 1).valid, true);
});

test('未承诺的关键字直接拒绝', () => {
  assert.throws(
    () => validate({ $ref: '#/definitions/x' }, {}),
    (error: unknown) => error instanceof UnsupportedKeywordError && error.keyword === '$ref',
  );
  assert.throws(
    () => validate({ type: 'string', format: 'email' }, 'a'),
    (error: unknown) => error instanceof UnsupportedKeywordError && error.keyword === 'format',
  );
  assert.throws(
    () => validate({ type: 'array', items: [] }, []),
    (error: unknown) => error instanceof UnsupportedKeywordError,
  );
});
