import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, SchemaError } from '../src/validator.ts';

const userSchema = {
  type: 'object',
  required: ['name', 'age'],
  properties: {
    name: { type: 'string', minLength: 1, maxLength: 20 },
    age: { type: 'integer', minimum: 0, maximum: 150 },
    email: { type: 'string', pattern: '^[^@]+@[^@]+$' },
    tags: { type: 'array', items: { type: 'string' }, maxItems: 5 },
  },
  additionalProperties: false,
};

test('合法实例返回 valid', () => {
  const r = validate(userSchema, { name: 'Ada', age: 36, tags: ['a', 'b'] });
  assert.equal(r.status, 'valid');
  assert.deepEqual(r.errors, []);
  assert.equal(r.incompleteReason, undefined);
});

test('无效实例返回 invalid，错误同时带实例路径和规则路径', () => {
  const r = validate(userSchema, { name: '', age: 200, extra: 1 });
  assert.equal(r.status, 'invalid');
  const byInstance = new Map(r.errors.map((e) => [e.instancePath, e]));
  assert.equal(byInstance.get('/name')?.schemaPath, '/properties/name/minLength');
  assert.equal(byInstance.get('/age')?.schemaPath, '/properties/age/maximum');
  assert.equal(byInstance.get('/extra')?.schemaPath, '/additionalProperties');
  for (const e of r.errors) {
    assert.ok(e.instancePath !== undefined && e.schemaPath !== undefined);
    assert.ok(e.message.length > 0);
  }
});

test('嵌套与数组路径正确', () => {
  const schema = {
    type: 'object',
    properties: {
      items: { type: 'array', items: { type: 'object', properties: { id: { type: 'integer' } } } },
    },
  };
  const r = validate(schema, { items: [{ id: 1 }, { id: 'x' }] });
  assert.equal(r.status, 'invalid');
  assert.equal(r.errors.length, 1);
  assert.equal(r.errors[0].instancePath, '/items/1/id');
  assert.equal(r.errors[0].schemaPath, '/properties/items/items/properties/id/type');
});

test('缺少必需属性', () => {
  const r = validate(userSchema, { age: 10 });
  assert.equal(r.status, 'invalid');
  assert.ok(r.errors.some((e) => e.schemaPath === '/required'));
});

test('pattern 校验生效', () => {
  const r = validate(userSchema, { name: 'A', age: 1, email: 'not-an-email' });
  assert.equal(r.status, 'invalid');
  assert.equal(r.errors[0].instancePath, '/email');
  assert.equal(r.errors[0].schemaPath, '/properties/email/pattern');
});

test('enum 与 const', () => {
  assert.equal(validate({ enum: [1, 'a', [true]] }, [true]).status, 'valid');
  assert.equal(validate({ enum: [1, 'a'] }, 2).status, 'invalid');
  assert.equal(validate({ const: { x: 1 } }, { x: 1 }).status, 'valid');
  assert.equal(validate({ const: { x: 1 } }, { x: 2 }).status, 'invalid');
});

test('预算耗尽返回 incomplete 而非 invalid', () => {
  const big = Array.from({ length: 500 }, (_, i) => i);
  const r = validate({ type: 'array', items: { type: 'integer' } }, big, { budget: 50 });
  assert.equal(r.status, 'incomplete');
  assert.equal(r.incompleteReason, 'budgetExhausted');
  assert.notEqual(r.status, 'invalid');
});

test('字符串匹配按长度计入共享预算', () => {
  const schema = { type: 'string', pattern: '^a+$' };
  const short = validate(schema, 'a'.repeat(10));
  const long = validate(schema, 'a'.repeat(1000));
  assert.equal(short.status, 'valid');
  assert.equal(long.status, 'valid');
  assert.ok(long.budget.consumed - short.budget.consumed >= 900,
    `长字符串应消耗更多预算: short=${short.budget.consumed}, long=${long.budget.consumed}`);
});

test('大量重复引用不能绕过预算：按引用次数计费', () => {
  const makeSchema = (n: number) => ({
    $defs: { x: { type: 'integer' } },
    type: 'object',
    properties: Object.fromEntries(
      Array.from({ length: n }, (_, i) => [`p${i}`, { $ref: '#/$defs/x' }]),
    ),
  });
  const makeInstance = (n: number) =>
    Object.fromEntries(Array.from({ length: n }, (_, i) => [`p${i}`, i]));

  const one = validate(makeSchema(1), makeInstance(1));
  const many = validate(makeSchema(50), makeInstance(50));
  assert.equal(one.status, 'valid');
  assert.equal(many.status, 'valid');
  // 50 个重复引用虽然指向同一缓存目标，消耗仍近似线性增长
  assert.ok(many.budget.consumed >= one.budget.consumed + 49 * 10,
    `重复引用未按次计费: 1次=${one.budget.consumed}, 50次=${many.budget.consumed}`);

  // 预算卡在中间时必须报 incomplete，而不是靠缓存"免费"通过
  const limited = validate(makeSchema(50), makeInstance(50), { budget: 200 });
  assert.equal(limited.status, 'incomplete');
  assert.equal(limited.incompleteReason, 'budgetExhausted');
});

test('递归 $ref 的合法 schema 正常终止', () => {
  const schema = {
    $ref: '#/$defs/node',
    $defs: {
      node: {
        type: 'object',
        properties: { child: { $ref: '#/$defs/node' }, value: { type: 'string' } },
      },
    },
  };
  const ok = validate(schema, { value: 'a', child: { value: 'b', child: { value: 'c' } } });
  assert.equal(ok.status, 'valid');
  const bad = validate(schema, { value: 'a', child: { value: 1 } });
  assert.equal(bad.status, 'invalid');
  assert.equal(bad.errors[0].instancePath, '/child/value');
});

test('循环引用不会栈溢出，由深度/预算兜底为 incomplete', () => {
  const r = validate({ $ref: '#' }, {}, { budget: 100_000 });
  assert.equal(r.status, 'incomplete');
  assert.equal(r.incompleteReason, 'budgetExhausted');
});

test('取消返回 incomplete/cancelled，且后续调用不受影响', () => {
  const controller = new AbortController();
  controller.abort();
  const r = validate(userSchema, { name: 'Ada', age: 36 }, { signal: controller.signal });
  assert.equal(r.status, 'incomplete');
  assert.equal(r.incompleteReason, 'cancelled');
  // 临时状态已清理，下一次调用（新信号）正常工作
  const again = validate(userSchema, { name: 'Ada', age: 36 });
  assert.equal(again.status, 'valid');
});

test('错误数上限与输入无效区分：errorLimitReached 是 incomplete', () => {
  const schema = {
    type: 'object',
    properties: Object.fromEntries(
      Array.from({ length: 20 }, (_, i) => [`f${i}`, { type: 'integer' }]),
    ),
  };
  const instance = Object.fromEntries(
    Array.from({ length: 20 }, (_, i) => [`f${i}`, 'not-int']),
  );
  const r = validate(schema, instance, { maxErrors: 5 });
  assert.equal(r.status, 'incomplete');
  assert.equal(r.incompleteReason, 'errorLimitReached');
  assert.equal(r.errors.length, 5);
  assert.notEqual(r.status, 'invalid');

  // 同样的输入，不设错误上限时才是 invalid
  const full = validate(schema, instance);
  assert.equal(full.status, 'invalid');
  assert.equal(full.errors.length, 20);
});

test('下次调用不继承上次耗尽状态', () => {
  const big = Array.from({ length: 500 }, (_, i) => i);
  const schema = { type: 'array', items: { type: 'integer' } };
  const exhausted = validate(schema, big, { budget: 10 });
  assert.equal(exhausted.status, 'incomplete');
  const fresh = validate(schema, big);
  assert.equal(fresh.status, 'valid');
  assert.equal(fresh.budget.consumed <= fresh.budget.limit, true);
});

test('承诺子集之外的关键字被忽略，子集冲突抛 SchemaError', () => {
  // 未承诺的关键字（format/title/$comment 等）直接忽略
  const r = validate({ type: 'string', format: 'email', title: 'x' }, 'anything');
  assert.equal(r.status, 'valid');
  // 非本地 $ref 属于子集之外，抛 SchemaError（调用方错误，不是验证结论）
  assert.throws(() => validate({ $ref: 'https://example.com/s.json' }, 1), SchemaError);
  assert.throws(() => validate({ $ref: '#/nope' }, 1), SchemaError);
});

test('布尔 schema', () => {
  assert.equal(validate(true, 123).status, 'valid');
  assert.equal(validate(false, 123).status, 'invalid');
});

test('type 数组与 integer/number 区分', () => {
  assert.equal(validate({ type: ['string', 'null'] }, null).status, 'valid');
  assert.equal(validate({ type: 'integer' }, 1.5).status, 'invalid');
  assert.equal(validate({ type: 'number' }, 1.5).status, 'valid');
});
