import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RichTextDoc } from '../src/richtext.ts';

/** 全量交换操作日志（模拟两个副本同步）。 */
function sync(...docs: RichTextDoc[]): void {
  const logs = docs.map((d) => [...d.getOps()]);
  docs.forEach((doc, i) => {
    logs.forEach((ops, j) => {
      if (i !== j) doc.applyAll(ops);
    });
  });
}

function formats(doc: RichTextDoc): string[][] {
  return doc.getFormats();
}

function spanTuples(doc: RichTextDoc): Array<[string, number, number]> {
  return doc.getSpans().map((s) => [s.type, s.start, s.end]);
}

test('验收：并发 bold(AB) 与 underline(BC) 合并后无格式断裂', () => {
  const alice = RichTextDoc.fromText('user1', 'ABC');
  const bob = new RichTextDoc('user2');
  bob.applyAll(alice.getOps());

  alice.addMark('bold', 0, 2); // 用户1：AB 加粗
  bob.addMark('underline', 1, 3); // 用户2 并发：BC 下划线
  sync(alice, bob);

  assert.equal(alice.getText(), 'ABC');
  // A 加粗；B 加粗+下划线；C 仅下划线
  assert.deepEqual(formats(alice), [['bold'], ['bold', 'underline'], ['underline']]);
  // 标注区间保持完整，没有断裂成碎片
  assert.deepEqual(spanTuples(alice), [
    ['bold', 0, 2],
    ['underline', 1, 3],
  ]);
  assert.ok(alice.equals(bob), '两个副本应收敛到相同状态');
});

test('合并满足交换律与幂等性', () => {
  const build = () => {
    const a = RichTextDoc.fromText('user1', 'ABC');
    const b = new RichTextDoc('user2');
    b.applyAll(a.getOps());
    a.addMark('bold', 0, 2);
    b.addMark('underline', 1, 3);
    return [a, b] as const;
  };
  const [a1, b1] = build();
  const [a2, b2] = build();

  b1.applyAll(a1.getOps());
  a1.applyAll(b1.getOps());

  // 反向合并 + 重复应用同样的操作
  a2.applyAll(b2.getOps());
  a2.applyAll(b2.getOps());
  b2.applyAll(a2.getOps());
  b2.applyAll(a2.getOps());

  assert.ok(a1.equals(b1));
  assert.ok(a1.equals(a2));
  assert.ok(a1.equals(b2));
});

test('因果判定：并发"加粗 vs 取消加粗"→ 并发新增标注胜出', () => {
  const alice = RichTextDoc.fromText('user1', 'ABC');
  alice.addMark('bold', 0, 3); // 初始：ABC 全部加粗
  const bob = new RichTextDoc('user2');
  bob.applyAll(alice.getOps());

  alice.removeMark('bold', 0, 2); // 用户1：取消 AB 的加粗
  bob.addMark('bold', 1, 3); // 用户2 并发：给 BC 再加粗（新标注）

  sync(alice, bob);
  // 用户1 的取消只作用于它因果上见过的旧标注；用户2 的新标注不受影响
  assert.deepEqual(formats(alice), [[], ['bold'], ['bold']]);
  assert.ok(alice.equals(bob));
});

test('并发重叠取消：剩余部分取交集语义', () => {
  const alice = RichTextDoc.fromText('user1', 'ABC');
  alice.addMark('bold', 0, 3);
  const bob = new RichTextDoc('user2');
  bob.applyAll(alice.getOps());

  alice.removeMark('bold', 0, 2); // 取消 AB
  bob.removeMark('bold', 1, 3); // 并发取消 BC

  sync(alice, bob);
  // AB 与 BC 的并集被取消，只有两侧剩余片段保留
  assert.deepEqual(formats(alice), [['bold'], [], ['bold']]);
  assert.ok(alice.equals(bob));
});

test('边界语义：区间末尾插入继承格式，开头插入不继承', () => {
  const doc = RichTextDoc.fromText('user1', 'ABC');
  doc.addMark('bold', 0, 2); // AB 加粗

  doc.insertChar(2, 'x'); // 在粗体区间末尾插入 → 继承粗体
  assert.equal(doc.getText(), 'ABxC');
  assert.deepEqual(formats(doc), [['bold'], ['bold'], ['bold'], []]);

  doc.insertChar(0, 'y'); // 在区间开头插入 → 不继承
  assert.equal(doc.getText(), 'yABxC');
  assert.deepEqual(formats(doc), [[], ['bold'], ['bold'], ['bold'], []]);
});

test('取消中间区间：标注分裂为两段', () => {
  const doc = RichTextDoc.fromText('user1', 'ABCDE');
  doc.addMark('bold', 0, 5);
  doc.removeMark('bold', 1, 3); // 取消 BC

  assert.deepEqual(spanTuples(doc), [
    ['bold', 0, 1],
    ['bold', 3, 5],
  ]);
  assert.deepEqual(formats(doc), [['bold'], [], [], ['bold'], ['bold']]);
});

test('并发文本插入收敛到相同顺序', () => {
  const alice = RichTextDoc.fromText('user1', 'AC');
  const bob = new RichTextDoc('user2');
  bob.applyAll(alice.getOps());

  alice.insertChar(1, 'B');
  bob.insertChar(1, 'D'); // 并发在同一位置插入
  sync(alice, bob);

  assert.equal(alice.getText(), bob.getText());
  assert.equal(alice.getText().length, 4);
  assert.ok(alice.equals(bob));
});

test('多种格式（斜体/超链接）可叠加共存', () => {
  const doc = RichTextDoc.fromText('user1', 'hello');
  doc.addMark('italic', 0, 5);
  doc.addMark('link', 0, 2);

  assert.deepEqual(formats(doc), [
    ['italic', 'link'],
    ['italic', 'link'],
    ['italic'],
    ['italic'],
    ['italic'],
  ]);
});

test('三副本乱序合并同样收敛', () => {
  const a = RichTextDoc.fromText('user1', 'ABC');
  const b = new RichTextDoc('user2');
  const c = new RichTextDoc('user3');
  b.applyAll(a.getOps());
  c.applyAll(a.getOps());

  a.addMark('bold', 0, 2);
  b.addMark('underline', 1, 3);
  c.removeMark('bold', 0, 1); // c 尚未见过任何 bold，操作应为 no-op

  // 两两交换，顺序不同
  b.applyAll(a.getOps());
  c.applyAll(b.getOps());
  a.applyAll(c.getOps());
  a.applyAll(b.getOps());
  b.applyAll(c.getOps());
  c.applyAll(a.getOps());

  assert.ok(a.equals(b));
  assert.ok(b.equals(c));
  assert.deepEqual(formats(a), [['bold'], ['bold', 'underline'], ['underline']]);
});
