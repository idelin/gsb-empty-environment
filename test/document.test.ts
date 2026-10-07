import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { RangeAffinity, VersionedDocument, range } from '../src/index.ts';

describe('VersionedDocument 基本编辑', () => {
  it('每次事务版本号递增', () => {
    const doc = new VersionedDocument('hello world');
    assert.equal(doc.version, 0);
    assert.equal(doc.insert(5, ','), 1);
    assert.equal(doc.delete(0, 1), 2);
    assert.equal(doc.text, 'ello, world');
    assert.equal(doc.version, 2);
  });

  it('事务内多个编辑基于同一基准状态且原子应用', () => {
    const doc = new VersionedDocument('a.b.c');
    doc.applyTransaction([
      { start: 0, end: 1, text: 'x' },
      { start: 4, end: 5, text: 'z' },
    ]);
    assert.equal(doc.text, 'x.b.z');
    assert.equal(doc.version, 1);
  });

  it('事务内重叠编辑被拒绝且文档不变', () => {
    const doc = new VersionedDocument('abcdef');
    assert.throws(() =>
      doc.applyTransaction([
        { start: 1, end: 4, text: 'x' },
        { start: 3, end: 5, text: 'y' },
      ]),
    );
    assert.equal(doc.text, 'abcdef');
    assert.equal(doc.version, 0);
  });

  it('越界编辑被拒绝', () => {
    const doc = new VersionedDocument('abc');
    assert.throws(() => doc.replace(2, 9, 'x'), RangeError);
    assert.equal(doc.version, 0);
  });

  it('transact 回调收集编辑为一个事务', () => {
    const doc = new VersionedDocument('foo bar');
    doc.transact((tx) => {
      tx.replace(0, 3, 'baz');
      tx.insert(7, '!');
    });
    assert.equal(doc.text, 'baz bar!');
    assert.equal(doc.version, 1);
  });
});

describe('撤销 / 重做', () => {
  it('一次事务是一次可撤销单元', () => {
    const doc = new VersionedDocument('a.b.c');
    doc.applyTransaction([
      { start: 0, end: 1, text: 'x' },
      { start: 4, end: 5, text: 'z' },
    ]);
    assert.equal(doc.text, 'x.b.z');
    assert.ok(doc.undo());
    assert.equal(doc.text, 'a.b.c');
    assert.ok(doc.redo());
    assert.equal(doc.text, 'x.b.z');
  });

  it('撤销产生新版本号，编辑链只增不减', () => {
    const doc = new VersionedDocument('abc');
    doc.insert(0, '>');
    const afterInsert = doc.version;
    doc.undo();
    assert.ok(doc.version > afterInsert);
    assert.equal(doc.text, 'abc');
  });

  it('新事务清空重做栈', () => {
    const doc = new VersionedDocument('abc');
    doc.insert(0, '>');
    doc.undo();
    doc.insert(0, '<');
    assert.equal(doc.canRedo, false);
    assert.equal(doc.text, '<abc');
  });

  it('空文档上撤销/重做返回 false', () => {
    const doc = new VersionedDocument('x');
    assert.equal(doc.undo(), false);
    assert.equal(doc.redo(), false);
  });

  it('连续撤销多个事务按逆序回退', () => {
    const doc = new VersionedDocument('');
    doc.insert(0, 'a');
    doc.insert(1, 'b');
    doc.insert(2, 'c');
    assert.equal(doc.text, 'abc');
    doc.undo();
    doc.undo();
    assert.equal(doc.text, 'a');
    doc.redo();
    assert.equal(doc.text, 'ab');
  });
});

describe('沿版本编辑链映射', () => {
  it('把旧版本范围映射穿过多个后续版本', () => {
    const doc = new VersionedDocument('hello world');
    const v0 = doc.version;
    doc.insert(0, '>> '); // v1
    doc.insert(doc.length, ' <<'); // v2
    assert.deepEqual(doc.mapRange(range(2, 5), v0), range(5, 8));
  });

  it('后续编辑触及范围内部则映射失效', () => {
    const doc = new VersionedDocument('hello world');
    const v0 = doc.version;
    doc.replace(2, 4, 'XX'); // 触及 [0,5) 内部
    assert.equal(doc.mapRange(range(0, 5), v0), undefined);
    // 不触及内部的编辑仍然可以映射
    const doc2 = new VersionedDocument('hello world');
    const w0 = doc2.version;
    doc2.replace(6, 8, 'XX');
    assert.deepEqual(doc2.mapRange(range(0, 5), w0), range(0, 5));
  });

  it('映射可以穿过撤销产生的新版本', () => {
    const doc = new VersionedDocument('hello');
    doc.insert(0, '>> '); // v1: ">> hello"
    const v1 = doc.version;
    doc.undo(); // v2: "hello"
    // v1 上的范围 [3,8)（对应 "hello"）穿过撤销映射回 [0,5)
    assert.deepEqual(doc.mapRange(range(3, 8), v1), range(0, 5));
  });

  it('fromVersion 等于当前版本时原样返回', () => {
    const doc = new VersionedDocument('abc');
    assert.deepEqual(doc.mapRange(range(1, 2), doc.version), range(1, 2));
  });

  it('非法版本号抛错', () => {
    const doc = new VersionedDocument('abc');
    assert.throws(() => doc.mapRange(range(0, 1), 3), RangeError);
    assert.throws(() => doc.mapRange(range(0, 1), -1), RangeError);
  });

  it('mapPosition 沿链映射并支持亲和性', () => {
    const doc = new VersionedDocument('abc');
    const v0 = doc.version;
    doc.insert(1, 'XY');
    assert.equal(doc.mapPosition(1, v0, 'before'), 1);
    assert.equal(doc.mapPosition(1, v0, 'after'), 3);
    assert.equal(doc.mapPosition(3, v0), 5);
  });

  it('Collapse 亲和性下边界插入被排除', () => {
    const doc = new VersionedDocument('hello');
    const v0 = doc.version;
    doc.insert(0, '>> ');
    assert.deepEqual(
      doc.mapRange(range(0, 5), v0, RangeAffinity.Collapse),
      range(3, 8),
    );
  });
});
