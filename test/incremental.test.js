'use strict';

// 增量编辑按旧版本坐标给出；重扫至词法状态与 token 边界均稳定。
// 逐字符删改验证远端注释状态传播。

const { assertConsistent, rng, assert, IncrementalLexer } = require('./helpers');

const CORPUS = [
  '',
  'a',
  '/',
  'alpha beta gamma',
  'let s = "he\\"llo" /* c /* n */ d */ // t\nend',
  '/* a */ x /* b */ y',
  '"open string',
  '/* open comment',
  'a // b\n"c" /* d /* e */ f */ g',
  '"s1" "s2" /* c */ // l\n',
];

const EDIT_CHARS = ['/', '*', '"', 'a', '1', ' ', '\n', '\\'];

test('exhaustive single-char insertions match fresh lexing', () => {
  for (const text of CORPUS) {
    for (let pos = 0; pos <= text.length; pos++) {
      for (const ch of EDIT_CHARS) {
        const inc = new IncrementalLexer(text);
        inc.applyEdit(pos, pos, ch);
        assertConsistent(inc, `insert ${JSON.stringify(ch)} at ${pos} into ${JSON.stringify(text)}`);
      }
    }
  }
});

test('exhaustive single-char deletions match fresh lexing', () => {
  for (const text of CORPUS) {
    for (let pos = 0; pos < text.length; pos++) {
      const inc = new IncrementalLexer(text);
      inc.applyEdit(pos, pos + 1, '');
      assertConsistent(inc, `delete at ${pos} of ${JSON.stringify(text)}`);
    }
  }
});

test('exhaustive single-char replacements match fresh lexing', () => {
  for (const text of CORPUS) {
    for (let pos = 0; pos < text.length; pos++) {
      for (const ch of EDIT_CHARS) {
        const inc = new IncrementalLexer(text);
        inc.applyEdit(pos, pos + 1, ch);
        assertConsistent(inc, `replace at ${pos} with ${JSON.stringify(ch)} in ${JSON.stringify(text)}`);
      }
    }
  }
});

test('exhaustive multi-char deletions match fresh lexing', () => {
  const text = 'a "b" /* c /* d */ e */ f // g\nh';
  for (let s = 0; s < text.length; s++) {
    for (let e = s; e <= text.length; e++) {
      const inc = new IncrementalLexer(text);
      inc.applyEdit(s, e, '');
      assertConsistent(inc, `delete [${s},${e}) of ${JSON.stringify(text)}`);
    }
  }
});

test('sequential random edits in old-version coordinates match fresh lexing', () => {
  const rand = rng(1234);
  const pieces = ['aa', '"s\\"t"', '/* c /* n */ d */', '// l\n', '/', '*', '"', '\\', ' ', 'x1'];
  let text = Array.from({ length: 30 }, () => pieces[Math.floor(rand() * pieces.length)]).join(' ');
  const inc = new IncrementalLexer(text);
  for (let step = 0; step < 300; step++) {
    const len = inc.text.length;
    const start = Math.floor(rand() * (len + 1));
    const end = Math.min(len, start + Math.floor(rand() * 6));
    const inserted = rand() < 0.5 ? pieces[Math.floor(rand() * pieces.length)] : '';
    inc.applyEdit(start, end, inserted); // coordinates of the previous version
    assertConsistent(inc, `after random edit ${step}: [${start},${end}) += ${JSON.stringify(inserted)}`);
  }
});

test('char-by-char edits propagate far-end block comment state', () => {
  // A comment opener far from the tail: removing it char by char must turn
  // the whole tail back into code, one edit at a time.
  const tail = Array.from({ length: 200 }, (_, k) => `v${k}`).join(' ');
  const inc = new IncrementalLexer('/*' + tail + '*/');
  assert.strictEqual(inc.tokens.length, 1); // one big comment
  inc.applyEdit(1, 2, ''); // delete '*' -> '/...' still not a comment
  assertConsistent(inc, 'after deleting * of opener');
  assert.notStrictEqual(inc.tokens.length, 1);
  inc.applyEdit(0, 1, ''); // delete '/' -> pure code
  assertConsistent(inc, 'after deleting / of opener');

  // Now type the opener back, char by char, at the far front.
  inc.applyEdit(0, 0, '/');
  assertConsistent(inc, 're-insert /');
  inc.applyEdit(1, 1, '*');
  assertConsistent(inc, 're-insert * -> tail becomes comment again');
  assert.strictEqual(inc.tokens[inc.tokens.length - 1].type, 'blockComment');
  assert.deepStrictEqual(inc.diagnostics, []);
});

test('char-by-char deletion of a closing comment opener shifts nesting', () => {
  const tail = Array.from({ length: 100 }, (_, k) => `w${k}`).join(' ');
  const text = '/* outer /* inner */ ' + tail + ' */';
  const inc = new IncrementalLexer(text);
  assert.strictEqual(inc.tokens.length, 1);
  // Delete the inner opener one char at a time: the first '*/' now closes
  // the outer comment and the far tail becomes code.
  inc.applyEdit(10, 11, ''); // delete '*' of inner '/*'
  assertConsistent(inc, 'inner opener half deleted');
  inc.applyEdit(9, 10, ''); // delete '/' of inner '/*'
  assertConsistent(inc, 'inner opener fully deleted');
  assert.ok(inc.tokens.length > 1);
  assert.strictEqual(inc.tokens[0].type, 'blockComment');
  assert.strictEqual(inc.tokens[0].text, '/* outer  inner */');
});

test('unterminated constructs across edits keep absolute offsets', () => {
  const inc = new IncrementalLexer('aa "bb" cc');
  inc.applyEdit(3, 4, ''); // delete opening quote -> stray quote unterminates
  assertConsistent(inc, 'quote deleted');
  assert.deepStrictEqual(inc.diagnostics, [{ kind: 'unterminated-string', start: 5 }]);
  inc.applyEdit(0, 0, '"'); // new opener pairs with the stray quote
  assertConsistent(inc, 'string repaired at 0');
  assert.deepStrictEqual(inc.diagnostics, []);
  inc.applyEdit(0, 0, '/* x /* y'); // nested unterminated comment swallows all
  assertConsistent(inc, 'nested unterminated comment');
  assert.deepStrictEqual(inc.diagnostics, [
    { kind: 'unterminated-comment', start: 0, opens: [0, 5] },
  ]);
});

test('rescan stops at the first stable token boundary, not a fixed line count', () => {
  const long = Array.from({ length: 2000 }, (_, k) => `id${k}`).join(' ');
  const inc = new IncrementalLexer(long);
  // Small edit in the middle: only a few tokens need rescanning even though
  // hundreds of lines follow.
  const mid = long.indexOf('id1000');
  inc.applyEdit(mid, mid + 1, 'q');
  assertConsistent(inc, 'middle edit');
  assert.strictEqual(inc.lastRescan.stable, true);
  assert.ok(inc.lastRescan.scanned < 512, `scanned ${inc.lastRescan.scanned}`);

  // An edit that opens a comment must rescan to the very end: stability is
  // never reached early, regardless of how many lines follow.
  const inc2 = new IncrementalLexer(long);
  inc2.applyEdit(0, 0, '/*');
  assertConsistent(inc2, 'opener at start');
  assert.strictEqual(inc2.lastRescan.stable, false);
  assert.strictEqual(inc2.lastRescan.scanned, inc2.text.length);
});

test('edits at and around token boundaries', () => {
  const text = 'ab "cd" /*ef*/ gh';
  const cases = [
    [0, 0, 'z'], // before everything
    [2, 2, 'z'], // identifier boundary
    [3, 3, '/'], // before string -> punct then string
    [8, 8, '/'], // after string
    [9, 9, '*'], // before comment
    [10, 10, '/'], // inside comment opener
    [15, 16, ''], // delete '*/' closer half
    [17, 17, '"'], // before gh
    [17, 17, '*/'], // at very end
  ];
  for (const [s, e, ins] of cases) {
    const inc = new IncrementalLexer(text);
    inc.applyEdit(s, e, ins);
    assertConsistent(inc, `edit [${s},${e}) += ${JSON.stringify(ins)} in ${JSON.stringify(text)}`);
  }
});
