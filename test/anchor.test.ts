import { apply, fromEdits } from '../src/patch';
import {
  createAnchor,
  findContextPositions,
  migrateAnchor,
  migrateAnchors,
} from '../src/anchor';
import { assert, assertEqual, test } from './framework';

test('createAnchor: 捕获左右上下文，文档边界处截断', () => {
  const doc = 'hello world';
  const a = createAnchor(doc, 6, { contextLength: 5 });
  assertEqual(a.left, 'ello ');
  assertEqual(a.right, 'world');
  const atStart = createAnchor(doc, 0, { contextLength: 5 });
  assertEqual(atStart.left, '');
  assertEqual(atStart.right, 'hello');
});

test('findContextPositions: 重叠出现也能全部找到', () => {
  assertEqual(findContextPositions('aaaa', 'a', 'a'), [1, 2, 3]);
  assertEqual(findContextPositions('abc', '', ''), [0, 1, 2, 3]);
  assertEqual(findContextPositions('abc', 'x', 'y'), []);
});

test('迁移: 无关编辑导致平移，上下文随之刷新', () => {
  const doc = 'hello world';
  const a = createAnchor(doc, 6, { contextLength: 5 });
  const p = fromEdits(doc.length, [{ start: 0, end: 0, text: 'XYZ' }]);
  const doc1 = apply(doc, p);
  const m = migrateAnchor(a, p, doc1);
  assertEqual(m.result, { kind: 'unique', pos: 9 });
  assertEqual(m.anchor.left, 'ello ');
  assertEqual(m.anchor.right, 'world');
});

test('迁移: 锚点处恰好插入，bias 决定去向', () => {
  const doc = 'abcdef';
  const p = fromEdits(6, [{ start: 3, end: 3, text: 'XY' }]);
  const doc1 = apply(doc, p);
  const left = migrateAnchor(createAnchor(doc, 3, { bias: 'left' }), p, doc1);
  const right = migrateAnchor(createAnchor(doc, 3, { bias: 'right' }), p, doc1);
  assertEqual(left.result, { kind: 'unique', pos: 3 });
  assertEqual(right.result, { kind: 'unique', pos: 5 });
});

test('迁移: 删除覆盖锚点 → 失效并保留墓碑，墓碑在后续补丁中不丢失', () => {
  const doc = 'alpha beta gamma';
  const a = createAnchor(doc, 8, { contextLength: 4 }); // 位于 'beta' 内
  const p1 = fromEdits(doc.length, [{ start: 6, end: 11, text: '' }]);
  const doc1 = apply(doc, p1);
  const m1 = migrateAnchor(a, p1, doc1);
  assertEqual(m1.result.kind, 'invalid');
  assert(m1.anchor.tombstone !== null, 'tombstone must exist');
  assertEqual(m1.anchor.tombstone!.cause, 'deleted');
  assertEqual(m1.anchor.tombstone!.left, 'a be');
  // 后续无关补丁：墓碑保留，仍然失效
  const p2 = fromEdits(doc1.length, [{ start: 0, end: 0, text: '>>' }]);
  const doc2 = apply(doc1, p2);
  const m2 = migrateAnchor(m1.anchor, p2, doc2);
  assertEqual(m2.result.kind, 'invalid');
  assert(m2.anchor.tombstone !== null, 'tombstone must survive');
  assertEqual(m2.anchor.tombstone!.left, 'a be');
});

test('迁移: 删除覆盖但上下文在新文档唯一 → 重定位', () => {
  const doc = 'foo TARGET bar baz';
  const a = createAnchor(doc, 7, { contextLength: 3 }); // 'TARGET' 内
  // 用另一段包含相同上下文的文本替换整行
  const p = fromEdits(doc.length, [
    { start: 0, end: doc.length, text: 'xx foo TARGET bar yy' },
  ]);
  const doc1 = apply(doc, p);
  const m = migrateAnchor(a, p, doc1);
  assertEqual(m.result, { kind: 'unique', pos: 10 });
  assertEqual(m.anchor.tombstone, null);
});

test('迁移: 上下文多处匹配 → 输出全部候选，不猜选', () => {
  const doc = 'cat cat cat cat';
  const a = createAnchor(doc, 5, { contextLength: 2 }); // 第二个 'cat' 内
  const p = fromEdits(doc.length, [{ start: 4, end: 7, text: '' }]); // 删掉第二个 'cat'
  const doc1 = apply(doc, p);
  const m = migrateAnchor(a, p, doc1);
  assertEqual(m.result.kind, 'candidates');
  if (m.result.kind === 'candidates') {
    assertEqual(m.result.positions, [6, 10]);
  }
  assert(m.anchor.tombstone !== null, 'ambiguous anchor keeps tombstone');
  assertEqual(m.anchor.tombstone!.cause, 'ambiguous');
});

test('墓碑复活: 上下文重新唯一出现时恢复位置', () => {
  const doc = 'one [x] two';
  const a = createAnchor(doc, 5, { contextLength: 4 }); // '[x]' 内
  const p1 = fromEdits(doc.length, [{ start: 4, end: 7, text: '' }]);
  const doc1 = apply(doc, p1);
  const m1 = migrateAnchor(a, p1, doc1);
  assertEqual(m1.result.kind, 'invalid');
  const p2 = fromEdits(doc1.length, [{ start: 4, end: 4, text: '[x]' }]);
  const doc2 = apply(doc1, p2);
  const m2 = migrateAnchor(m1.anchor, p2, doc2);
  assertEqual(m2.result, { kind: 'unique', pos: 5 });
  assertEqual(m2.anchor.tombstone, null);
});

test('migrateAnchors: 批量迁移，失效锚点保留在数组中', () => {
  const doc = 'aa bb cc';
  const anchors = [
    createAnchor(doc, 1, { id: 'keep', contextLength: 2 }),
    createAnchor(doc, 4, { id: 'drop', contextLength: 2 }),
  ];
  const p = fromEdits(doc.length, [{ start: 3, end: 6, text: '' }]); // 删除 ' bb '
  const doc1 = apply(doc, p);
  const { anchors: next, results } = migrateAnchors(anchors, p, doc1);
  assertEqual(next.length, 2);
  assertEqual(results[0], { kind: 'unique', pos: 1 });
  assertEqual(results[1].kind, 'invalid');
  assertEqual(next[1].id, 'drop');
  assert(next[1].tombstone !== null, 'dropped anchor kept as tombstone');
});
