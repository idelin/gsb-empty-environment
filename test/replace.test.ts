import { findMatches, replaceAll } from '../src';
import { assert, describe, it, naiveConcat } from './helpers';

function globalOf(re: RegExp): RegExp {
  return re.flags.includes('g') ? re : new RegExp(re.source, re.flags + 'g');
}

describe('match scanning', () => {
  it('finds leftmost non-overlapping matches only', () => {
    const matches = findMatches('aaaa', /aa/);
    assert.deepStrictEqual(matches.map((m) => m.index), [0, 2]);
  });

  it('zero-width match consumes its boundary at most once', () => {
    const matches = findMatches('aa', /(?=a)/);
    assert.deepStrictEqual(matches.map((m) => m.index), [0, 1]);
    assert.ok(matches.every((m) => m[0] === ''));
  });

  it('empty pattern hits every boundary exactly once', () => {
    const matches = findMatches('ab', /(?:)/);
    assert.deepStrictEqual(matches.map((m) => m.index), [0, 1, 2]);
  });

  it('zero-width and consuming matches interleave like native replace', () => {
    const matches = findMatches('abx', /x*/);
    assert.deepStrictEqual(matches.map((m) => m.index), [0, 1, 2, 3]);
    assert.strictEqual(matches[2][0], 'x');
  });
});

describe('replaceAll semantics', () => {
  it('matches native String.replace for a spread of cases', () => {
    const cases: [string, RegExp, string][] = [
      ['aaaa', /aa/, 'X'],
      ['aa', /(?=a)/, '-'],
      ['ab', /(?:)/, '-'],
      ['abx', /x*/, '-'],
      ['axbxc', /x/, ''],
      ['ababab', /ab/, '-'],
      ['John Smith', /(\w+) (\w+)/, '$2 $1'],
      ['John Smith', /(?<first>\w+) (?<last>\w+)/, '$<last> $<first>'],
      ['abc def', /def/, '[$`|$\']'],
      ['price', /price/, '$$5'],
      ['b', /(a)|(b)/, '$1x'],
      ['a', /(a)/, '$9'],
      ['a', /(a)/, '$&$&'],
      ['hello world', /o/, '0'],
    ];
    for (const [text, re, rep] of cases) {
      const expected = text.replace(globalOf(re), rep);
      const result = replaceAll(text, re, rep);
      assert.strictEqual(result.text, expected, `replace(${JSON.stringify(text)}, ${re}, ${JSON.stringify(rep)})`);
      assert.strictEqual(result.text, naiveConcat(text, result.edits), 'naive concat model agrees');
    }
  });

  it('does not reprocess matches created by replacement text', () => {
    const result = replaceAll('aa', /a/, 'aa');
    assert.strictEqual(result.text, 'aaaa');
    assert.strictEqual(result.matches.length, 2);

    const self = replaceAll('b', /b/, 'b');
    assert.strictEqual(self.text, 'b');
    assert.strictEqual(self.matches.length, 1);

    const cascade = replaceAll('b', /b/, 'a');
    assert.strictEqual(cascade.text, 'a');
    assert.strictEqual(cascade.matches.length, 1);
  });

  it('applies all matches simultaneously against the frozen old text', () => {
    // Swap semantics: each replacement is computed from the OLD text only.
    const result = replaceAll('ab', /a|b/, (m) => (m[0] === 'a' ? 'b' : 'a'));
    assert.strictEqual(result.text, 'ba');
  });

  it('supports function replacements receiving the live match', () => {
    const result = replaceAll('a1 b22', /([a-z])(\d+)/, (m) => `${m[1].toUpperCase()}<${m[2]}>`);
    assert.strictEqual(result.text, 'A<1> B<22>');
  });
});

describe('replacement template syntax', () => {
  it('expands capture group references', () => {
    assert.strictEqual(replaceAll('John Smith', /(\w+) (\w+)/, '$2 $1').text, 'Smith John');
  });

  it('expands named capture groups', () => {
    assert.strictEqual(
      replaceAll('John Smith', /(?<first>\w+) (?<last>\w+)/, '$<last> $<first>').text,
      'Smith John',
    );
  });

  it('expands non-participating captures to empty text', () => {
    assert.strictEqual(replaceAll('b', /(a)|(b)/, '<$1>').text, '<>');
    assert.strictEqual(replaceAll('b', /(?<x>a)|(?<y>b)/, '<$<x>>').text, '<>');
  });

  it('treats $$ as an escaped literal dollar', () => {
    assert.strictEqual(replaceAll('x', /x/, '$$').text, '$');
    assert.strictEqual(replaceAll('x', /x/, '$$$&').text, '$x');
  });

  it('expands $` and $\' to the frozen before/after context', () => {
    assert.strictEqual(replaceAll('abc def', /def/, '[$`|$\']').text, 'abc [abc |]');
    assert.strictEqual(replaceAll('aXbXc', /X/, '$`').text, 'aabaXbc');
  });

  it('leaves unknown dollar sequences literal', () => {
    assert.strictEqual(replaceAll('x', /x/, '$9').text, '$9');
    assert.strictEqual(replaceAll('x', /x/, '$z').text, '$z');
    assert.strictEqual(replaceAll('x', /x/, '$<nope>').text, '$<nope>');
  });
});
