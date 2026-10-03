import { generate } from './generator.ts';
import { generateCounterexample, listRules } from './counterexample.ts';
import { validate } from './validator.ts';
import type { Schema } from './schema.ts';

const SEED = 20261004;

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

const show = (label: string, data: unknown): void => {
  console.log(`\n=== ${label} ===`);
  console.log(typeof data === 'string' ? data : JSON.stringify(data, null, 2));
};

console.log(`seed = ${SEED}（相同 seed 结果完全一致）`);

// 1. 正例
const positive = generate(userSchema, SEED);
if (positive.ok) {
  show('正例（满足全部规则）', positive.value);
  const errors = validate(positive.value, userSchema);
  console.log(`独立验证器验真: ${errors.length === 0 ? '通过，0 个错误' : JSON.stringify(errors)}`);
} else {
  show('正例构造失败', positive.reason);
}

// 2. 每条规则一个反例
console.log('\n=== 单规则反例（只违反指定规则，其余保持满足）===');
for (const rule of listRules(userSchema)) {
  const result = generateCounterexample(userSchema, rule, SEED);
  if (!result.ok) {
    console.log(`\n[${rule.schemaPath}]\n  无法构造: ${result.reason}`);
    continue;
  }
  console.log(`\n[${rule.schemaPath}]`);
  console.log(`  实例: ${JSON.stringify(result.instance)}`);
  for (const v of result.violations) {
    console.log(
      `  违规: instancePath=${JSON.stringify(v.instancePath)} ` +
        `schemaPath=${v.schemaPath} (${v.keyword}): ${v.message}`,
    );
  }
}

// 3. 矛盾 schema：无正例，明确说明而非伪造
const contradictory: Schema = {
  type: 'object',
  properties: {
    id: { type: 'string', minLength: 5, maxLength: 2 },
    flag: { type: 'boolean' },
  },
  required: ['id'],
  additionalProperties: false,
};
const bad = generate(contradictory, SEED);
show('矛盾 schema（minLength=5 > maxLength=2）', bad.ok ? bad.value : `无正例: ${bad.reason}`);

// 4. 原理上不可构造的反例：说明原因而非伪造
const tricky: Schema = { type: 'integer', minimum: 1, maximum: 9, multipleOf: 1 };
const impossible = generateCounterexample(
  tricky,
  { schemaPath: '#/multipleOf', keyword: 'multipleOf' },
  SEED,
);
show(
  '不可构造的反例（integer 且 multipleOf=1）',
  impossible.ok ? impossible.instance : `无法构造: ${impossible.reason}`,
);
