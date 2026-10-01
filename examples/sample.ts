import { validate } from '../src/index.ts';

function show(label: string, result: unknown): void {
  console.log(`\n=== ${label} ===`);
  console.log(JSON.stringify(result, null, 2));
}

const schema = {
  type: 'object',
  required: ['name', 'age'],
  properties: {
    name: { type: 'string', minLength: 1, pattern: '^[A-Z]' },
    age: { $ref: '#/$defs/age' },
  },
  additionalProperties: false,
  $defs: { age: { type: 'integer', minimum: 0, maximum: 150 } },
};

show('1. valid', validate(schema, { name: 'Ada', age: 36 }));

show('2. invalid (instance path + schema path)', validate(schema, { name: 'ada', age: 200, extra: 1 }));

show('3. incomplete: budget exhausted (not invalid!)',
  validate(schema, { name: 'Ada', age: 36 }, { budget: 4 }));

// 大量重复引用：缓存存在，但每次解引用仍计预算
const props: Record<string, unknown> = {};
const doc: Record<string, unknown> = {};
for (let i = 0; i < 500; i++) { props['f' + i] = { $ref: '#/$defs/age' }; doc['f' + i] = i; }
const refBomb = { type: 'object', properties: props, $defs: schema.$defs };
show('4. incomplete: 500 duplicate $refs vs budget 1000',
  validate(refBomb, doc, { budget: 1000 }));

const controller = new AbortController();
const tricky = {
  name: 'Ada',
  get age() { controller.abort(); return 36; }, // 验证中途取消
};
show('5. incomplete: cancelled mid-run',
  validate(schema, tricky, { signal: controller.signal }));

const bad20 = Object.fromEntries(Array.from({ length: 20 }, (_, i) => ['p' + i, i]));
const wideSchema = { type: 'object', properties: Object.fromEntries(Object.keys(bad20).map((k) => [k, { type: 'string' }])) };
show('6. incomplete: error limit reached (maxErrors=3)',
  validate(wideSchema, bad20, { maxErrors: 3 }));

show('7. next call starts fresh (same input, default budget)',
  validate(schema, { name: 'Ada', age: 36 }));
