import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AnnotatedDocument } from './document.ts';

// ---- 端点粘附：左端点插字 ----

test('[2,4) 左端插字：向内策略不纳入标注', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(2, 4, 'inward', 'inward'); // "cd"
  doc.applyEdit({ id: 1, start: 2, end: 2, text: 'XY' });
  assert.equal(doc.text, 'abXYcdef');
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [4, 6]); // 仍标注 "cd"
  assert.equal(doc.text.slice(ann.start, ann.end), 'cd');
});

test('[2,4) 左端插字：向外策略纳入标注', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(2, 4, 'outward', 'inward');
  doc.applyEdit({ id: 1, start: 2, end: 2, text: 'XY' });
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [2, 6]); // 标注 "XYcd"
  assert.equal(doc.text.slice(ann.start, ann.end), 'XYcd');
});

// ---- 端点粘附：右端点插字 ----

test('[2,4) 右端插字：向内策略不纳入，向外策略纳入', () => {
  const inward = new AnnotatedDocument('abcdef');
  const id1 = inward.addAnnotation(2, 4, 'inward', 'inward');
  inward.applyEdit({ id: 1, start: 4, end: 4, text: 'XY' });
  assert.deepEqual(
    (({ start, end }: { start: number; end: number }) => [start, end])(inward.getAnnotation(id1)!),
    [2, 4],
  );

  const outward = new AnnotatedDocument('abcdef');
  const id2 = outward.addAnnotation(2, 4, 'inward', 'outward');
  outward.applyEdit({ id: 1, start: 4, end: 4, text: 'XY' });
  const ann = outward.getAnnotation(id2)!;
  assert.deepEqual([ann.start, ann.end], [2, 6]);
  assert.equal(outward.text.slice(ann.start, ann.end), 'cdXY');
});

test('左右端点可独立选择粘附方向', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(2, 4, 'outward', 'inward');
  doc.applyBatch([
    { id: 1, start: 2, end: 2, text: 'L' },
    { id: 2, start: 4, end: 4, text: 'R' },
  ]);
  const ann = doc.getAnnotation(id)!;
  assert.equal(doc.text.slice(ann.start, ann.end), 'Lcd'); // 左纳入、右不纳入
});

// ---- 删除与零宽锚点 ----

test('删除全部覆盖内容时保留零宽锚点', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(2, 4);
  doc.applyEdit({ id: 1, start: 2, end: 4, text: '' });
  assert.equal(doc.text, 'abef');
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [2, 2]);
  assert.ok(doc.getAnnotation(id) !== undefined, '批注未被移除');
});

test('删除范围大于批注范围时也折叠为零宽锚点', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(2, 4);
  doc.applyEdit({ id: 1, start: 0, end: 6, text: '' });
  assert.equal(doc.text, '');
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [0, 0]);
});

test('覆盖删除不产生负长度', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(1, 5);
  // 批注内部被分段删除
  doc.applyBatch([
    { id: 1, start: 1, end: 3, text: '' },
    { id: 2, start: 3, end: 5, text: '' },
  ]);
  const ann = doc.getAnnotation(id)!;
  assert.ok(ann.end >= ann.start, '长度非负');
  assert.deepEqual([ann.start, ann.end], [1, 1]);

  // 零宽锚点两端均向内时，锚点处插字也不产生负长度
  const doc2 = new AnnotatedDocument('abcdef');
  const id2 = doc2.addAnnotation(3, 3, 'inward', 'inward');
  doc2.applyEdit({ id: 1, start: 3, end: 3, text: 'XY' });
  const ann2 = doc2.getAnnotation(id2)!;
  assert.ok(ann2.end >= ann2.start);
  assert.deepEqual([ann2.start, ann2.end], [3, 3]);
});

test('批注之前的删除使其整体平移', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(3, 5); // "de"
  doc.applyEdit({ id: 1, start: 0, end: 2, text: '' });
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [1, 3]);
  assert.equal(doc.text.slice(ann.start, ann.end), 'de');
});

test('批注内部部分删除：保留剩余内容', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(1, 5); // "bcde"
  doc.applyEdit({ id: 1, start: 2, end: 4, text: '' }); // 删 "cd"
  const ann = doc.getAnnotation(id)!;
  assert.equal(doc.text.slice(ann.start, ann.end), 'be');
});

// ---- 批量编辑：同位置按操作ID排序 ----

test('批量同位置插入按操作ID排序', () => {
  const doc = new AnnotatedDocument('ac');
  doc.applyBatch([
    { id: 3, start: 1, end: 1, text: '3' },
    { id: 1, start: 1, end: 1, text: '1' },
    { id: 2, start: 1, end: 1, text: '2' },
  ]);
  assert.equal(doc.text, 'a123c'); // 最终文本顺序 = 操作ID升序
});

test('批量编辑以原始坐标给出，相互自动变换', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(4, 5); // "e"
  doc.applyBatch([
    { id: 1, start: 0, end: 1, text: 'XX' }, // 头部替换，后续位置 +1
    { id: 2, start: 3, end: 4, text: 'Y' }, // 原始坐标删除 "d"
  ]);
  assert.equal(doc.text, 'XXbcYef');
  const ann = doc.getAnnotation(id)!;
  assert.equal(doc.text.slice(ann.start, ann.end), 'e');
});

// ---- UTF-16 与代理对 ----

test('公开偏移为 UTF-16：增补平面字符占 2 个偏移', () => {
  const doc = new AnnotatedDocument('a😀b');
  assert.equal(doc.length, 4); // "😀" 占 2 个 UTF-16 code unit
  const id = doc.addAnnotation(1, 3); // 恰好覆盖 "😀"
  doc.applyEdit({ id: 1, start: 0, end: 0, text: 'Z' });
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [2, 4]);
  assert.equal(doc.text.slice(ann.start, ann.end), '😀');
});

test('编辑边界禁止切开代理对：自动对齐到整对边界', () => {
  const doc = new AnnotatedDocument('a😀b'); // 偏移: a=0, 😀=1..2, b=3
  // 在代理对中间（偏移 2）插入，应被对齐到代理对之前（偏移 1）
  doc.applyEdit({ id: 1, start: 2, end: 2, text: 'X' });
  assert.equal(doc.text, 'aX😀b');

  // 删除区间切开代理对两侧，应扩展为删除整对
  const doc2 = new AnnotatedDocument('a😀b');
  doc2.applyEdit({ id: 1, start: 2, end: 3, text: '' }); // 起点在代理对中间
  assert.equal(doc2.text, 'ab');

  const doc3 = new AnnotatedDocument('a😀b');
  doc3.applyEdit({ id: 1, start: 1, end: 2, text: '' }); // 终点在代理对中间
  assert.equal(doc3.text, 'ab');
});

test('批注边界禁止切开代理对：创建时自动对齐', () => {
  const doc = new AnnotatedDocument('a😀b');
  const id = doc.addAnnotation(2, 3); // 起点切开代理对
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [1, 3]);
  assert.equal(doc.text.slice(ann.start, ann.end), '😀');
});

// ---- LF 与 CRLF ----

test('LF 文本中的批注随编辑移动', () => {
  const doc = new AnnotatedDocument('one\ntwo\nthree');
  const id = doc.addAnnotation(4, 7); // "two"
  doc.applyEdit({ id: 1, start: 0, end: 0, text: 'zero\n' });
  const ann = doc.getAnnotation(id)!;
  assert.equal(doc.text.slice(ann.start, ann.end), 'two');
  assert.deepEqual([ann.start, ann.end], [9, 12]);
});

test('CRLF 文本中的批注随编辑移动（\\r\\n 占 2 个 UTF-16 偏移）', () => {
  const doc = new AnnotatedDocument('one\r\ntwo\r\nthree');
  const id = doc.addAnnotation(5, 8); // "two"
  doc.applyEdit({ id: 1, start: 0, end: 0, text: 'zero\r\n' });
  const ann = doc.getAnnotation(id)!;
  assert.equal(doc.text.slice(ann.start, ann.end), 'two');
  assert.deepEqual([ann.start, ann.end], [11, 14]);
});

test('CRLF 换行整体删除不残留孤立 \\r 或 \\n', () => {
  const doc = new AnnotatedDocument('a\r\nb');
  const id = doc.addAnnotation(3, 4); // "b"
  doc.applyEdit({ id: 1, start: 1, end: 3, text: '' }); // 删除 "\r\n"
  assert.equal(doc.text, 'ab');
  const ann = doc.getAnnotation(id)!;
  assert.equal(doc.text.slice(ann.start, ann.end), 'b');
});

// ---- 撤销 ----

test('撤销编辑恢复原锚点', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(2, 4, 'outward', 'outward');
  doc.applyEdit({ id: 1, start: 2, end: 3, text: 'LONG' });
  assert.notDeepEqual(
    (({ start, end }: { start: number; end: number }) => [start, end])(doc.getAnnotation(id)!),
    [2, 4],
  );
  assert.ok(doc.undo());
  assert.equal(doc.text, 'abcdef');
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [2, 4]);
});

test('撤销批量编辑恢复全部锚点，可连续撤销', () => {
  const doc = new AnnotatedDocument('abcdef');
  const id = doc.addAnnotation(1, 2);
  doc.applyBatch([
    { id: 1, start: 0, end: 0, text: 'H' },
    { id: 2, start: 6, end: 6, text: 'T' },
  ]);
  doc.applyEdit({ id: 3, start: 0, end: 1, text: '' });
  assert.ok(doc.undo()); // 撤销第二次
  assert.equal(doc.text, 'HabcdefT');
  assert.ok(doc.undo()); // 撤销批次
  assert.equal(doc.text, 'abcdef');
  const ann = doc.getAnnotation(id)!;
  assert.deepEqual([ann.start, ann.end], [1, 2]);
  assert.ok(!doc.undo(), '历史为空时撤销返回 false');
});
