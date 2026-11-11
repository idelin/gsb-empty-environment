import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AmbiguityDetector,
  buildFailingSuffix,
  chooseDeadChar,
  detectAmbiguity,
  nfaAccepts,
  buildNfa,
  parsePattern,
  epsilonClosure,
} from '../index.ts';

test('唯一解析模式不报告歧义环（避免假阳性）', () => {
  for (const source of [
    'a*$',
    '(a|b)*$',
    '^a*b$',
    '(foo|foot)*$',
    '(abc|ab|a)*$',
    '(ab|a)(b|ab)',
  ]) {
    const detector = new AmbiguityDetector(parsePattern(source));
    assert.equal(detector.findAmbiguityCycle(), null, `${source} 不应有歧义环`);
  }
});

test('星号内重叠分支被识别为可泵送歧义', () => {
  for (const source of ['(a|aa)*$', '^((ab)*)*$', 'a(b|b)*c']) {
    const detector = new AmbiguityDetector(parsePattern(source));
    const cycle = detector.findAmbiguityCycle();
    assert.ok(cycle, `${source} 应当找到歧义环`);
    assert.ok(cycle!.pump.length > 0, '重复段必须消费至少一个字符');
    // 前缀+泵必须是 NFA 上的真实可达路径（之后还会拼接失败后缀，不要求整串 accept）。
    const nfa = detector.getNfa();
    for (const witness of [
      cycle!.prefix + cycle!.pump,
      cycle!.prefix + cycle!.pump + cycle!.pump,
    ]) {
      let current = epsilonClosure(nfa, [nfa.start]);
      for (const char of witness) {
        const next = new Set<number>();
        for (const state of current) {
          for (const target of nfa.states[state].transitions.get(char) ?? []) {
            next.add(target);
          }
        }
        current = epsilonClosure(nfa, [...next]);
      }
      assert.ok(current.size > 0, `${source} 的见证 ${witness} 应当可达`);
    }
  }
});

test('失败后缀以死字符结尾且死字符不在字母表中', () => {
  const parsed = parsePattern('(a|aa)*$');
  const detector = new AmbiguityDetector(parsed);
  const nfa = detector.getNfa();
  const { suffix, deadChar } = buildFailingSuffix(nfa, 'a');
  assert.equal(nfa.alphabet.has(deadChar), false);
  assert.ok(suffix.endsWith(deadChar));
  assert.equal(nfaAccepts(nfa, suffix), false);
});

test('chooseDeadChar 在字母表占满常见符号时回退到私有区字符', () => {
  const alphabet = new Set(['~', '`', '@', '#', '%', '&', '_', '=', '+', ':', ';', '<', '>', '\u0000', '\uE000']);
  const dead = chooseDeadChar(alphabet);
  assert.equal(alphabet.has(dead), false);
});

test('detectAmbiguity 便捷封装返回环与死字符', () => {
  const result = detectAmbiguity(parsePattern('(a|aa)*$'));
  assert.ok(result !== null);
  assert.ok(result!.cycle.pump.length > 0);
  assert.equal(typeof result!.deadChar, 'string');
});

test('预算耗尽时返回 null 而非抛出（未证实语义）', () => {
  const detector = new AmbiguityDetector(parsePattern('(a|aa)*$'), 1);
  assert.equal(detector.findAmbiguityCycle(), null);
});

test('嵌套星号的 NFA 仍可正常构造与接受', () => {
  const nfa = buildNfa(parsePattern('((a*)*)*').ast);
  assert.equal(nfaAccepts(nfa, 'aaa'), true);
});
