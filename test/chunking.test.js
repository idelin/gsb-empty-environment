'use strict';

// 枚举短输入的所有切块位置，与一次读取结果比较。

const { lexAll, assertSameLex, rng, assert } = require('./helpers');
const { Lexer } = require('../src');

const INPUTS = [
  '',
  '/',
  '"',
  '"\\',
  '/*',
  '*/',
  '/*/',
  'a/b',
  '1.5',
  '1..2',
  '"a\\"b"',
  '"\\\\"',
  '// c\nx',
  '/* a */',
  '/*a/*b*/c*/',
  '/*a/*b*/',
  '"unterm',
  '/* unterm',
  'a//b\n"c"/*d*/e',
  '"s" /* c */ // l',
  'x"y/*z"//w',
];

function lexChunked(text, cuts) {
  const lexer = new Lexer();
  let prev = 0;
  for (const cut of cuts) {
    lexer.feed(text.slice(prev, cut));
    prev = cut;
  }
  lexer.feed(text.slice(prev));
  lexer.end();
  return { tokens: lexer.tokens, diagnostics: lexer.diagnostics };
}

test('all chunkings of short inputs match one-shot lexing', () => {
  for (const input of INPUTS) {
    const expected = lexAll(input);
    const n = input.length;
    const total = 1 << Math.max(0, n - 1); // every subset of cut positions
    for (let mask = 0; mask < total; mask++) {
      const cuts = [];
      for (let bit = 0; bit < n - 1; bit++) {
        if (mask & (1 << bit)) cuts.push(bit + 1);
      }
      assertSameLex(lexChunked(input, cuts), expected, `input ${JSON.stringify(input)} cuts [${cuts}]`);
    }
  }
});

test('random chunkings of a long mixed input match one-shot lexing', () => {
  const parts = [];
  const rand = rng(42);
  const pieces = ['abc', ' ', '"s\\"t"', '/* a /* b */ c */', '// x\n', '1.5', '/', '"', '/*', '*/', '\\', '\n', '$x9'];
  for (let k = 0; k < 60; k++) parts.push(pieces[Math.floor(rand() * pieces.length)]);
  const text = parts.join('');
  const expected = lexAll(text);
  for (let trial = 0; trial < 200; trial++) {
    const cuts = [];
    for (let p = 1; p < text.length; p++) if (rand() < 0.3) cuts.push(p);
    assertSameLex(lexChunked(text, cuts), expected, `random trial ${trial}`);
  }
});

test('tokens keep absolute UTF-16 ranges and tile the input', () => {
  const text = 'a "b\\"c" /* d /* e */ f */ // g\nh';
  const { tokens, diagnostics } = lexAll(text);
  assert.deepStrictEqual(diagnostics, []);
  let pos = 0;
  for (const t of tokens) {
    assert.strictEqual(t.start, pos);
    assert.strictEqual(t.text, text.slice(t.start, t.end));
    pos = t.end;
  }
  assert.strictEqual(pos, text.length);
});

test('unterminated constructs report their absolute start offsets', () => {
  const str = lexAll('ok "abc');
  assert.deepStrictEqual(str.diagnostics, [{ kind: 'unterminated-string', start: 3 }]);

  const cmt = lexAll('x /* a /* b */ c');
  assert.deepStrictEqual(cmt.diagnostics, [
    { kind: 'unterminated-comment', start: 2, opens: [2] },
  ]);

  // comment opener inside a string is inert; string still terminates
  const inert = lexAll('"/*" x');
  assert.deepStrictEqual(inert.diagnostics, []);
  assert.strictEqual(inert.tokens[0].type, 'string');
  // string quote inside a comment is inert too
  const inert2 = lexAll('/* " */ y');
  assert.deepStrictEqual(inert2.diagnostics, []);
  assert.strictEqual(inert2.tokens[0].type, 'blockComment');
});
