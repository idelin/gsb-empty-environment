import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LanguageService, lexAll, analyzeBrackets, splitsSurrogatePair } from '../src/index.ts';

test('inserting ")" inside a string does not change the outer structure', () => {
  const svc = new LanguageService('f("x")');
  const before = svc.getPairs().map((p) => [p.open, p.close]);
  assert.deepEqual(before, [[1, 5]]);

  svc.applyEdit(4, 4, ')'); // -> f("x)")
  assert.equal(svc.text, 'f("x)")');
  assert.equal(svc.getDiagnostics().length, 0);
  assert.equal(svc.getPairs().length, 1);
  // the inserted ")" sits inside the string and matches nothing
  assert.equal(svc.matchAt(4), null);
  // the real closer shifted by one
  assert.deepEqual(
    svc.getPairs().map((p) => [p.open, p.close]),
    [[1, 6]],
  );
});

test('inserting "(" inside a string creates no unclosed opener', () => {
  const svc = new LanguageService('f("x")');
  svc.applyEdit(3, 3, '('); // -> f("x(")
  assert.equal(svc.getDiagnostics().length, 0);
  assert.equal(svc.getPairs().length, 1);
});

test('deleting a middle opener updates downstream mismatches', () => {
  const svc = new LanguageService('( [ ) ]');
  // ")" mismatches "["; the outer "(" is left unclosed
  assert.deepEqual(
    svc.getDiagnostics().map((d) => [d.code, d.start]),
    [
      ['unclosed-open', 0],
      ['mismatched-close', 4],
    ],
  );
  svc.applyEdit(2, 4, ''); // delete "[ " -> "( ) ]"
  assert.equal(svc.text, '( ) ]');
  // now "(" and ")" match, and "]" is the unmatched one
  assert.deepEqual(
    svc.getPairs().map((p) => [p.open, p.close]),
    [[0, 2]],
  );
  assert.deepEqual(
    svc.getDiagnostics().map((d) => [d.code, d.start]),
    [['unmatched-close', 4]],
  );
});

test('deleting an opener re-pairs the rest of the document', () => {
  const svc = new LanguageService('(a)(b)(c)');
  assert.equal(svc.getPairs().length, 3);
  svc.applyEdit(0, 1, ''); // delete first "(" -> "a)(b)(c)"
  assert.deepEqual(
    svc.getDiagnostics().map((d) => [d.code, d.start]),
    [['unmatched-close', 1]],
  );
  assert.deepEqual(
    svc.getPairs().map((p) => [p.open, p.close]),
    [
      [2, 4],
      [5, 7],
    ],
  );
});

test('typing "/*" at the top comments out the rest, removing it restores', () => {
  const svc = new LanguageService('(a)\n(b)\n');
  assert.equal(svc.getPairs().length, 2);

  svc.applyEdit(0, 0, '/*');
  assert.equal(svc.getPairs().length, 0);
  assert.deepEqual(
    svc.getDiagnostics().map((d) => d.code),
    ['unterminated-block-comment'],
  );

  svc.applyEdit(2, 2, '*/'); // close it: "/**/(a)\n(b)\n"
  assert.equal(svc.getPairs().length, 2);
  assert.equal(svc.getDiagnostics().length, 0);

  svc.applyEdit(0, 4, ''); // remove "/**/" again
  assert.equal(svc.getPairs().length, 2);
  assert.equal(svc.getDiagnostics().length, 0);
});

test('editing across CRLF lines keeps pairs and positions correct', () => {
  const svc = new LanguageService('fn() {\r\n  (1)\r\n}\r\n');
  assert.equal(svc.getDiagnostics().length, 0);
  const openOffset = svc.offsetAt({ line: 1, character: 2 });
  const pair = svc.pairAt(openOffset)!;
  assert.deepEqual(svc.positionAt(pair.close), { line: 1, character: 4 });

  svc.applyEdit(openOffset + 3, openOffset + 3, '\r\n  (2)'); // add a line
  assert.equal(svc.getDiagnostics().length, 0);
  assert.equal(svc.getPairs().length, 4);
  assert.deepEqual(svc.positionAt(svc.getPairs()[3].close), { line: 2, character: 4 });
});

test('incremental lexer stays in sync with a full re-lex under random edits', () => {
  // Deterministic PRNG (LCG) so failures are reproducible.
  let seed = 0x2f6e2b1;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };
  const pieces = ['(', ')', '[', ']', '{', '}', '"', "'", '/', '*', 'a', 'b', ' ', '=', '\n', '\r\n', '😀', '\\'];
  const pick = () => pieces[Math.floor(rand() * pieces.length)];

  let text = '';
  for (let i = 0; i < 40; i++) text += pick();
  const svc = new LanguageService(text);

  const assertConsistent = (label: string) => {
    const expectedTokens = lexAll(svc.text);
    assert.deepEqual(
      svc.getTokens().map((tk) => [tk.type, tk.start, tk.end, tk.char ?? null, tk.unterminated ?? false]),
      expectedTokens.map((tk) => [tk.type, tk.start, tk.end, tk.char ?? null, tk.unterminated ?? false]),
      `${label}: token stream diverged`,
    );
    const expected = analyzeBrackets(expectedTokens);
    assert.deepEqual(svc.getDiagnostics(), expected.diagnostics, `${label}: diagnostics diverged`);
    assert.deepEqual(svc.getPairs(), expected.pairs, `${label}: pairs diverged`);
  };

  assertConsistent('initial');
  for (let i = 0; i < 400; i++) {
    const current = svc.text;
    let start = Math.floor(rand() * (current.length + 1));
    let end = start + Math.floor(rand() * Math.min(8, current.length - start + 1));
    if (splitsSurrogatePair(current, start) || splitsSurrogatePair(current, end)) {
      continue; // the API forbids these edits; covered by document tests
    }
    let inserted = '';
    const count = Math.floor(rand() * 4);
    for (let k = 0; k < count; k++) inserted += pick();
    svc.applyEdit(start, end, inserted);
    assertConsistent(`edit #${i} [${start},${end}) += ${JSON.stringify(inserted)}`);
  }
});
