import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateCounterexample, listRules } from '../src/counterexample.ts';
import { validate } from '../src/validator.ts';
import type { Schema } from '../src/schema.ts';

const userSchema: Schema = {
  type: 'object',
  properties: {
    name: { type: 'string', minLength: 2, maxLength: 10 },
    age: { type: 'integer', minimum: 0, maximum: 150 },
    email: { type: 'string', pattern: '^[a-z0-9.]+@[a-z]+\\.com$' },
    tags: {
      type: 'array',
      items: { type: 'string', minLength: 1, maxLength: 5 },
      minItems: 1,
      maxItems: 3,
      uniqueItems: true,
    },
    vip: { type: 'boolean' },
    level: { type: 'integer', minimum: 1, maximum: 10 },
  },
  required: ['name', 'age'],
  additionalProperties: false,
  dependentRequired: { vip: ['level'] },
};

test('每条规则都能造出"只违反该规则"的反例，并经独立验证器验真', () => {
  const rules = listRules(userSchema);
  assert.ok(rules.length > 15, `规则数应足够多，实际 ${rules.length}`);
  for (const rule of rules) {
    for (const seed of [1, 7, 20261004]) {
      const result = generateCounterexample(userSchema, rule, seed);
      assert.ok(result.ok, `${rule.schemaPath} (seed=${seed}) 应可构造: ${JSON.stringify(result)}`);
      const errors = validate(result.instance, userSchema);
      assert.ok(errors.length > 0, '反例必须至少违反一条规则');
      for (const e of errors) {
        assert.equal(
          e.schemaPath,
          rule.schemaPath,
          `不得误伤其他规则: ${JSON.stringify(e)}（目标 ${rule.schemaPath}）`,
        );
        assert.equal(e.keyword, rule.keyword);
      }
      assert.deepEqual(errors, result.violations);
    }
  }
});

test('反例确定性：相同 seed 结果一致', () => {
  const rule = { schemaPath: '#/properties/age/minimum', keyword: 'minimum' };
  assert.deepEqual(
    generateCounterexample(userSchema, rule, 42),
    generateCounterexample(userSchema, rule, 42),
  );
});

test('原理上不可构造的反例：说明原因而非伪造', () => {
  const cases: Array<[Schema, string, RegExp]> = [
    [{ type: 'integer', multipleOf: 1 }, '#/multipleOf', /非 1 倍数|不可违反|不存在/],
    [{ type: 'boolean', enum: [true, false] }, '#/enum', /enum/],
    [{ type: 'string', minLength: 0 }, '#/minLength', /minLength=0/],
    [{ type: 'array', minItems: 0 }, '#/minItems', /minItems=0/],
    [
      { type: 'array', maxItems: 1, uniqueItems: true },
      '#/uniqueItems',
      /maxItems/,
    ],
    [
      { type: 'string', minLength: 2, pattern: '^[a-z]{3,}$' },
      '#/minLength',
      /pattern/,
    ],
    [
      { type: 'array', items: { type: 'boolean' }, maxItems: 2, uniqueItems: true },
      '#/maxItems',
      /取值域|互异/,
    ],
  ];
  for (const [schema, schemaPath, pattern] of cases) {
    const keyword = schemaPath.split('/').pop()!;
    const result = generateCounterexample(schema, { schemaPath, keyword }, 1);
    assert.ok(!result.ok, `${schemaPath} 不应伪造反例`);
    assert.match(result.reason, pattern);
  }
});

test('矛盾 schema 的反例请求：报告无正例', () => {
  const result = generateCounterexample(
    { type: 'string', minLength: 5, maxLength: 2 },
    { schemaPath: '#/minLength', keyword: 'minLength' },
    1,
  );
  assert.ok(!result.ok);
  assert.match(result.reason, /矛盾/);
});

test('反例确实满足其他所有规则（抽查具体实例）', () => {
  const result = generateCounterexample(
    userSchema,
    { schemaPath: '#/properties/age/maximum', keyword: 'maximum' },
    3,
  );
  assert.ok(result.ok);
  const instance = result.instance as Record<string, unknown>;
  assert.ok((instance.age as number) > 150, 'age 必须违反 maximum');
  // 其余规则全部满足：删掉 age 后应零错误
  const { age: _age, ...restObj } = instance;
  const restSchema: Schema = {
    ...userSchema,
    properties: { ...userSchema.properties },
    required: (userSchema.required ?? []).filter((k) => k !== 'age'),
  };
  delete restSchema.properties!.age;
  assert.deepEqual(validate(restObj, restSchema), []);
});
