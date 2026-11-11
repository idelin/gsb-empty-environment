import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parsePattern, PatternSyntaxError } from '../index.ts';

test('解析字面量、连接与锚点标记', () => {
  const parsed = parsePattern('^abc$');
  assert.equal(parsed.anchoredStart, true);
  assert.equal(parsed.anchoredEnd, true);
  assert.equal(parsed.ast.type, 'concat');
  assert.equal(parsed.body, 'abc');
});

test('解析并集、分组与星号', () => {
  const parsed = parsePattern('(a|ab)*');
  assert.equal(parsed.anchoredStart, false);
  assert.equal(parsed.ast.type, 'star');
  const group = parsed.ast;
  if (group.type !== 'star') throw new Error('expected star');
  assert.equal(group.child.type, 'group');
});

test('无锚点模式两个标记均为 false', () => {
  const parsed = parsePattern('ab');
  assert.equal(parsed.anchoredStart, false);
  assert.equal(parsed.anchoredEnd, false);
});

test('转义元字符按字面量处理', () => {
  const parsed = parsePattern('a\\*b');
  assert.equal(parsed.ast.type, 'concat');
});

test('支持 RegExp 实例输入', () => {
  const parsed = parsePattern(/(a|aa)*$/);
  assert.equal(parsed.source, '(a|aa)*$');
  assert.equal(parsed.anchoredEnd, true);
});

test('拒绝带标志位的 RegExp', () => {
  assert.throws(() => parsePattern(/a*/g), PatternSyntaxError);
});

test('拒绝不支持的字符类、量词与点号', () => {
  for (const bad of ['[abc]', 'a+', 'a?', 'a{2}', 'a.b']) {
    assert.throws(() => parsePattern(bad), PatternSyntaxError, `应当拒绝 ${bad}`);
  }
});

test('拒绝反向引用', () => {
  assert.throws(() => parsePattern('(a)\\1'), PatternSyntaxError);
});

test('拒绝未闭合分组与悬空量词', () => {
  assert.throws(() => parsePattern('(a'), PatternSyntaxError);
  assert.throws(() => parsePattern('*a'), PatternSyntaxError);
  assert.throws(() => parsePattern('a||b'), PatternSyntaxError);
});

test('锚点只允许出现在首尾', () => {
  assert.throws(() => parsePattern('a^b'), PatternSyntaxError);
  assert.throws(() => parsePattern('a$b'), PatternSyntaxError);
});
