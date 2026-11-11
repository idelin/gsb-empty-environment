import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildNfa, nfaAccepts, parsePattern, epsilonClosure } from '../index.ts';

test('字面量 NFA 的整串接受与拒绝', () => {
  const nfa = buildNfa(parsePattern('abc').ast);
  assert.equal(nfaAccepts(nfa, 'abc'), true);
  assert.equal(nfaAccepts(nfa, 'ab'), false);
  assert.equal(nfaAccepts(nfa, 'abcd'), false);
  assert.equal(nfaAccepts(nfa, 'xabc'), false);
});

test('星号 NFA 接受零次与多次重复', () => {
  const nfa = buildNfa(parsePattern('a*').ast);
  assert.equal(nfaAccepts(nfa, ''), true);
  assert.equal(nfaAccepts(nfa, 'a'), true);
  assert.equal(nfaAccepts(nfa, 'aaaa'), true);
  assert.equal(nfaAccepts(nfa, 'b'), false);
});

test('并集 NFA 接受任一分支', () => {
  const nfa = buildNfa(parsePattern('ab|cd').ast);
  assert.equal(nfaAccepts(nfa, 'ab'), true);
  assert.equal(nfaAccepts(nfa, 'cd'), true);
  assert.equal(nfaAccepts(nfa, 'ef'), false);
});

test('分组与连接的组合语义', () => {
  const nfa = buildNfa(parsePattern('(ab|a)c').ast);
  assert.equal(nfaAccepts(nfa, 'abc'), true);
  assert.equal(nfaAccepts(nfa, 'ac'), true);
  assert.equal(nfaAccepts(nfa, 'c'), false);
});

test('ε-闭包至少包含种子状态', () => {
  const nfa = buildNfa(parsePattern('a').ast);
  const closure = epsilonClosure(nfa, [nfa.start]);
  assert.ok(closure.has(nfa.start));
});

test('字母表只包含模式中出现的字符', () => {
  const nfa = buildNfa(parsePattern('(ab|a)*').ast);
  assert.deepEqual([...nfa.alphabet].sort(), ['a', 'b']);
});
