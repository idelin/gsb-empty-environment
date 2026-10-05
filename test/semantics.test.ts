/**
 * Hand-written semantics checks. Every expectation below is derived from the
 * documented rules (leftmost start, longest overall match, then longest
 * capture in group order), not from any existing regex engine.
 */

import { compile } from '../src/index';
import { test, eq, ok } from './harness';

type G = [number, number] | 'u'; // 'u' = did not participate

function m(pattern: string, input: string): { span: [number, number]; groups: G[] } | null {
  const r = compile(pattern).exec(input);
  if (r === null) return null;
  return {
    span: [r.span.start, r.span.end],
    groups: r.groups.map((g) => (g === undefined ? 'u' : [g.start, g.end] as [number, number])),
  };
}

test('literal match and leftmost start', () => {
  eq(m('abc', 'xxabcy'), { span: [2, 5], groups: [[2, 5]] });
  eq(m('b', 'aab'), { span: [2, 3], groups: [[2, 3]] });
  eq(m('z', 'aaa'), null);
});

test('empty pattern matches empty at position 0', () => {
  eq(m('', 'abc'), { span: [0, 0], groups: [[0, 0]] });
  eq(m('', ''), { span: [0, 0], groups: [[0, 0]] });
});

test('longest overall match beats alternation order', () => {
  // A backtracking engine would return "a"; leftmost-longest requires "ab".
  eq(m('a|ab', 'ab'), { span: [0, 2], groups: [[0, 2]] });
  eq(m('ab|a', 'ab'), { span: [0, 2], groups: [[0, 2]] });
});

test('star is greedy in length, not in iteration count', () => {
  eq(m('a*', 'bbb'), { span: [0, 0], groups: [[0, 0]] });
  eq(m('a*', 'aaab'), { span: [0, 3], groups: [[0, 3]] });
  eq(m('a**', 'aaa'), { span: [0, 3], groups: [[0, 3]] });
});

test('classic POSIX submatch case: (a|ab)(c|bcd) on abcd', () => {
  // Overall longest is [0,4); then group 1 as long as possible => "a",
  // leaving "bcd" for group 2.
  eq(m('(a|ab)(c|bcd)', 'abcd'), { span: [0, 4], groups: [[0, 4], [0, 1], [1, 4]] });
});

test('group priority: earlier groups get length first', () => {
  eq(m('(a*)(a*)', 'aa'), { span: [0, 2], groups: [[0, 2], [0, 2], [2, 2]] });
  eq(m('(a*)(a*)(a*)', 'aa'), { span: [0, 2], groups: [[0, 2], [0, 2], [2, 2], [2, 2]] });
  eq(m('(|a)(a|)', 'a'), { span: [0, 1], groups: [[0, 1], [0, 1], [1, 1]] });
});

test('non-participating group is distinct from empty group', () => {
  // group 1 does not participate at all:
  eq(m('(a)|(b)', 'b'), { span: [0, 1], groups: [[0, 1], 'u', [0, 1]] });
  // group 1 participates but captures the empty string:
  eq(m('(a*)b', 'b'), { span: [0, 1], groups: [[0, 1], [0, 0]] });
  // participating-empty beats not-participating for the same overall span:
  eq(m('(a*)|()', ''), { span: [0, 0], groups: [[0, 0], [0, 0], 'u'] });
});

test('star records the last iteration of a group', () => {
  eq(m('(a)*', 'aa'), { span: [0, 2], groups: [[0, 2], [1, 2]] });
  eq(m('(a|b)*c', 'abac'), { span: [0, 4], groups: [[0, 4], [2, 3]] });
  eq(m('((a)|(b))*', 'ab'), { span: [0, 2], groups: [[0, 2], [1, 2], [0, 1], [1, 2]] });
});

test('zero-length loops terminate and still capture', () => {
  eq(m('()*', ''), { span: [0, 0], groups: [[0, 0], [0, 0]] });
  eq(m('(()*)*', ''), { span: [0, 0], groups: [[0, 0], [0, 0], [0, 0]] });
  eq(m('(a*)*', ''), { span: [0, 0], groups: [[0, 0], [0, 0]] });
  eq(m('(a*)*', 'aa'), { span: [0, 2], groups: [[0, 2], [0, 2]] });
  eq(m('(a*)*', 'b'), { span: [0, 0], groups: [[0, 0], [0, 0]] });
});

test('nested empty branches', () => {
  eq(m('(|a)', 'a'), { span: [0, 1], groups: [[0, 1], [0, 1]] });
  eq(m('(|a)', 'b'), { span: [0, 0], groups: [[0, 0], [0, 0]] });
  eq(m('(a|)*', 'a'), { span: [0, 1], groups: [[0, 1], [0, 1]] });
  eq(m('(a|)*', ''), { span: [0, 0], groups: [[0, 0], [0, 0]] });
  eq(m('((|a)*)*', 'a'), { span: [0, 1], groups: [[0, 1], [0, 1], [0, 1]] });
  eq(m('((a*)*)*', 'aa'), { span: [0, 2], groups: [[0, 2], [0, 2], [0, 2]] });
  eq(m('(|a|)*', 'a'), { span: [0, 1], groups: [[0, 1], [0, 1]] });
});

test('concatenation backtracks across alternatives', () => {
  eq(m('(a*)(b*)', 'ab'), { span: [0, 2], groups: [[0, 2], [0, 1], [1, 2]] });
  eq(m('b*(a*)b', 'bbab'), { span: [0, 4], groups: [[0, 4], [2, 3]] });
  eq(m('(a|ab)*', 'ab'), { span: [0, 2], groups: [[0, 2], [0, 2]] });
  eq(m('(ab|a)*', 'aa'), { span: [0, 2], groups: [[0, 2], [1, 2]] });
});

test('escapes and metacharacters as literals', () => {
  eq(m('a\\*b', 'a*b'), { span: [0, 3], groups: [[0, 3]] });
  eq(m('\\(\\)', '()'), { span: [0, 2], groups: [[0, 2]] });
  eq(m('\\\\', '\\'), { span: [0, 1], groups: [[0, 1]] });
});

test('parse errors are reported', () => {
  const bad = ['(', ')', 'a)', '*a', 'a\\', '(a'];
  for (const p of bad) {
    let threw = false;
    try {
      compile(p);
    } catch {
      threw = true;
    }
    ok(threw, `expected parse error for ${JSON.stringify(p)}`);
  }
});

test('exec result exposes text and groupCount', () => {
  const re = compile('(a*)(b*)');
  eq(re.groupCount, 2);
  const r = re.exec('aab');
  ok(r !== null, 'should match');
  eq(r!.text, 'aab');
  eq(re.test('b'), true);
  eq(compile('z').test('abc'), false);
});
