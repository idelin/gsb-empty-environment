import { test, assert } from './helpers.mjs';
import { DocumentStore, commitTransaction, applyInverse } from '../dist/index.js';

function setup() {
  const store = new DocumentStore();
  store.register('a.txt', 'foo bar foo');
  store.register('b.txt', '1,2,3');
  return store;
}

test('commit applies all docs against the same frozen versions', () => {
  const store = setup();
  const va = store.get('a.txt').version;
  const vb = store.get('b.txt').version;
  const res = commitTransaction(store, [
    { docId: 'a.txt', expectedVersion: va, pattern: /foo/g, replacement: 'baz' },
    { docId: 'b.txt', expectedVersion: vb, pattern: ',', replacement: ';' },
  ]);
  assert.equal(res.ok, true);
  assert.equal(store.get('a.txt').text, 'baz bar baz');
  assert.equal(store.get('b.txt').text, '1;2;3');
  assert.equal(store.get('a.txt').version, va + 1);
  assert.equal(store.get('b.txt').version, vb + 1);
  assert.equal(res.applied.length, 2);
  assert.equal(res.applied[0].edits.length, 2);
  assert.equal(res.applied[0].oldVersion, va);
  assert.equal(res.applied[0].newVersion, va + 1);
});

test('any version drift rejects the whole batch', () => {
  const store = setup();
  const va = store.get('a.txt').version;
  const vb = store.get('b.txt').version;
  // 并发修改：b.txt 在提交前被外部写入
  store.write('b.txt', 'changed externally');
  const res = commitTransaction(store, [
    { docId: 'a.txt', expectedVersion: va, pattern: /foo/g, replacement: 'baz' },
    { docId: 'b.txt', expectedVersion: vb, pattern: /,/g, replacement: ';' },
  ]);
  assert.equal(res.ok, false);
  assert.deepEqual(res.conflicts, [
    { docId: 'b.txt', expectedVersion: vb, actualVersion: vb + 1 },
  ]);
  // 整批拒绝：两个文档都保持原样
  assert.equal(store.get('a.txt').text, 'foo bar foo');
  assert.equal(store.get('a.txt').version, va);
  assert.equal(store.get('b.txt').text, 'changed externally');
});

test('unknown document rejects the batch', () => {
  const store = setup();
  const va = store.get('a.txt').version;
  const res = commitTransaction(store, [
    { docId: 'a.txt', expectedVersion: va, pattern: /foo/g, replacement: 'x' },
    { docId: 'ghost.txt', expectedVersion: 0, pattern: /x/g, replacement: 'y' },
  ]);
  assert.equal(res.ok, false);
  assert.equal(store.get('a.txt').text, 'foo bar foo');
});

test('duplicate document in one batch is rejected', () => {
  const store = setup();
  const v = store.get('a.txt').version;
  const res = commitTransaction(store, [
    { docId: 'a.txt', expectedVersion: v, pattern: /foo/, replacement: 'x' },
    { docId: 'a.txt', expectedVersion: v, pattern: /bar/, replacement: 'y' },
  ]);
  assert.equal(res.ok, false);
  assert.equal(store.get('a.txt').text, 'foo bar foo');
});

test('apply-then-undo restores text and version', () => {
  const store = setup();
  const before = store.get('a.txt');
  const res = commitTransaction(store, [
    { docId: 'a.txt', expectedVersion: before.version, pattern: /foo/g, replacement: 'x' },
  ]);
  assert.equal(res.ok, true);
  assert.equal(store.get('a.txt').text, 'x bar x');
  res.undo();
  assert.equal(store.get('a.txt').text, before.text);
  assert.equal(store.get('a.txt').version, before.version);
  // 重复 undo 报错
  assert.throws(() => res.undo(), /already undone/);
});

test('undo after concurrent modification throws', () => {
  const store = setup();
  const v = store.get('a.txt').version;
  const res = commitTransaction(store, [
    { docId: 'a.txt', expectedVersion: v, pattern: /foo/g, replacement: 'x' },
  ]);
  store.write('a.txt', 'tampered');
  assert.throws(() => res.undo(), /modified after commit/);
});

test('zero-width matches round-trip through apply and undo', () => {
  const store = new DocumentStore();
  store.register('z.txt', 'ab');
  const v = store.get('z.txt').version;
  const res = commitTransaction(store, [
    { docId: 'z.txt', expectedVersion: v, pattern: /(?:)/g, replacement: '-' },
  ]);
  assert.equal(res.ok, true);
  assert.equal(store.get('z.txt').text, '-a-b-');
  const applied = res.applied[0];
  // 可逆编辑：逆向应用 edits 还原旧文本
  assert.equal(applyInverse(applied.newText, applied.edits), applied.oldText);
  res.undo();
  assert.equal(store.get('z.txt').text, 'ab');
  assert.equal(store.get('z.txt').version, v);
});

test('non-participating capture expands to empty inside a transaction', () => {
  const store = new DocumentStore();
  store.register('c.txt', 'y');
  const v = store.get('c.txt').version;
  const res = commitTransaction(store, [
    { docId: 'c.txt', expectedVersion: v, pattern: /(x)|(y)/g, replacement: '<$1>' },
  ]);
  assert.equal(res.ok, true);
  assert.equal(store.get('c.txt').text, '<>');
  res.undo();
  assert.equal(store.get('c.txt').text, 'y');
});

test('transaction exposes range mapping consistent with the naive model', () => {
  const store = new DocumentStore();
  store.register('m.txt', 'one two three');
  const v = store.get('m.txt').version;
  const res = commitTransaction(store, [
    { docId: 'm.txt', expectedVersion: v, pattern: /two|three/g, replacement: 'X' },
  ]);
  assert.equal(res.ok, true);
  const applied = res.applied[0];
  assert.equal(applied.newText, 'one X X');
  const map = applied.result.mapPosition;
  // 保留区 "one " 映射后内容一致
  assert.equal(applied.newText.slice(map(0), map(4)), 'one ');
  // 每个编辑的新区间内容就是替换文本
  for (const e of applied.edits) {
    assert.equal(applied.newText.slice(e.newStart, e.newEnd), e.newText);
    assert.equal(applied.oldText.slice(e.oldStart, e.oldEnd), e.oldText);
  }
});
