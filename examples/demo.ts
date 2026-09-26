import assert from 'node:assert/strict';
import { IncrementalValidator, type SchemaNode, type ValidationError } from '../src/index.ts';

const schema: SchemaNode = {
  type: 'object',
  required: ['name', 'age', 'start', 'end'],
  properties: {
    name: { type: 'string', minLength: 2 },
    age: { type: 'integer', minimum: 0, maximum: 150 },
    start: { type: 'number' },
    end: { type: 'number' },
    tags: { type: 'array', items: { type: 'string', minLength: 1 }, maxItems: 3 },
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

function printErrors(title: string, errors: ValidationError[]): void {
  console.log(`\n${title} (${errors.length} 条)`);
  for (const e of errors) {
    console.log(`  实例路径 ${e.instancePath || '/'}  |  规则路径 ${e.schemaPath}`);
    console.log(`    ${e.message}`);
  }
  if (errors.length === 0) console.log('  （无）');
}

function printChange(title: string, v: IncrementalValidator, added: ValidationError[], removed: ValidationError[]): void {
  console.log(`\n=== ${title} ===`);
  console.log(`本次实际执行的规则 (${v.lastExecuted.length} 条):`);
  for (const id of v.lastExecuted) console.log(`  ${id}`);
  printErrors('新增错误', added);
  printErrors('撤销错误', removed);
}

// 初始文档带有 5 处错误
const v = new IncrementalValidator(schema, {
  name: 'A', // 太短
  age: 200, // 超过 maximum
  start: 10,
  end: 5, // 违反 start < end
  tags: ['ok', ''], // 第二个元素为空串
  address: { city: 'Shanghai', zip: 'abc' }, // zip 不匹配 pattern
});

console.log('规则总数（实例化后）:', v.ruleCount);
printErrors('初始全量校验错误', v.getErrors());

// 1) 修改 start：只重验依赖 /start 的规则（含跨字段规则 start<end），不碰 age/name
let r = v.update('/start', 3);
printChange('修改 /start = 3（修复 start<end）', v, r.added, r.removed);

// 2) 修改 age：与跨字段规则无关，#/rules/0 不会被执行
r = v.update('/age', 36);
printChange('修改 /age = 36（与 start<end 无关）', v, r.added, r.removed);

// 3) 删除父节点 /address：子节点 zip 的错误随之撤销
r = v.remove('/address');
printChange('删除 /address（子错误被撤销）', v, r.added, r.removed);

// 4) 修复剩余错误
r = v.update('/name', 'Alice');
printChange('修改 /name = "Alice"', v, r.added, r.removed);
r = v.update('/tags/1', 'tag');
printChange('修改 /tags/1 = "tag"', v, r.added, r.removed);

// 5) 一致性：增量错误集 == 从头全量校验错误集
printErrors('最终增量错误集', v.getErrors());
assert.deepEqual(v.getErrors(), v.validateFull());
console.log('\n一致性检查通过：增量错误集与全量校验完全一致 ✔');
