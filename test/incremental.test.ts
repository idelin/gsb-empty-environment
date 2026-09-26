import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DELETE,
  IncrementalValidator,
  type SchemaNode,
  type ValidationError,
} from '../src/index.ts';

const schema: SchemaNode = {
  type: 'object',
  required: ['name', 'age', 'start', 'end'],
  properties: {
    name: { type: 'string', minLength: 2 },
    age: { type: 'integer', minimum: 0, maximum: 150 },
    start: { type: 'number' },
    end: { type: 'number' },
    level: { type: 'string', enum: ['a', 'b', 'c'] },
    tags: { type: 'array', items: { type: 'string', minLength: 1 }, minItems: 1, maxItems: 3 },
    address: {
      type: 'object',
      required: ['city'],
      properties: {
        city: { type: 'string' },
        zip: { type: 'string', pattern: '^\\d{6}$' },
      },
    },
  },
  rules: [{ op: 'lt', left: 'start', right: 'end' }],
};

function validDoc(): Record<string, unknown> {
  return {
    name: 'Alice',
    age: 30,
    start: 1,
    end: 2,
    level: 'b',
    tags: ['x'],
    address: { city: 'Shanghai', zip: '200000' },
  };
}

function keys(errors: ValidationError[]): string[] {
  return errors.map((e) => `${e.instancePath}|${e.schemaPath}|${e.message}`).sort();
}

test('错误同时携带实例路径与规则路径', () => {
  const v = new IncrementalValidator(schema, {
    name: 'A',
    age: 30,
    start: 5,
    end: 2,
    tags: ['ok'],
  });
  const errors = v.getErrors();
  const byInstance = new Map(errors.map((e) => [e.instancePath, e]));
  assert.equal(byInstance.get('/name')?.schemaPath, '#/properties/name/minLength');
  assert.equal(byInstance.get('/start')?.schemaPath, '#/rules/0');
  // 缺失的 required 字段：实例路径指向缺失字段本身
  assert.ok(byInstance.has('/address') === false);
  const missing = errors.filter((e) => e.schemaPath.endsWith('/required'));
  assert.equal(missing.length, 0); // address 非必填
  for (const e of errors) {
    assert.ok(e.instancePath.startsWith('/'), 'instancePath 必须是实例内路径');
    assert.ok(e.schemaPath.startsWith('#/'), 'schemaPath 必须是 schema 内路径');
  }
});

test('局部修改只重验依赖该路径的规则（跨字段规则有依赖索引）', () => {
  const v = new IncrementalValidator(schema, validDoc());

  // 改 start：重验 /start 自身规则 + 跨字段规则 #/rules/0，不碰 age/name
  v.update('/start', 100);
  assert.ok(v.lastExecuted.some((id) => id.startsWith('#/rules/0@')), '应重验跨字段规则');
  assert.ok(v.lastExecuted.some((id) => id.startsWith('#/properties/start/')), '应重验 start 自身规则');
  assert.ok(
    v.lastExecuted.every((id) => !id.includes('/properties/age') && !id.includes('/properties/name')),
    '不应重验无关字段',
  );

  // 改 age：与 start<end 无关，#/rules/0 不得执行
  v.update('/age', 41);
  assert.ok(v.lastExecuted.length > 0);
  assert.ok(
    v.lastExecuted.every(
      (id) => id.startsWith('#/properties/age/') || id.startsWith('#/required#age@'),
    ),
    `只执行依赖 /age 的规则: ${v.lastExecuted.join(', ')}`,
  );
  assert.ok(!v.lastExecuted.some((id) => id.startsWith('#/rules/0@')), '无关跨字段规则不重验');
  assert.ok(
    v.lastExecuted.every((id) => !id.includes('/properties/name') && !id.includes('/properties/end')),
    '无关字段 z 的规则不重验',
  );
});

test('输出新增与撤销错误', () => {
  const v = new IncrementalValidator(schema, { ...validDoc(), name: 'A' });
  assert.equal(v.getErrors().length, 1);

  // 修复：撤销 1 条，新增 0 条
  let r = v.update('/name', 'Alice');
  assert.equal(r.added.length, 0);
  assert.equal(r.removed.length, 1);
  assert.equal(r.removed[0].instancePath, '/name');
  assert.equal(r.removed[0].schemaPath, '#/properties/name/minLength');

  // 再次破坏：新增 1 条，撤销 0 条
  r = v.update('/name', '');
  assert.equal(r.added.length, 1);
  assert.equal(r.removed.length, 0);
  assert.equal(r.added[0].schemaPath, '#/properties/name/minLength');

  // 改成仍然非法但违反不同规则：先制造 pattern 错误
  r = v.update('/address/zip', 'abc');
  assert.equal(r.added.length, 1);
  assert.equal(r.added[0].instancePath, '/address/zip');
  assert.equal(r.added[0].schemaPath, '#/properties/address/properties/zip/pattern');
});

test('删除父节点使子错误撤销', () => {
  const doc = validDoc();
  (doc.address as Record<string, unknown>).zip = 'bad';
  const v = new IncrementalValidator(schema, doc);
  assert.ok(v.getErrors().some((e) => e.instancePath === '/address/zip'));

  const r = v.remove('/address');
  assert.equal(r.added.length, 0);
  assert.ok(r.removed.some((e) => e.instancePath === '/address/zip'));
  assert.ok(v.getErrors().every((e) => !e.instancePath.startsWith('/address')));

  // 删除 required 字段本身 → 新增缺失错误；删父节点 → 该错误撤销
  const v2 = new IncrementalValidator(schema, validDoc());
  const r2 = v2.remove('/name');
  assert.ok(r2.added.some((e) => e.instancePath === '/name' && e.schemaPath === '#/required'));
});

test('删除数组元素后下标规则随之收缩', () => {
  const v = new IncrementalValidator(schema, { ...validDoc(), tags: ['ok', '', 'fine'] });
  assert.ok(v.getErrors().some((e) => e.instancePath === '/tags/1'));

  // 删除第 0 个元素后，原来的 '' 移到 /tags/0，错误应跟着收缩且集合与全量一致
  const r = v.remove('/tags/0');
  assert.deepEqual(keys(v.getErrors()), keys(v.validateFull()));
  assert.ok(v.getErrors().some((e) => e.instancePath === '/tags/0'));
  assert.ok(r.removed.some((e) => e.instancePath === '/tags/1'));
});

test('类型变化导致子树规则重建', () => {
  const v = new IncrementalValidator(schema, validDoc());
  // address 由对象变成字符串：子规则撤销，只留 type 错误
  const r = v.update('/address', 'not-an-object');
  assert.ok(r.added.some((e) => e.instancePath === '/address' && e.schemaPath.endsWith('/type')));
  assert.ok(v.getErrors().every((e) => !e.instancePath.startsWith('/address/')));
  // 恢复成合法对象：子规则重新生效
  v.update('/address', { city: 'Beijing', zip: '100000' });
  assert.equal(v.getErrors().length, 0);
  assert.deepEqual(keys(v.getErrors()), keys(v.validateFull()));
});

test('schema 子集各关键字行为', () => {
  const s: SchemaNode = {
    type: 'object',
    properties: {
      i: { type: 'integer' },
      n: { type: 'number', exclusiveMinimum: 0, exclusiveMaximum: 10 },
      e: { enum: [1, 'two', [3]] },
      arr: { type: 'array', items: { type: 'number' }, minItems: 2 },
    },
    rules: [{ op: 'gte', left: 'n', right: 'i' }],
  };
  const v = new IncrementalValidator(s, { i: 1.5, n: 0, e: 4, arr: [1] });
  const byPath = new Map(v.getErrors().map((e) => [`${e.instancePath} ${e.schemaPath}`, e]));
  assert.ok(byPath.has('/i #/properties/i/type'));
  assert.ok(byPath.has('/n #/properties/n/exclusiveMinimum'));
  assert.ok(byPath.has('/e #/properties/e/enum'));
  assert.ok(byPath.has('/arr #/properties/arr/minItems'));
  // 跨字段 gte：n=0, i=1.5 → 违反
  assert.ok(byPath.has('/n #/rules/0'));
  // enum 支持数组深比较
  v.update('/e', [3]);
  assert.ok(v.getErrors().every((e) => e.instancePath !== '/e'));
});

test('update 支持 DELETE 哨兵', () => {
  const v = new IncrementalValidator(schema, validDoc());
  const r = v.update('/level', DELETE);
  assert.equal(r.added.length, 0);
  assert.equal((v.getDocument() as Record<string, unknown>).level, undefined);
});

test('全量与增量错误集一致（固定操作序列）', () => {
  const v = new IncrementalValidator(schema, validDoc());
  const ops: Array<['set' | 'del', string, unknown?]> = [
    ['set', '/name', ''],
    ['set', '/age', -3],
    ['set', '/start', 9],
    ['set', '/tags', ['ok', '', 'x', 'y']],
    ['del', '/address'],
    ['set', '/end', 1],
    ['set', '/age', 'abc'],
    ['set', '/tags/1', 'fixed'],
    ['del', '/tags/0'],
    ['set', '/address', { zip: '1' }],
    ['set', '/level', 'zzz'],
    ['set', '/level', 'a'],
    ['del', '/name'],
    ['set', '/name', 'Bob'],
  ];
  for (const [op, p, val] of ops) {
    if (op === 'set') v.update(p, val);
    else v.remove(p);
    assert.deepEqual(keys(v.getErrors()), keys(v.validateFull()), `不一致发生于 ${op} ${p}`);
  }
});

test('全量与增量错误集一致（确定性随机操作）', () => {
  let seed = 42;
  const rnd = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  const v = new IncrementalValidator(schema, validDoc());
  const paths = [
    '/name', '/age', '/start', '/end', '/level',
    '/tags', '/tags/0', '/tags/1', '/tags/2',
    '/address', '/address/city', '/address/zip',
  ];
  const values: unknown[] = [
    0, -1, 200, 3.5, 'ok', '', 'abcdef', true, null,
    ['a'], ['x', '', 'y', 'z', 'w'], { city: 'BJ' }, 42, '200000',
  ];
  for (let i = 0; i < 300; i++) {
    const p = paths[Math.floor(rnd() * paths.length)];
    const val = values[Math.floor(rnd() * values.length)];
    try {
      if (rnd() < 0.25) v.remove(p);
      else v.update(p, val);
    } catch {
      // 非法路径操作（父节点不存在等）跳过
    }
    assert.deepEqual(keys(v.getErrors()), keys(v.validateFull()), `不一致发生于第 ${i} 步 (${p})`);
  }
});

test('replaceDocument 全量重建', () => {
  const v = new IncrementalValidator(schema, validDoc());
  v.update('/age', -5);
  const errors = v.replaceDocument(validDoc());
  assert.equal(errors.length, 0);
  assert.deepEqual(keys(v.getErrors()), keys(v.validateFull()));
});
