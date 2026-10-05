import test from 'node:test';
import assert from 'node:assert/strict';
import {
  FragmentDocument,
  ReferenceDocument,
  SourceRegistry,
  verifyAgainstReference,
} from '../src/index.ts';

const READERS = ['alice', 'bob', 'carol'];

function setup(): {
  registry: SourceRegistry;
  doc: FragmentDocument;
  ref: ReferenceDocument;
} {
  const registry = new SourceRegistry();
  registry.addSource('public', ['alice', 'bob', 'carol']);
  registry.addSource('team', ['alice', 'bob']);
  registry.addSource('secret', ['alice']);
  return {
    registry,
    doc: new FragmentDocument(registry),
    ref: new ReferenceDocument(registry),
  };
}

function verifyAll(doc: FragmentDocument, ref: ReferenceDocument): void {
  verifyAgainstReference(doc, ref, [...READERS, 'mallory']);
}

test('重复引用：复制片段保留来源，删除一份不影响另一份', () => {
  const { doc, ref } = setup();
  doc.append('secret', 'abc');
  ref.append('secret', 'abc');
  doc.copyRange(0, 3, 3);
  ref.copyRange(0, 3, 3);

  assert.equal(doc.text(), 'abcabc');
  const hidden = doc.queryReader('carol');
  assert.equal(hidden.text, '██████');
  assert.deepEqual(hidden.redactions, [{ start: 0, end: 6, reasons: ['secret'] }]);
  assert.equal(doc.queryReader('alice').text, 'abcabc');

  doc.deleteRange(0, 3);
  ref.deleteRange(0, 3);
  assert.equal(doc.text(), 'abc');
  assert.deepEqual(doc.queryReader('carol').redactions, [
    { start: 0, end: 3, reasons: ['secret'] },
  ]);
  verifyAll(doc, ref);
});

test('删除：跨权限边界删除后剩余字符来源不变', () => {
  const { doc, ref } = setup();
  doc.append('public', 'hello ');
  ref.append('public', 'hello ');
  doc.append('secret', 'XYZ');
  ref.append('secret', 'XYZ');

  doc.deleteRange(4, 7);
  ref.deleteRange(4, 7);
  assert.equal(doc.text(), 'hellYZ');
  assert.deepEqual(
    doc.expand().map((c) => c.sources),
    [['public'], ['public'], ['public'], ['public'], ['secret'], ['secret']],
  );
  assert.deepEqual(doc.queryReader('bob').redactions, [
    { start: 4, end: 6, reasons: ['secret'] },
  ]);
  verifyAll(doc, ref);
});

test('跨权限边界替换：新文本获得被替换范围的权限交集', () => {
  const { doc, ref } = setup();
  doc.append('team', 'foo');
  ref.append('team', 'foo');
  doc.append('secret', 'bar');
  ref.append('secret', 'bar');

  doc.replaceRange(1, 5, 'XY');
  ref.replaceRange(1, 5, 'XY');
  assert.equal(doc.text(), 'fXYr');
  assert.deepEqual(doc.expand()[1].sources, ['secret', 'team']);

  const bob = doc.queryReader('bob');
  assert.equal(bob.text, 'f███');
  assert.deepEqual(bob.redactions, [{ start: 1, end: 4, reasons: ['secret'] }]);

  const carol = doc.queryReader('carol');
  assert.deepEqual(carol.redactions, [
    { start: 0, end: 1, reasons: ['team'] },
    { start: 1, end: 3, reasons: ['secret', 'team'] },
    { start: 3, end: 4, reasons: ['secret'] },
  ]);
  verifyAll(doc, ref);
});

test('空范围替换等价于格式化插入：继承相邻来源交集', () => {
  const { doc, ref } = setup();
  doc.append('team', 'ab');
  ref.append('team', 'ab');
  doc.append('secret', 'cd');
  ref.append('secret', 'cd');

  doc.replaceRange(2, 2, '-');
  ref.replaceRange(2, 2, '-');
  assert.equal(doc.text(), 'ab-cd');
  assert.deepEqual(doc.expand()[2].sources, ['secret', 'team']);
  assert.deepEqual(doc.queryReader('bob').redactions, [
    { start: 2, end: 5, reasons: ['secret'] },
  ]);
  verifyAll(doc, ref);
});

test('合并片段：新来源读者为各来源交集，不为字符放宽权限', () => {
  const { registry, doc, ref } = setup();
  doc.append('public', 'ab');
  ref.append('public', 'ab');
  doc.append('team', 'cd');
  ref.append('team', 'cd');

  doc.mergeRange(0, 4, 'merged');
  ref.mergeRange(0, 4, 'merged');

  assert.deepEqual([...registry.readersOf('merged')].sort(), ['alice', 'bob']);
  assert.deepEqual(registry.get('merged').derivedFrom, ['public', 'team']);

  assert.equal(doc.queryReader('bob').text, 'abcd');
  const carol = doc.queryReader('carol');
  assert.deepEqual(carol.redactions, [
    { start: 0, end: 2, reasons: ['merged'] },
    { start: 2, end: 4, reasons: ['merged', 'team'] },
  ]);
  verifyAll(doc, ref);
});

test('格式化：插入与重排不丢失限制', () => {
  const { doc, ref } = setup();
  doc.append('secret', 'abc');
  ref.append('secret', 'abc');

  doc.insertFormatting(1, '—');
  ref.insertFormatting(1, '—');
  assert.equal(doc.text(), 'a—bc');
  assert.deepEqual(doc.expand()[1].sources, ['secret']);
  assert.equal(doc.queryReader('carol').text, '████');

  doc.append('public', '!');
  ref.append('public', '!');
  doc.insertFormatting(4, ' ');
  ref.insertFormatting(4, ' ');
  assert.deepEqual(doc.expand()[4].sources, ['public', 'secret']);
  assert.deepEqual(doc.queryReader('carol').redactions, [
    { start: 0, end: 5, reasons: ['secret'] },
  ]);

  doc.mapRange(0, 4, (ch) => ch.toUpperCase());
  ref.mapRange(0, 4, (ch) => ch.toUpperCase());
  assert.equal(doc.text(), 'A—BC !');
  assert.deepEqual(doc.expand()[0].sources, ['secret']);

  doc.mapRange(4, 5, (ch) => ch + ch);
  ref.mapRange(4, 5, (ch) => ch + ch);
  assert.equal(doc.text(), 'A—BC  !');
  assert.deepEqual(doc.expand()[5].sources, ['public', 'secret']);
  verifyAll(doc, ref);
});

test('移动：跨权限边界移动保留逐字符来源', () => {
  const { doc, ref } = setup();
  doc.append('public', 'ab');
  ref.append('public', 'ab');
  doc.append('secret', 'CD');
  ref.append('secret', 'CD');
  doc.append('public', 'ef');
  ref.append('public', 'ef');

  doc.moveRange(2, 4, 0);
  ref.moveRange(2, 4, 0);
  assert.equal(doc.text(), 'CDabef');
  assert.deepEqual(doc.expand()[0].sources, ['secret']);
  assert.deepEqual(doc.expand()[2].sources, ['public']);
  assert.deepEqual(doc.queryReader('bob').redactions, [
    { start: 0, end: 2, reasons: ['secret'] },
  ]);
  verifyAll(doc, ref);
});

test('未知读者：所有受限内容均被遮挡并给出原因来源', () => {
  const { doc, ref } = setup();
  doc.append('public', 'hi ');
  ref.append('public', 'hi ');
  doc.append('team', 'there');
  ref.append('team', 'there');

  const mallory = doc.queryReader('mallory');
  assert.equal(mallory.text, '████████');
  assert.deepEqual(mallory.redactions, [
    { start: 0, end: 3, reasons: ['public'] },
    { start: 3, end: 8, reasons: ['team'] },
  ]);
  verifyAll(doc, ref);
});

test('合并冲突的同名来源注册被拒绝', () => {
  const { registry } = setup();
  assert.throws(() => registry.addSource('secret', ['alice', 'bob']));
  registry.addSource('secret', ['alice']);
});
