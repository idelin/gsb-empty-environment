import {
  normalize,
  flatten,
  render,
  splitAt,
  addMark,
  removeMark,
  toggleMark,
  applyEdits,
} from '../dist/index.js';
import {
  test,
  assert,
  assertDeepEqual,
  deepEqual,
  mulberry32,
  flatOf,
  flatApplyEdits,
  randomDoc,
  randomEdits,
  anchorsOf,
  emptyAnchoredCount,
} from './helpers.mjs';

test('跨节点范围：拆分、应用、再规范合并', () => {
  const doc = {
    nodes: [
      { id: '1', text: 'aaa', marks: [{ type: 'bold' }] },
      { id: '2', text: 'bbb', marks: [] },
    ],
  };
  const { doc: out } = addMark(doc, 2, 4, { type: 'italic' });
  assertDeepEqual(flatOf(out), [
    { char: 'a', marks: ['bold'] },
    { char: 'a', marks: ['bold'] },
    { char: 'a', marks: ['bold', 'italic'] },
    { char: 'b', marks: ['italic'] },
    { char: 'b', marks: [] },
    { char: 'b', marks: [] },
  ], 'split + apply + merge');
});

test('整批编辑基于原坐标，与给定顺序无关', () => {
  const doc = { nodes: [{ id: '1', text: 'abcdef', marks: [] }] };
  const edits = [
    { kind: 'add', start: 0, end: 2, mark: { type: 'bold' } },
    { kind: 'add', start: 4, end: 6, mark: { type: 'italic' } },
  ];
  const forward = applyEdits(doc, edits).doc;
  const reversed = applyEdits(doc, [edits[1], edits[0]]).doc;
  assertDeepEqual(flatOf(forward), flatOf(reversed), 'order independent');
  assertDeepEqual(flatOf(forward), flatApplyEdits(flatOf(doc), edits), 'matches flat model');
});

test('添加链接替换已有链接，不产生嵌套', () => {
  const doc = {
    nodes: [{ id: '1', text: 'ab', marks: [{ type: 'link', attrs: { href: 'https://old' } }] }],
  };
  const { doc: out } = addMark(doc, 0, 2, { type: 'link', attrs: { href: 'https://new' } });
  const links = out.nodes[0].marks.filter((m) => m.type === 'link');
  assert(links.length === 1, 'single link');
  assert(links[0].attrs.href === 'https://new', 'link replaced');
});

test('拆分按锚点亲和性保留锚点', () => {
  const leftDoc = {
    nodes: [{ id: '1', text: 'abcd', marks: [], anchor: { id: 'A', affinity: 'left' } }],
  };
  const leftSplit = splitAt(leftDoc, 2);
  assert(leftSplit.nodes.length === 2, 'split into two');
  assert(leftSplit.nodes[0].anchor && leftSplit.nodes[0].anchor.id === 'A', 'left affinity stays left');
  assert(!leftSplit.nodes[1].anchor, 'right part has no anchor');

  const rightDoc = {
    nodes: [{ id: '1', text: 'abcd', marks: [], anchor: { id: 'A', affinity: 'right' } }],
  };
  const rightSplit = splitAt(rightDoc, 2);
  assert(!rightSplit.nodes[0].anchor, 'left part has no anchor');
  assert(rightSplit.nodes[1].anchor && rightSplit.nodes[1].anchor.id === 'A', 'right affinity moves right');

  assert(splitAt(leftDoc, 0) === leftDoc, 'boundary split is no-op');
  assert(splitAt(leftDoc, 4) === leftDoc, 'end split is no-op');
});

test('toggleMark：全覆盖则移除，否则添加；两次切换渲染等价', () => {
  const doc = { nodes: [{ id: '1', text: 'abcd', marks: [] }] };
  const on = toggleMark(doc, 0, 4, { type: 'bold' }).doc;
  assert(flatOf(on).every((c) => c.marks.includes('bold')), 'toggle on');
  const off = toggleMark(on, 0, 4, { type: 'bold' }).doc;
  assertDeepEqual(flatOf(off), flatOf(doc), 'toggle twice equivalent');

  const mixed = {
    nodes: [
      { id: '1', text: 'ab', marks: [{ type: 'bold' }] },
      { id: '2', text: 'cd', marks: [] },
    ],
  };
  const filled = toggleMark(mixed, 0, 4, { type: 'bold' }).doc;
  assert(flatOf(filled).every((c) => c.marks.includes('bold')), 'partial coverage toggles on');
});

test('空范围操作是无操作；非法范围抛错', () => {
  const doc = { nodes: [{ id: '1', text: 'ab', marks: [] }] };
  assert(addMark(doc, 1, 1, { type: 'bold' }).doc === doc, 'empty add no-op');
  assert(removeMark(doc, 1, 1, 'bold').doc === doc, 'empty remove no-op');
  let threw = false;
  try {
    addMark(doc, 1, 3, { type: 'bold' });
  } catch (err) {
    threw = err instanceof RangeError;
  }
  assert(threw, 'out-of-range throws RangeError');
});

test('撤销恢复原文档对象：节点身份与锚点完全还原', () => {
  const rng = mulberry32(2024);
  for (let i = 0; i < 100; i++) {
    const doc = randomDoc(rng, 'undo' + i);
    const length = flatOf(doc).length;
    const edits = randomEdits(rng, length, 1 + Math.floor(rng() * 4));
    const applied = applyEdits(doc, edits);
    const restored = applied.undo();
    assert(restored === doc, 'undo returns original doc object #' + i);
    for (let j = 0; j < doc.nodes.length; j++) {
      assert(restored.nodes[j] === doc.nodes[j], 'node identity restored #' + i);
    }
    assertDeepEqual(anchorsOf(restored), anchorsOf(doc), 'anchors restored #' + i);
  }
});

test('随机化核对：扁平模型等价、锚点保留、空锚点节点保留、链接不嵌套、幂等', () => {
  const rng = mulberry32(555);
  for (let i = 0; i < 200; i++) {
    const doc = randomDoc(rng, 'prop' + i);
    const flat = flatOf(doc);
    const edits = randomEdits(rng, flat.length, 1 + Math.floor(rng() * 4));
    const { doc: out } = applyEdits(doc, edits);

    assertDeepEqual(flatOf(out), flatApplyEdits(flat, edits), 'flat model equal #' + i);
    assertDeepEqual(anchorsOf(out), anchorsOf(doc), 'anchors preserved #' + i);
    assert(emptyAnchoredCount(out) === emptyAnchoredCount(doc), 'empty anchored kept #' + i);
    if (edits.some((e) => e.start !== e.end)) {
      for (const node of out.nodes) {
        const links = node.marks.filter((m) => m.type === 'link');
        assert(links.length <= 1, 'no nested links #' + i);
      }
      const again = normalize(out);
      assert(deepEqual(again.doc, out), 'result is normalize fixed point #' + i);
    } else {
      assert(out === doc, 'all-empty edits are a no-op #' + i);
    }
  }
});

test('带锚点空节点在范围编辑中保留', () => {
  const doc = {
    nodes: [
      { id: '1', text: 'ab', marks: [] },
      { id: '2', text: '', marks: [], anchor: { id: 'A', affinity: 'left' } },
      { id: '3', text: 'cd', marks: [] },
    ],
  };
  const { doc: out } = addMark(doc, 0, 4, { type: 'bold' });
  const anchored = out.nodes.find((n) => n.anchor && n.anchor.id === 'A');
  assert(anchored !== undefined, 'anchor node still present');
  assert(anchored.text === '', 'still empty');
  assert(render(out) === 'abcd', 'text unchanged');
  assert(flatOf(out).every((c) => c.marks.includes('bold')), 'mark applied around it');
});
