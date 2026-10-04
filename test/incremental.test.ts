import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/jsonpath/compiler.ts';
import {
  IncrementalResults,
  VersionedDocument,
  resolveCursor,
  setPath,
  deletePath,
} from '../src/jsonpath/incremental.ts';
import { evaluate } from '../src/jsonpath/interpreter.ts';
import { canonical } from '../src/jsonpath/path.ts';
import { EditError } from '../src/jsonpath/errors.ts';
import type { JsonValue } from '../src/jsonpath/ast.ts';

test('增删改后结果差量：added/removed/changed', () => {
  const doc = new VersionedDocument({
    items: [
      { id: 'a', n: 1 },
      { id: 'b', n: 5 },
    ],
  } as JsonValue);
  const inc = new IncrementalResults(compile('$.items[?(@.n > 2)].id'), doc);
  const d0 = inc.sync();
  assert.deepEqual(d0.added.map((m) => canonical(m.path)), ['$["items"][1]["id"]']);
  assert.deepEqual(d0.removed, []);

  // 修改：b.n 5→1（移出结果），新增 c.n=9（进入结果）
  doc.apply([
    { op: 'set', path: ['items', 1, 'n'], value: 1 },
    { op: 'set', path: ['items', 2], value: { id: 'c', n: 9 } },
  ]);
  const d1 = inc.sync();
  assert.deepEqual(d1.added.map((m) => canonical(m.path)), ['$["items"][2]["id"]']);
  assert.deepEqual(d1.removed.map((m) => canonical(m.path)), ['$["items"][1]["id"]']);

  // 值变化但路径不变 → changed
  doc.apply([{ op: 'set', path: ['items', 2, 'id'], value: 'c2' }]);
  const d2 = inc.sync();
  assert.deepEqual(d2.added, []);
  assert.deepEqual(d2.removed, []);
  assert.equal(d2.changed.length, 1);
  assert.equal(d2.changed[0]!.before, 'c');
  assert.equal(d2.changed[0]!.after, 'c2');

  // 删除 → removed
  doc.apply([{ op: 'delete', path: ['items', 2] }]);
  const d3 = inc.sync();
  assert.deepEqual(d3.removed.map((m) => canonical(m.path)), ['$["items"][2]["id"]']);
});

test('删除数组元素后旧游标仍绑定旧版本', () => {
  const doc = new VersionedDocument({ a: [10, 20, 30] } as JsonValue);
  const inc = new IncrementalResults(compile('$.a[*]'), doc);
  inc.sync();
  const cursors = inc.cursors();
  assert.equal(cursors.length, 3);
  const old = cursors[1]!; // $["a"][1] @ v0 → 20

  doc.apply([{ op: 'delete', path: ['a', 0] }]); // v1: [20, 30]
  inc.sync();

  // 旧游标解析旧版本：路径与值都不变
  const resolved = resolveCursor(doc, old);
  assert.equal(resolved!.value, 20);
  assert.deepEqual(resolved!.path, ['a', 1]);
  // 新版本同路径已是别的值（20 移到了 [0]）
  const shifted = resolveCursor(doc, { path: ['a', 1], version: doc.current });
  assert.equal(shifted!.value, 30);
  // 旧版本根未被破坏（结构共享，不可变）
  assert.deepEqual(doc.get(0), { a: [10, 20, 30] });
  assert.deepEqual(doc.get(1), { a: [20, 30] });
});

test('编辑原语：set/delete 与错误路径定位', () => {
  const t = setPath({ a: { b: 1 } }, ['a', 'c'], 2);
  assert.deepEqual(t, { a: { b: 1, c: 2 } });
  const t2 = setPath({ a: [1] }, ['a', 1], 9); // 追加
  assert.deepEqual(t2, { a: [1, 9] });
  const t3 = deletePath({ a: [1, 2, 3] }, ['a', 1]);
  assert.deepEqual(t3, { a: [1, 3] });
  assert.throws(() => deletePath({ a: 1 }, ['b']), (e: unknown) => {
    assert.ok(e instanceof EditError);
    assert.equal(e.path, '$["b"]');
    return true;
  });
  assert.throws(() => setPath({ a: [] }, ['a', 5], 1), EditError);
});

test('增量结果与参考解释器一致', () => {
  const doc = new VersionedDocument({ xs: [1, 2, 3], obj: { k: 'v' } } as JsonValue);
  const prog = compile('$..*');
  const inc = new IncrementalResults(prog, doc);
  inc.sync();
  doc.apply([
    { op: 'set', path: ['obj', 'nested'], value: { deep: [true, null] } },
    { op: 'delete', path: ['xs', 0] },
  ]);
  inc.sync();
  const fromInc = new Set(inc.matches().map((m) => canonical(m.path)));
  const fresh = new Set(evaluate(doc.get(), prog).map((m) => canonical(m.path)));
  assert.deepEqual(fromInc, fresh);
});
