import test from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeReferences,
  parseFragment,
  parseStartTag,
  type ElementNode,
  type TextNode,
} from './index.ts';

test('文本节点里带分号的 &lt; 替换为小于号', () => {
  const nodes = parseFragment('<p>a &lt; b &#60; c</p>');
  assert.equal(nodes.length, 1);
  const p = nodes[0] as ElementNode;
  assert.equal(p.type, 'element');
  const text = p.children[0] as TextNode;
  assert.equal(text.type, 'text');
  assert.equal(text.text, 'a < b < c');
});

test('属性中带分号的引用完整替换', () => {
  const tag = parseStartTag('<a title="x &lt; y &#60; z" data-n="1">');
  assert.ok(tag);
  assert.deepEqual(tag.attributes, [
    { name: 'title', value: 'x < y < z' },
    { name: 'data-n', value: '1' },
  ]);
});

test('属性中缺分号的 &lt 按字面保留且不提前结束属性', () => {
  const tag = parseStartTag('<a title="x &lt y" data-n="1">');
  assert.ok(tag);
  assert.deepEqual(tag.attributes, [
    { name: 'title', value: 'x &lt y' },
    { name: 'data-n', value: '1' },
  ]);
});

test('缺分号的 &quot 不换掉引号边界', () => {
  const tag = parseStartTag('<a t="x &quot y" z="1">');
  assert.ok(tag);
  assert.deepEqual(tag.attributes, [
    { name: 't', value: 'x &quot y' },
    { name: 'z', value: '1' },
  ]);
});

test('带分号与不带分号的属性边界一致', () => {
  const withSemi = parseStartTag('<a t="1&lt;2" u="v">');
  const withoutSemi = parseStartTag('<a t="1&lt2" u="v">');
  assert.ok(withSemi);
  assert.ok(withoutSemi);
  assert.equal(withSemi.attributes.length, 2);
  assert.equal(withoutSemi.attributes.length, 2);
  assert.equal(withSemi.attributes[0].name, withoutSemi.attributes[0].name);
  assert.deepEqual(withSemi.attributes[1], withoutSemi.attributes[1]);
  assert.equal(withSemi.attributes[0].value, '1<2');
  assert.equal(withoutSemi.attributes[0].value, '1&lt2');
});

test('无引号属性值同样只在分号处替换', () => {
  assert.equal(parseStartTag('<a t=1&lt;2>')?.attributes[0].value, '1<2');
  assert.equal(parseStartTag('<a t=1&lt2>')?.attributes[0].value, '1&lt2');
});

test('decodeReferences 缺分号一律字面保留', () => {
  assert.equal(decodeReferences('&lt; &lt &amp; &amp &#65; &#x41;'), '< &lt & &amp A A');
});
