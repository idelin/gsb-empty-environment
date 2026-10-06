import { normalize, flatten, render, equivalent } from '../dist/index.js';
import {
  test,
  assert,
  assertDeepEqual,
  deepEqual,
  mulberry32,
  flatOf,
  randomDoc,
  anchorsOf,
} from './helpers.mjs';

test('合并相邻且格式集合相同（顺序无关）的节点', () => {
  const doc = {
    nodes: [
      { id: '1', text: 'foo', marks: [{ type: 'bold' }, { type: 'italic' }] },
      { id: '2', text: 'bar', marks: [{ type: 'italic' }, { type: 'bold' }] },
      { id: '3', text: 'baz', marks: [{ type: 'bold' }] },
    ],
  };
  const { doc: out, map } = normalize(doc);
  assert(out.nodes.length === 2, 'merged into 2 nodes');
  assert(out.nodes[0].text === 'foobar', 'texts concatenated');
  assert(out.nodes[0].id === '1', 'merged node keeps leftmost id');
  assert(out.nodes[1].text === 'baz', 'different marks not merged');
  assertDeepEqual(map[0], { start: 0, end: 1 }, 'map[0]');
  assertDeepEqual(map[1], { start: 0, end: 1 }, 'map[1] merged to same node');
  assertDeepEqual(map[2], { start: 1, end: 2 }, 'map[2]');
});

test('移除无锚点空节点，保留带锚点空节点', () => {
  const doc = {
    nodes: [
      { id: '1', text: '', marks: [] },
      { id: '2', text: '', marks: [], anchor: { id: 'A', affinity: 'left' } },
      { id: '3', text: 'x', marks: [] },
    ],
  };
  const { doc: out, map } = normalize(doc);
  assert(out.nodes.length === 2, 'empty unanchored removed, anchored kept');
  assert(out.nodes[0].id === '2' && out.nodes[0].anchor.id === 'A', 'anchor node preserved');
  assert(map[0] === null, 'removed node maps to null');
  assertDeepEqual(map[1], { start: 0, end: 1 }, 'anchored empty mapped');
  assertDeepEqual(map[2], { start: 1, end: 2 }, 'text node mapped');
});

test('带锚点的节点是合并屏障', () => {
  const doc = {
    nodes: [
      { id: '1', text: 'a', marks: [{ type: 'bold' }] },
      { id: '2', text: 'b', marks: [{ type: 'bold' }], anchor: { id: 'A', affinity: 'right' } },
      { id: '3', text: 'c', marks: [{ type: 'bold' }] },
    ],
  };
  const { doc: out } = normalize(doc);
  assert(out.nodes.length === 3, 'anchored node blocks merging on both sides');
  assert(render(out) === 'abc', 'text unchanged');
});

test('链接不可嵌套：同节点多个 link 保留首个', () => {
  const doc = {
    nodes: [
      {
        id: '1',
        text: 'x',
        marks: [
          { type: 'link', attrs: { href: 'https://first' } },
          { type: 'link', attrs: { href: 'https://second' } },
          { type: 'bold' },
          { type: 'bold' },
        ],
      },
    ],
  };
  const { doc: out } = normalize(doc);
  const links = out.nodes[0].marks.filter((m) => m.type === 'link');
  assert(links.length === 1, 'only one link kept');
  assert(links[0].attrs.href === 'https://first', 'first link kept');
  assert(out.nodes[0].marks.length === 2, 'duplicate bold deduped');
});

test('规范化保持渲染等价（字符 + 格式序列）', () => {
  const rng = mulberry32(42);
  for (let i = 0; i < 100; i++) {
    const doc = randomDoc(rng, 'eq' + i);
    const { doc: out } = normalize(doc);
    assertDeepEqual(flatOf(out), flatOf(doc), 'flat model equal after normalize #' + i);
    assert(equivalent(out, doc), 'equivalent() agrees #' + i);
  }
});

test('规范化幂等：反复执行不变', () => {
  const rng = mulberry32(7);
  for (let i = 0; i < 100; i++) {
    const doc = randomDoc(rng, 'idem' + i);
    const once = normalize(doc);
    const twice = normalize(once.doc);
    assert(deepEqual(twice.doc, once.doc), 'idempotent #' + i);
    for (let j = 0; j < twice.map.length; j++) {
      assertDeepEqual(twice.map[j], { start: j, end: j + 1 }, 'identity map #' + i);
    }
  }
});

test('映射：分组文本拼接一致，锚点与节点身份可追踪', () => {
  const rng = mulberry32(99);
  for (let i = 0; i < 100; i++) {
    const doc = randomDoc(rng, 'map' + i);
    const { doc: out, map } = normalize(doc);
    assert(map.length === doc.nodes.length, 'map length matches');
    const groups = new Map();
    for (let j = 0; j < doc.nodes.length; j++) {
      const range = map[j];
      const original = doc.nodes[j];
      if (range === null) {
        assert(original.text === '' && !original.anchor, 'null only for empty unanchored');
        continue;
      }
      assert(range.end === range.start + 1, 'unit range');
      const target = out.nodes[range.start];
      assert(target !== undefined, 'target exists');
      if (original.anchor) {
        assert(deepEqual(target.anchor, original.anchor), 'anchor preserved on target');
        assert(target.id === original.id, 'anchored node keeps id');
      }
      if (!groups.has(range.start)) groups.set(range.start, '');
      groups.set(range.start, groups.get(range.start) + original.text);
    }
    for (const [index, text] of groups) {
      assert(out.nodes[index].text === text, 'grouped text equals merged text');
    }
    assertDeepEqual(anchorsOf(out), anchorsOf(doc), 'anchor multiset preserved');
  }
});

test('flatten 与独立扁平模型一致', () => {
  const rng = mulberry32(1234);
  for (let i = 0; i < 50; i++) {
    const doc = randomDoc(rng, 'flat' + i);
    assertDeepEqual(flatten(doc), flatOf(doc), 'flatten matches reference #' + i);
  }
});
