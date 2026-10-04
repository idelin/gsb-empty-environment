/**
 * 可运行样例：node examples/demo.ts
 * 覆盖：编译/求值、流式增量输出、有界暂存与溢写、文档编辑差量、稳定游标、查询预算。
 */
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  BudgetTracker,
  IncrementalResults,
  VersionedDocument,
  canonical,
  compile,
  evaluate,
  resolveCursor,
  streamQuery,
  walk,
} from '../src/jsonpath/index.ts';
import type { JsonValue } from '../src/jsonpath/index.ts';

const doc = {
  store: {
    book: [
      { title: 'Sayings', price: 8, tags: ['ref'] },
      { title: 'Sword', price: 12 },
      { title: 'Moby', price: 9, tags: ['sea', 'classic'] },
    ],
    'shelf/a': { capacity: 30 },
  },
  budget: 10,
} as JsonValue;

console.log('== 1. 编译 + 参考解释器（结果携带规范路径） ==');
const prog = compile('$.store.book[?(@.price <= $.budget)].title');
for (const m of evaluate(doc, prog)) console.log(`  ${canonical(m.path)} = ${JSON.stringify(m.value)}`);

console.log('\n== 2. 流式执行：结果一旦确定立即输出 ==');
const events = [...walk(doc)];
const total = events.length;
streamQuery(doc, compile('$.store.book[*].title'), {
  onMatch: (m, n) => console.log(`  [event ${String(n).padStart(3)}/${total}] ${canonical(m.path)} = ${JSON.stringify(m.value)}`),
});

console.log('\n== 3. 负下标：尾部索引在数组闭合前不可判定 ==');
streamQuery(doc, compile('$.store.book[-1].title'), {
  onMatch: (m, n) => console.log(`  [event ${n}/${total}] ${canonical(m.path)} = ${JSON.stringify(m.value)}`),
});

console.log('\n== 4. 有界暂存 + 溢写（大树、小缓冲） ==');
const big = { rows: Array.from({ length: 1000 }, (_, i) => ({ id: i, hot: i % 7 === 0 })), limit: 0 } as JsonValue;
const dir = mkdtempSync(join(tmpdir(), 'jp-demo-'));
const res = streamQuery(big, compile('$.rows[-3:].id'), { maxBuffered: 16, spillDir: dir });
console.log(`  spilled=${res.spilled} 尾部3行: ${res.matches.map((m) => m.value).join(', ')}`);
console.log(`  临时文件清理后目录内容: [${readdirSync(dir).join(', ')}]`);
rmSync(dir, { recursive: true, force: true });

console.log('\n== 5. 文档编辑 → 结果差量 + 稳定游标 ==');
const vdoc = new VersionedDocument(doc);
const inc = new IncrementalResults(compile('$.store.book[?(@.price <= $.budget)].title'), vdoc);
inc.sync();
const cursor = inc.cursors()[1]!; // Moby @ v0
console.log(`  游标绑定: ${canonical(cursor.path)} @ v${cursor.version}`);
vdoc.apply([
  { op: 'set', path: ['store', 'book', 0, 'price'], value: 99 }, // Sayings 涨价 → 移出结果
  { op: 'set', path: ['store', 'book', 3], value: { title: 'Dune', price: 7 } }, // 新增
  { op: 'delete', path: ['store', 'book', 1] }, // 删除 Sword
]);
const diff = inc.sync();
console.log(`  v${diff.version} added:   ${diff.added.map((m) => canonical(m.path)).join(', ')}`);
console.log(`  v${diff.version} removed: ${diff.removed.map((m) => canonical(m.path)).join(', ')}`);
console.log(`  v${diff.version} changed: ${diff.changed.map((c) => canonical(c.path)).join(', ') || '(none)'}`);
const oldValue = resolveCursor(vdoc, cursor);
console.log(`  旧游标仍解析旧版本: ${canonical(oldValue!.path)} = ${JSON.stringify(oldValue!.value)}`);

console.log('\n== 6. 查询预算（解析/展开/比较） ==');
const budget = new BudgetTracker({ parse: 1000, expand: 1000, compare: 2 });
try {
  evaluate(doc, compile('$.store.book[?(@.price > 1)].title'), { budget });
} catch (e) {
  console.log(`  ${(e as Error).name}: ${(e as Error).message}`);
}
