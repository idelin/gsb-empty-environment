import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReflectionEngine, type MessageSchema } from '../src/schema.ts';
import { SchemaValidationError, TruncatedFieldError } from '../src/errors.ts';
import { WireWriter } from '../src/wire.ts';

const addressSchema: MessageSchema = {
  name: 'Address',
  fields: [
    { name: 'city', number: 1, type: 'string', required: true },
    { name: 'zip', number: 2, type: 'fixed32' },
  ],
};

const personSchema: MessageSchema = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'uint64', required: true },
    { name: 'name', number: 2, type: 'string', required: true },
    { name: 'email', number: 3, type: 'string' },
    { name: 'age', number: 4, type: 'sint64' },
    { name: 'address', number: 5, type: 'message', message: addressSchema },
    { name: 'previous', number: 6, type: 'message', repeated: true, message: addressSchema },
    { name: 'luckyNumbers', number: 7, type: 'sint64', repeated: true, packed: true },
    { name: 'scores', number: 8, type: 'fixed32', repeated: true, packed: true },
    { name: 'role', number: 9, type: 'enum', enumValues: new Set([0, 1, 2]) },
    { name: 'avatar', number: 10, type: 'bytes' },
    { name: 'balance', number: 11, type: 'double' },
  ],
};

const engine = new ReflectionEngine();

const sample = {
  id: 9007199254740993n, // 超过 Number.MAX_SAFE_INTEGER, 验证 bigint 精度
  name: 'Ada',
  email: 'ada@example.com',
  age: -36n,
  address: { city: 'Shanghai', zip: 200000 },
  previous: [
    { city: 'Beijing', zip: 100000 },
    { city: 'Hangzhou', zip: 310000 },
  ],
  luckyNumbers: [7n, -13n, 300n],
  scores: [95, 82, 0xffffffff],
  role: 2,
  avatar: Uint8Array.from([1, 2, 3, 255]),
  balance: 3.5,
};

test('反射引擎: 多层嵌套 + packed repeated 全字段往返', () => {
  const bytes = engine.encode(personSchema, sample);
  const decoded = engine.decode(personSchema, bytes);
  assert.deepEqual(decoded, sample);
});

test('反射引擎: bigint 精度在 2^53 之上不丢失', () => {
  const decoded = engine.decode(personSchema, engine.encode(personSchema, sample));
  assert.equal(decoded.id, 9007199254740993n);
});

test('反射引擎: packed 与 unpacked 编码互通', () => {
  // 手工构造 unpacked 形式的 repeated sint64 (字段 7, wire type 0)
  const w = new WireWriter();
  w.writeUint64(1, 1n);
  w.writeString(2, 'x');
  w.writeSInt64(7, 5n);
  w.writeSInt64(7, -6n);
  w.writeSInt64(7, 7n);
  const decoded = engine.decode(personSchema, w.finish());
  assert.deepEqual(decoded.luckyNumbers, [5n, -6n, 7n]);
});

test('反射校验: wire type 不匹配抛 SchemaValidationError', () => {
  const w = new WireWriter();
  w.writeUint64(1, 1n);
  w.writeString(2, 'x');
  w.writeString(4, 'not-a-varint'); // age 应为 varint
  assert.throws(
    () => engine.decode(personSchema, w.finish()),
    (err: unknown) => {
      assert.ok(err instanceof SchemaValidationError);
      assert.match((err as Error).message, /wire type 不匹配/);
      assert.equal((err as SchemaValidationError).fieldPath, 'Person.age');
      return true;
    },
  );
});

test('反射校验: 缺少 required 字段被拒绝', () => {
  const w = new WireWriter();
  w.writeString(2, 'no-id'); // 缺 id
  assert.throws(
    () => engine.decode(personSchema, w.finish()),
    SchemaValidationError,
  );
  assert.throws(
    () => engine.encode(personSchema, { name: 'x' }),
    SchemaValidationError,
  );
});

test('反射校验: 枚举值越界被拒绝 (编码与解码双向)', () => {
  assert.throws(
    () => engine.encode(personSchema, { id: 1n, name: 'x', role: 9 }),
    SchemaValidationError,
  );
  const w = new WireWriter();
  w.writeUint64(1, 1n);
  w.writeString(2, 'x');
  w.writeUint64(9, 9n); // 非法枚举
  assert.throws(() => engine.decode(personSchema, w.finish()), SchemaValidationError);
});

test('反射校验: 未知字段默认安全跳过, 严格模式报错', () => {
  const w = new WireWriter();
  w.writeUint64(1, 1n);
  w.writeString(2, 'x');
  w.writeString(99, 'future-field'); // schema 未定义
  const bytes = w.finish();
  const decoded = engine.decode(personSchema, bytes);
  assert.equal(decoded.name, 'x');
  assert.throws(
    () => engine.decode(personSchema, bytes, { rejectUnknownFields: true }),
    SchemaValidationError,
  );
});

test('反射校验: 嵌套消息内部截断, 越界防护捕获所有非法截断点', () => {
  const full = engine.encode(personSchema, sample);
  let guardHits = 0;
  // 从尾部逐字节截断: 要么落在字段边界(合法短消息), 要么被安全防护捕获,
  // 绝不抛出非预期异常或越界读取
  for (let cut = full.length - 1; cut > 0; cut--) {
    try {
      engine.decode(personSchema, full.subarray(0, cut));
    } catch (err) {
      assert.ok(
        err instanceof TruncatedFieldError || err instanceof SchemaValidationError,
        `cut=${cut} 应为防护错误, 实际: ${err}`,
      );
      guardHits++;
    }
  }
  assert.ok(guardHits > 0, '字段内部截断必须被防护捕获');
  // 明确验证: 砍掉末尾 packed 字段的最后一个字节必抛
  assert.throws(
    () => engine.decode(personSchema, full.subarray(0, full.length - 1)),
    (err: unknown) =>
      err instanceof TruncatedFieldError || err instanceof SchemaValidationError,
  );
});

test('反射校验: 空消息与默认值省略', () => {
  const decoded = engine.decode(personSchema, engine.encode(personSchema, {
    id: 0n,
    name: '',
  }));
  assert.equal(decoded.id, 0n);
  assert.equal(decoded.name, '');
  assert.equal(decoded.luckyNumbers, undefined);
});
