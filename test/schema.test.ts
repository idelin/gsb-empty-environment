import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateSchema } from '../src/schema.ts';

test('基本类型通过 / 失败并带路径', () => {
  assert.deepEqual(validateSchema(1, { type: 'integer' }), []);
  assert.equal(validateSchema(1.5, { type: 'integer' })[0]!.path, '');
  assert.deepEqual(validateSchema({ a: 1 }, { type: 'object', required: ['b'], additionalProperties: false, properties: {} }), [
    { path: '', reason: 'missing required property', expected: 'b', actual: 'undefined' },
    { path: '/a', reason: 'additional property not allowed', actual: 'a' },
  ]);
});

test('嵌套缺失与越界路径可定位', () => {
  const schema = {
    type: 'object' as const,
    required: ['user'],
    additionalProperties: false,
    properties: {
      user: {
        type: 'object' as const,
        required: ['email'],
        additionalProperties: true,
        properties: { email: { type: 'string' } },
      },
    },
  };
  const issues = validateSchema({ user: {} }, schema);
  assert.equal(issues[0]!.path, '/user');
  assert.equal(issues[0]!.expected, 'email');

  const arrIssues = validateSchema({ user: { email: 5 } }, schema);
  assert.equal(arrIssues[0]!.path, '/user/email');
  assert.equal(arrIssues[0]!.expected, 'string');
});

test('additionalProperties 默认允许，false 时拒绝', () => {
  assert.deepEqual(validateSchema({ a: 1, b: 2 }, { type: 'object', properties: { a: { type: 'integer' } } }), []);
  const issues = validateSchema(
    { a: 1, b: 2 },
    { type: 'object', properties: { a: { type: 'integer' } }, additionalProperties: false },
  );
  assert.equal(issues[0]!.path, '/b');
});

test('数组 bounds、items、enum、anyOf', () => {
  assert.equal(validateSchema([], { type: 'array', minItems: 1 })[0]!.reason, 'array too short');
  assert.equal(validateSchema([1, 2], { type: 'array', maxItems: 1, items: { type: 'integer' } })[0]!.path, '');
  assert.equal(validateSchema([1, 'x'], { type: 'array', items: { type: 'integer' } })[0]!.path, '/1');
  assert.equal(validateSchema('z', { enum: ['a', 'b'] })[0]!.reason, 'value not in enum');
  assert.deepEqual(validateSchema(2, { anyOf: [{ type: 'string' }, { type: 'integer' }] }), []);
  assert.equal(validateSchema(true, { anyOf: [{ type: 'string' }, { type: 'integer' }] })[0]!.reason, 'no anyOf branch matched');
});
