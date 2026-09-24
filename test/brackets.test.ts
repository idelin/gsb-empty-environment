import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { BracketAnalyzer, type BracketPair, type Diagnostic } from '../src/index.ts';

const spans = (a: BracketAnalyzer): number[][] =>
  a.getPairs().map((p: BracketPair) => [p.open, p.close]);

const diags = (a: BracketAnalyzer): Array<[string, number, number]> =>
  a.getDiagnostics().map((d: Diagnostic) => [d.code, d.start, d.end]);

describe('pairing across blocks and nesting', () => {
  test('nested blocks produce sorted pairs', () => {
    const a = new BracketAnalyzer('{ a { b } c }');
    assert.deepEqual(spans(a), [
      [0, 12],
      [4, 8],
    ]);
    assert.deepEqual(diags(a), []);
  });

  test('mixed bracket kinds across multiple blocks', () => {
    const a = new BracketAnalyzer('f(x, [1, {2}])');
    assert.deepEqual(spans(a), [
      [1, 13],
      [5, 12],
      [9, 11],
    ]);
    assert.deepEqual(diags(a), []);
  });
});

describe('strings and comments are ignored', () => {
  test('brackets inside a double-quoted string do not match', () => {
    const a = new BracketAnalyzer('x("({[") + y');
    assert.deepEqual(spans(a), [[1, 7]]);
    assert.deepEqual(diags(a), []);
  });

  test('brackets inside a single-quoted string do not match', () => {
    const a = new BracketAnalyzer("x(']})')");
    assert.deepEqual(spans(a), [[1, 7]]);
    assert.deepEqual(diags(a), []);
  });

  test('escaped quote does not end the string', () => {
    const a = new BracketAnalyzer('x("a\\"(b")');
    assert.deepEqual(spans(a), [[1, 9]]);
    assert.deepEqual(diags(a), []);
  });

  test('brackets inside a line comment do not match', () => {
    const a = new BracketAnalyzer('a // (}\nb');
    assert.deepEqual(spans(a), []);
    assert.deepEqual(diags(a), []);
  });

  test('brackets inside a block comment do not match', () => {
    const a = new BracketAnalyzer('/* { [ ( */ ()');
    assert.deepEqual(spans(a), [[12, 13]]);
    assert.deepEqual(diags(a), []);
  });

  test('unterminated string is diagnosed and swallows brackets', () => {
    const a = new BracketAnalyzer('x("abc');
    assert.deepEqual(spans(a), []);
    assert.deepEqual(diags(a), [
      ['unclosed-opening', 1, 2],
      ['unterminated-string', 2, 6],
    ]);
  });

  test('unterminated block comment is diagnosed and swallows brackets', () => {
    const a = new BracketAnalyzer('a /* b }');
    assert.deepEqual(spans(a), []);
    assert.deepEqual(diags(a), [['unterminated-comment', 2, 8]]);
  });
});

describe('mismatch diagnostics', () => {
  test('lone closing bracket', () => {
    const a = new BracketAnalyzer(')');
    assert.deepEqual(spans(a), []);
    assert.deepEqual(diags(a), [['unmatched-closing', 0, 1]]);
  });

  test('unclosed opener at end of input', () => {
    const a = new BracketAnalyzer('a + (b');
    assert.deepEqual(spans(a), []);
    assert.deepEqual(diags(a), [['unclosed-opening', 4, 5]]);
  });

  test('mismatched kinds recover and keep matching later pairs', () => {
    const a = new BracketAnalyzer('([)]');
    assert.deepEqual(spans(a), [[0, 2]]);
    assert.deepEqual(diags(a), [
      ['unclosed-opening', 1, 2],
      ['unmatched-closing', 3, 4],
    ]);
  });
});

describe('minimal enclosing pair query', () => {
  test('returns the innermost pair containing the offset', () => {
    const a = new BracketAnalyzer('a(b(c)d)e');
    assert.deepEqual(spans(a), [
      [1, 7],
      [3, 5],
    ]);
    assert.deepEqual(a.enclosingPair(4), { open: 3, close: 5, openChar: '(', closeChar: ')' });
    assert.deepEqual(a.enclosingPair(2), { open: 1, close: 7, openChar: '(', closeChar: ')' });
    assert.deepEqual(a.enclosingPair(5)?.open, 3);
    assert.equal(a.enclosingPair(0), null);
    assert.equal(a.enclosingPair(9), null);
  });

  test('works across sibling top-level pairs', () => {
    const a = new BracketAnalyzer('(a) (b) (c)');
    assert.equal(a.enclosingPair(1)?.open, 0);
    assert.equal(a.enclosingPair(5)?.open, 4);
    assert.equal(a.enclosingPair(9)?.open, 8);
    assert.equal(a.enclosingPair(3), null);
  });
});

describe('UTF-16 offsets and surrogate pairs', () => {
  const emoji = '\u{1F600}'; // 2 UTF-16 code units

  test('offsets count UTF-16 code units', () => {
    const a = new BracketAnalyzer(`${emoji}{ }`);
    assert.deepEqual(spans(a), [[2, 4]]);
    assert.deepEqual(a.positionAt(4), { line: 0, character: 4 });
    assert.deepEqual(a.enclosingPair(3), { open: 2, close: 4, openChar: '{', closeChar: '}' });
  });

  test('edits may not split a surrogate pair', () => {
    const a = new BracketAnalyzer(emoji);
    assert.throws(() => a.applyEdit(1, 1, 'x'), RangeError);
    assert.throws(() => a.applyEdit(0, 1, ''), RangeError);
    a.applyEdit(0, 2, ''); // removing the whole pair is fine
    assert.equal(a.getText(), '');
  });

  test('positionAt and enclosingPair reject surrogate-splitting offsets', () => {
    const a = new BracketAnalyzer(`${emoji}()`);
    assert.throws(() => a.positionAt(1), RangeError);
    assert.throws(() => a.enclosingPair(1), RangeError);
  });

  test('offsetAt rejects positions that resolve inside a surrogate pair', () => {
    const a = new BracketAnalyzer(`${emoji}\nx`);
    assert.throws(() => a.offsetAt({ line: 0, character: 1 }), RangeError);
    assert.equal(a.offsetAt({ line: 0, character: 2 }), 2);
  });
});

describe('LF and CRLF line endings', () => {
  test('matching and positions with LF', () => {
    const a = new BracketAnalyzer('{\n  x()\n}');
    assert.deepEqual(spans(a), [
      [0, 8],
      [5, 6],
    ]);
    assert.deepEqual(a.positionAt(8), { line: 2, character: 0 });
    assert.deepEqual(a.positionAt(5), { line: 1, character: 3 });
    assert.equal(a.offsetAt({ line: 1, character: 3 }), 5);
  });

  test('matching and positions with CRLF', () => {
    const a = new BracketAnalyzer('{\r\n  x()\r\n}');
    assert.deepEqual(spans(a), [
      [0, 10],
      [6, 7],
    ]);
    assert.deepEqual(a.positionAt(10), { line: 2, character: 0 });
    assert.deepEqual(a.positionAt(6), { line: 1, character: 3 });
    assert.equal(a.offsetAt({ line: 1, character: 3 }), 6);
  });

  test('offsetAt clamps the character to the line content (before CRLF)', () => {
    const a = new BracketAnalyzer('ab\r\ncd');
    assert.equal(a.offsetAt({ line: 0, character: 99 }), 2);
    assert.equal(a.offsetAt({ line: 1, character: 1 }), 5);
    assert.deepEqual(a.positionAt(5), { line: 1, character: 1 });
  });
});

describe('local edits', () => {
  test('inserting a closing bracket inside a string keeps outer structure', () => {
    const a = new BracketAnalyzer('f("s") + g()');
    assert.deepEqual(spans(a), [
      [1, 5],
      [10, 11],
    ]);
    a.applyEdit(4, 4, ')'); // inside the string literal, after `s`
    assert.equal(a.getText(), 'f("s)") + g()');
    assert.deepEqual(spans(a), [
      [1, 6],
      [11, 12],
    ]);
    assert.deepEqual(diags(a), []);
  });

  test('deleting a middle opener updates subsequent mismatches', () => {
    const a = new BracketAnalyzer('{ x { y } z }');
    assert.deepEqual(spans(a), [
      [0, 12],
      [4, 8],
    ]);
    a.applyEdit(4, 5, ''); // delete the inner '{'
    assert.equal(a.getText(), '{ x  y } z }');
    assert.deepEqual(spans(a), [[0, 7]]);
    assert.deepEqual(diags(a), [['unmatched-closing', 11, 12]]);
  });

  test('deleting an opener of another kind re-diagnoses the closer', () => {
    const a = new BracketAnalyzer('{ [ ] }');
    a.applyEdit(2, 3, ''); // delete '['
    assert.equal(a.getText(), '{  ] }');
    assert.deepEqual(spans(a), [[0, 5]]);
    assert.deepEqual(diags(a), [['unmatched-closing', 3, 4]]);
  });

  test('inserting text shifts later blocks consistently', () => {
    const source = 'fn main() {\n  foo();\n}\nfn other() {\n  bar();\n}';
    const a = new BracketAnalyzer(source);
    const before = spans(a);
    assert.equal(before.length, 6);
    const insert = '// comment\n';
    a.applyEdit(0, 0, insert);
    const after = spans(a);
    assert.equal(after.length, 6);
    assert.deepEqual(
      after,
      before.map(([open, close]) => [open + insert.length, close + insert.length]),
    );
    assert.deepEqual(diags(a), []);
  });

  test('edits may not have start after end', () => {
    const a = new BracketAnalyzer('()');
    assert.throws(() => a.applyEdit(2, 1, ''), RangeError);
  });
});

describe('deep nesting without recursion', () => {
  test('depth 10_000 matches and queries without stack overflow', () => {
    const depth = 10_000;
    const a = new BracketAnalyzer('('.repeat(depth) + ')'.repeat(depth));
    assert.equal(a.getPairs().length, depth);
    assert.deepEqual(diags(a), []);
    const innermost = a.enclosingPair(depth);
    assert.deepEqual(innermost, {
      open: depth - 1,
      close: depth,
      openChar: '(',
      closeChar: ')',
    });
    const outermost = a.enclosingPair(0);
    assert.deepEqual(outermost, { open: 0, close: 2 * depth - 1, openChar: '(', closeChar: ')' });
  });

  test('depth 10_000 of unclosed openers is diagnosed iteratively', () => {
    const depth = 10_000;
    const a = new BracketAnalyzer('{'.repeat(depth));
    const d = a.getDiagnostics();
    assert.equal(d.length, depth);
    assert.ok(d.every((x) => x.code === 'unclosed-opening'));
  });
});
