import { validate } from '../src/validator.ts';

const schema = {
  $defs: {
    positiveInt: { type: 'integer', minimum: 1 },
  },
  type: 'object',
  required: ['name', 'age'],
  properties: {
    name: { type: 'string', minLength: 1, maxLength: 20 },
    age: { $ref: '#/$defs/positiveInt' },
    email: { type: 'string', pattern: '^[^@]+@[^@]+$' },
    tags: { type: 'array', items: { type: 'string' }, maxItems: 3 },
  },
  additionalProperties: false,
};

function show(title: string, result: unknown): void {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(result, null, 2));
}

// 1. 合法实例
show('合法实例', validate(schema, {
  name: 'Ada', age: 36, email: 'ada@example.com', tags: ['math'],
}));

// 2. 无效实例：错误同时给出实例路径与规则路径
show('无效实例', validate(schema, {
  name: '', age: 0, email: 'bad', extra: true,
}));

// 3. 预算耗尽：incomplete 而非 invalid
const bigArray = Array.from({ length: 1000 }, (_, i) => i);
show('预算耗尽', validate(
  { type: 'array', items: { type: 'integer' } },
  bigArray,
  { budget: 100 },
));

// 4. 取消：AbortSignal
const controller = new AbortController();
controller.abort();
show('取消', validate(schema, { name: 'Ada', age: 36 }, { signal: controller.signal }));

// 5. 错误数上限：与 invalid 区分
const manyBadFields = Object.fromEntries(
  Array.from({ length: 20 }, (_, i) => [`f${i}`, 'x']),
);
const wideSchema = {
  type: 'object',
  properties: Object.fromEntries(
    Array.from({ length: 20 }, (_, i) => [`f${i}`, { type: 'integer' }]),
  ),
};
show('错误数上限', validate(wideSchema, manyBadFields, { maxErrors: 3 }));

// 6. 上次耗尽不影响下次调用
show('耗尽后的下一次调用', validate(
  { type: 'array', items: { type: 'integer' } },
  bigArray,
));
