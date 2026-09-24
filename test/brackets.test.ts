import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LanguageService } from '../src/index.ts';

test('matches nested pairs across blocks and lines', () => {
  const svc = new LanguageService('fn main() {\r\n  let x = (1 + [2, 3]);\n}\n');
  assert.equal(svc.getDiagnostics().length, 0);
  const pairs = svc.getPairs();
  assert.deepEqual(
    pairs.map((p) => [p.openChar, p.open, p.close]),
    [
      ['(', 7, 8],
      ['{', 10, 37],
      ['(', 23, 34],
      ['[', 28, 33],
    ],
  );
  assert.equal(svc.matchAt(7), 8);
  assert.equal(svc.matchAt(8), 7);
  assert.equal(svc.matchAt(37), 10);
  assert.equal(svc.matchAt(0), null); // not a bracket
});

test('brackets inside strings and comments do not participate', () => {
  const svc = new LanguageService('("(" /* ) */ // )\n)');
  assert.equal(svc.getDiagnostics().length, 0);
  assert.deepEqual(
    svc.getPairs().map((p) => [p.open, p.close]),
    [[0, 18]],
  );
});

test('unmatched closing bracket is reported', () => {
  const svc = new LanguageService('a + b)');
  const diags = svc.getDiagnostics();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].code, 'unmatched-close');
  assert.equal(diags[0].start, 5);
});

test('unclosed opener is reported at end of input', () => {
  const svc = new LanguageService('f(1, {2: 3}');
  const diags = svc.getDiagnostics();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].code, 'unclosed-open');
  assert.equal(diags[0].start, 1);
  // the inner braces still match
  assert.deepEqual(
    svc.getPairs().map((p) => [p.open, p.close]),
    [[5, 10]],
  );
});

test('mismatched closer is reported and matching recovers', () => {
  // "(" then "]" mismatches; the following ")" still closes "(".
  const svc = new LanguageService('(])');
  const diags = svc.getDiagnostics();
  assert.equal(diags.length, 1);
  assert.equal(diags[0].code, 'mismatched-close');
  assert.equal(diags[0].start, 1);
  assert.deepEqual(
    svc.getPairs().map((p) => [p.open, p.close]),
    [[0, 2]],
  );
});

test('unterminated string and block comment produce diagnostics', () => {
  const svc = new LanguageService('let s = "abc\n/* x');
  const codes = svc.getDiagnostics().map((d) => d.code);
  assert.deepEqual(codes, ['unterminated-string', 'unterminated-block-comment']);
});

test('enclosingPair returns the minimal enclosing pair', () => {
  const text = '(a {b} [c] d)';
  const svc = new LanguageService(text);
  const inner = svc.enclosingPair(4); // on 'b'
  assert.deepEqual([inner!.open, inner!.close], [3, 5]); // {b}
  const mid = svc.enclosingPair(8); // on 'c'
  assert.deepEqual([mid!.open, mid!.close], [7, 9]); // [c]
  const outer = svc.enclosingPair(11); // on 'd'
  assert.deepEqual([outer!.open, outer!.close], [0, 12]); // ( ... )
  // offset between two sibling pairs belongs to the outer pair
  const between = svc.enclosingPair(6);
  assert.deepEqual([between!.open, between!.close], [0, 12]);
});

test('enclosingPair boundaries: brackets themselves are not enclosed', () => {
  const svc = new LanguageService('(x)');
  assert.equal(svc.enclosingPair(0), null); // on '('
  assert.equal(svc.enclosingPair(2), null); // on ')'
  assert.deepEqual([svc.enclosingPair(1)!.open, svc.enclosingPair(1)!.close], [0, 2]);
  assert.equal(svc.enclosingPair(99), null); // outside the text
});

test('pairAt identifies the pair a bracket belongs to', () => {
  const svc = new LanguageService('[()]');
  const pair = svc.pairAt(1)!;
  assert.deepEqual([pair.open, pair.close], [1, 2]);
  assert.deepEqual([svc.pairAt(2)!.open, svc.pairAt(2)!.close], [1, 2]);
  assert.deepEqual([svc.pairAt(0)!.open, svc.pairAt(0)!.close], [0, 3]);
  assert.equal(svc.pairAt(10), null);
});

test('emoji before brackets keeps UTF-16 offsets honest', () => {
  const svc = new LanguageService('😀(x)');
  // 😀 is two UTF-16 code units, so '(' sits at offset 2.
  assert.deepEqual(
    svc.getPairs().map((p) => [p.open, p.close]),
    [[2, 4]],
  );
  assert.equal(svc.matchAt(2), 4);
});
