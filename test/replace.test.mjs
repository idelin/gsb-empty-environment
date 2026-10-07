import { test, assert } from './helpers.mjs';
import { replaceAll, findAllMatches } from '../dist/index.js';

// 与原生 String.prototype.replace 对照的语义 oracle（模板只用 $ 语法，不用反斜杠转义）。
test('oracle: matches native global replace', () => {
  const cases = [
    ['aaa', /x*/g, '-'],
    ['aab', /a*/g, '-'],
    ['ab', /(?:)/g, '-'],
    ['abc', /b/g, "$`|$&|$'"],
    ['a ab', /(a)(b)?/g, '[$2$1]'],
    ['aa', /a/g, 'aa'],
    ['abc', /(?=b)/g, '-'],
    ['hello world', /(\w+) (\w+)/, '$2 $1'],
    ['abc', /x/, 'y'],
    ['foo bar foo', /foo/g, '$$'],
    ['aXbXc', /X/g, "$'"],
    ['aXbXc', /X/g, '$`'],
    ['abc', /(?<mid>b)/, '<$<mid>>'],
    ['abc', /b/, '$10$1'],
  ];
  for (const [text, re, tpl] of cases) {
    const g = new RegExp(re.source, re.flags.includes('g') ? re.flags : re.flags + 'g');
    assert.equal(
      replaceAll(text, re, tpl).newText,
      text.replace(g, tpl),
      `case ${JSON.stringify([text, re.source, tpl])}`,
    );
  }
});

test('non-overlapping matches only', () => {
  const r = replaceAll('aaaa', /aa/g, 'b');
  assert.equal(r.newText, 'bb');
  assert.equal(r.matches.length, 2);
});

test('replacement text is not re-scanned in the same round', () => {
  const r = replaceAll('a', /a/g, 'aa');
  assert.equal(r.newText, 'aa');
  assert.equal(r.matches.length, 1);
  const r2 = replaceAll('x', /x/g, 'xx');
  assert.equal(r2.newText, 'xx');
  assert.equal(r2.matches.length, 1);
});

test('zero-width match fires at most once per boundary', () => {
  const r = replaceAll('ab', /(?:)/g, '-');
  assert.equal(r.newText, '-a-b-');
  assert.deepEqual(
    r.matches.map((m) => m.start),
    [0, 1, 2],
  );
  // 零宽命中后扫描推进：/a*/ 在 "aab" 上命中 [0,2)、[2,2)、[3,3)
  const r2 = replaceAll('aab', /a*/g, '-');
  assert.equal(r2.newText, '--b-');
  assert.deepEqual(
    r2.matches.map((m) => [m.start, m.end]),
    [
      [0, 2],
      [2, 2],
      [3, 3],
    ],
  );
});

test('adjacent matches', () => {
  const r = replaceAll('aa', /a/g, 'bb');
  assert.equal(r.newText, 'bbbb');
  assert.equal(r.edits.length, 2);
  assert.equal(r.edits[0].oldEnd, r.edits[1].oldStart);
  assert.equal(r.edits[0].newEnd, r.edits[1].newStart);
});

test('non-participating capture expands to empty string', () => {
  const r = replaceAll('b', /(a)|(b)/, '$1-$2');
  assert.equal(r.newText, '-b');
});

test('named capture groups', () => {
  const r = replaceAll('abc', /(?<mid>b)/, '<$<mid>>');
  assert.equal(r.newText, 'a<b>c');
  // 未参与的命名组 -> 空串
  const r2 = replaceAll('y', /(?<p>x)|(?<q>y)/, '$<p>$<q>');
  assert.equal(r2.newText, 'y');
});

test('before/after references read the frozen old text', () => {
  const r = replaceAll('abc', /b/g, "[$`][$']");
  assert.equal(r.newText, 'a[a][c]c');
  // 多个匹配同时应用：第二个匹配的 $` 仍是旧文本前缀，不含第一个替换
  const r2 = replaceAll('aXbXc', /X/g, '$`');
  assert.equal(r2.newText, 'aabaXbc');
});

test('backslash escape syntax', () => {
  assert.equal(replaceAll('a', /a/, 'x\\ny').newText, 'x\ny');
  assert.equal(replaceAll('a', /a/, 'x\\ty').newText, 'x\ty');
  assert.equal(replaceAll('a', /a/, 'x\\ry').newText, 'x\ry');
  assert.equal(replaceAll('a', /a/, 'a\\\\b').newText, 'a\\b');
  assert.equal(replaceAll('a', /a/, '\\$&').newText, '$&');
  assert.equal(replaceAll('a', /a/, '$$&').newText, '$&');
  // 未知转义保留反斜杠
  assert.equal(replaceAll('a', /a/, '\\q').newText, '\\q');
});

test('dollar edge cases', () => {
  // 组号不存在 -> 字面保留
  assert.equal(replaceAll('a', /(a)/, '$2').newText, '$2');
  // 两位组号优先，超出则退化为一位 + 字面数字
  assert.equal(replaceAll('a', /(a)/, '$10').newText, 'a0');
  assert.equal(replaceAll('a', /a/, '$0').newText, '$0');
});

test('string pattern is literal and global', () => {
  assert.equal(replaceAll('a.b.c', '.', '-').newText, 'a-b-c');
  assert.equal(replaceAll('abc', '', '-').newText, '-a-b-c-');
  assert.equal(replaceAll('aaaa', 'aa', 'b').newText, 'bb');
});

test('function replacement receives match, groups, offset, text', () => {
  const seen = [];
  const r = replaceAll('a1 b2', /([a-z])(\d)/g, (m, groups, offset, text) => {
    seen.push([m, groups.slice(), offset, text]);
    return groups[1] + groups[0].toUpperCase();
  });
  assert.equal(r.newText, '1A 2B');
  assert.deepEqual(seen[0], ['a1', ['a', '1'], 0, 'a1 b2']);
  assert.deepEqual(seen[1], ['b2', ['b', '2'], 3, 'a1 b2']);
});

test('empty replacement deletes the match', () => {
  const r = replaceAll('abc', /b/, '');
  assert.equal(r.newText, 'ac');
  assert.deepEqual([r.edits[0].newStart, r.edits[0].newEnd], [1, 1]);
});

test('findAllMatches freezes matches on the old text', () => {
  const matches = findAllMatches('one two three', /\w+/g);
  assert.deepEqual(
    matches.map((m) => [m.start, m.end, m.text]),
    [
      [0, 3, 'one'],
      [4, 7, 'two'],
      [8, 13, 'three'],
    ],
  );
});
