import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generate } from '../src/generator.ts';
import { validate } from '../src/validator.ts';
import { UnsupportedSchemaError, type Schema } from '../src/schema.ts';

const SCHEMAS: Schema[] = [
  { type: 'string', minLength: 2, maxLength: 10 },
  { type: 'string', pattern: '^[a-z0-9.]+@[a-z]+\\.com$' },
  { type: 'string', minLength: 3, maxLength: 6, pattern: '^[a-z]{2,4}[0-9]+$' },
  { type: 'integer', minimum: 0, maximum: 150 },
  { type: 'integer', minimum: 3, maximum: 99, multipleOf: 3 },
  { type: 'number', exclusiveMinimum: 0, exclusiveMaximum: 1 },
  { type: 'number', minimum: 2, maximum: 8, multipleOf: 0.5 },
  { type: 'boolean' },
  { type: 'null' },
  { type: 'array', items: { type: 'integer' }, minItems: 1, maxItems: 4 },
  {
    type: 'array',
    items: { type: 'string', minLength: 1 },
    minItems: 2,
    maxItems: 3,
    uniqueItems: true,
  },
  { type: 'array', items: { type: 'boolean' }, minItems: 2, uniqueItems: true },
  { type: 'integer', enum: [1, 2, 3], minimum: 2 },
  { const: { a: [1, 2] } },
  {
    type: 'object',
    properties: {
      name: { type: 'string', minLength: 2, maxLength: 10 },
      age: { type: 'integer', minimum: 0, maximum: 150 },
      vip: { type: 'boolean' },
      level: { type: 'integer', minimum: 1, maximum: 10 },
    },
    required: ['name', 'age', 'vip'],
    additionalProperties: false,
    dependentRequired: { vip: ['level'] },
  },
];

test('正例生成：多种 schema × 多种 seed 均通过独立验证器验真', () => {
  for (const schema of SCHEMAS) {
    for (const seed of [1, 2, 3, 42, 20261004]) {
      const result = generate(schema, seed);
      assert.ok(result.ok, `应能构造正例: ${JSON.stringify(schema)} -> ${JSON.stringify(result)}`);
      const errors = validate(result.value, schema);
      assert.deepEqual(errors, [], `正例必须零错误: ${JSON.stringify(result.value)}`);
    }
  }
});

test('相同 seed 结果完全一致（确定性）', () => {
  const schema = SCHEMAS[SCHEMAS.length - 1];
  for (const seed of [7, 99, 123456]) {
    assert.deepEqual(generate(schema, seed), generate(schema, seed));
  }
});

test('dependentRequired 联动：触发属性出现时依赖属性必被补齐', () => {
  const schema: Schema = {
    type: 'object',
    properties: { vip: { type: 'boolean' }, level: { type: 'integer' } },
    required: ['vip'],
    dependentRequired: { vip: ['level'] },
  };
  for (const seed of [1, 2, 3, 4, 5]) {
    const result = generate(schema, seed);
    assert.ok(result.ok);
    const obj = result.value as Record<string, unknown>;
    if ('vip' in obj) assert.ok('level' in obj, 'vip 出现必须携带 level');
  }
});

test('矛盾 schema 无正例：返回原因而非伪造', () => {
  const cases: Array<[Schema, RegExp]> = [
    [{ type: 'string', minLength: 5, maxLength: 2 }, /minLength.*maxLength/],
    [{ type: 'integer', minimum: 10, maximum: 3 }, /区间.*为空/],
    [{ type: 'array', minItems: 3, maxItems: 1 }, /minItems.*maxItems/],
    [
      { type: 'array', items: { type: 'boolean' }, minItems: 3, uniqueItems: true },
      /uniqueItems/,
    ],
    [
      {
        type: 'object',
        properties: {},
        required: ['x'],
        additionalProperties: false,
      },
      /required/,
    ],
    [{ type: 'string', enum: ['a', 'b'], minLength: 5 }, /enum/],
    [{ type: 'number', const: 5, minimum: 10 }, /const/],
    [{ type: 'integer', minimum: 1, maximum: 2, multipleOf: 5 }, /倍/],
    [
      { type: 'string', pattern: '^[a-z]{4}$', maxLength: 2 },
      /pattern|无交集/,
    ],
  ];
  for (const [schema, pattern] of cases) {
    const result = generate(schema, 1);
    assert.ok(!result.ok, `矛盾 schema 不应产出正例: ${JSON.stringify(schema)}`);
    assert.match(result.reason, pattern);
    assert.equal((result as { value?: unknown }).value, undefined, '不得伪造值');
  }
});

test('超出子集的 schema 抛错而非静默处理', () => {
  assert.throws(
    () => generate({ type: 'string', format: 'email' } as unknown as Schema),
    UnsupportedSchemaError,
  );
});
