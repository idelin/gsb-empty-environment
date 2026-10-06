'use strict';

// 暂停检查点保存未完成字符串、嵌套注释栈和源偏移，恢复后与连续扫描一致。

const { lexAll, assertSameLex, rng, assert } = require('./helpers');
const { Lexer } = require('../src');

const INPUTS = [
  '',
  '/',
  'a "b\\"c" d',
  '"esc at end\\',
  '/* a /* b */ c */ d',
  '/* a /* b */ c',
  '// line\nnext',
  '"unterminated',
  '/* unterminated',
  'x"y/*z"//w\nq',
  '1.5 + 2. x',
  'a/b//c',
];

function lexContinuous(text) {
  return lexAll(text);
}

function lexWithPause(text, pauseAt, secondPauseAt) {
  const a = new Lexer();
  a.feed(text.slice(0, pauseAt));
  const cp1 = JSON.parse(JSON.stringify(a.checkpoint())); // serialize round-trip
  const b = Lexer.restore(cp1);
  if (secondPauseAt === undefined) {
    b.feed(text.slice(pauseAt));
    b.end();
    return { tokens: a.tokens.concat(b.tokens), diagnostics: b.diagnostics };
  }
  b.feed(text.slice(pauseAt, secondPauseAt));
  const cp2 = JSON.parse(JSON.stringify(b.checkpoint()));
  const c = Lexer.restore(cp2);
  c.feed(text.slice(secondPauseAt));
  c.end();
  return { tokens: a.tokens.concat(b.tokens, c.tokens), diagnostics: c.diagnostics };
}

test('pause/resume at every offset matches continuous scanning', () => {
  for (const input of INPUTS) {
    const expected = lexContinuous(input);
    for (let p = 0; p <= input.length; p++) {
      assertSameLex(lexWithPause(input, p), expected, `input ${JSON.stringify(input)} pause at ${p}`);
    }
  }
});

test('double pause/resume matches continuous scanning', () => {
  for (const input of INPUTS) {
    const expected = lexContinuous(input);
    for (let p1 = 0; p1 <= input.length; p1++) {
      for (let p2 = p1; p2 <= input.length; p2++) {
        assertSameLex(lexWithPause(input, p1, p2), expected, `input ${JSON.stringify(input)} pauses ${p1},${p2}`);
      }
    }
  }
});

test('checkpoint captures unfinished string, comment stack and offset', () => {
  const lexer = new Lexer();
  lexer.feed('ab /* x /* y');
  const cp = lexer.checkpoint();
  assert.strictEqual(cp.offset, 12);
  assert.strictEqual(cp.mode, 'blockComment');
  assert.deepStrictEqual(cp.commentStack, [3, 8]); // nested comment stack
  assert.strictEqual(cp.tokenStart, 3);

  const lexer2 = new Lexer();
  lexer2.feed('q "unfin');
  const cp2 = lexer2.checkpoint();
  assert.strictEqual(cp2.mode, 'string');
  assert.strictEqual(cp2.tokenStart, 2);
  assert.strictEqual(cp2.pendingText, '"unfin');
});

test('random chunked feeds with pauses match continuous scanning', () => {
  const rand = rng(7);
  const pieces = ['id', ' ', '"a\\"b"', '/* n /* e */ d */', '// l\n', '"open', '/* open', '*/', '"', '/'];
  for (let trial = 0; trial < 100; trial++) {
    const text = Array.from({ length: 8 }, () => pieces[Math.floor(rand() * pieces.length)]).join('');
    const expected = lexContinuous(text);
    let lexer = new Lexer();
    const segments = [];
    let pos = 0;
    while (pos < text.length) {
      const step = 1 + Math.floor(rand() * 5);
      lexer.feed(text.slice(pos, pos + step));
      pos += step;
      if (rand() < 0.4) {
        // pause and resume into a fresh lexer mid-stream
        const cp = JSON.parse(JSON.stringify(lexer.checkpoint()));
        segments.push(lexer.tokens);
        lexer = Lexer.restore(cp);
      }
    }
    lexer.end();
    segments.push(lexer.tokens);
    const tokens = segments.reduce((acc, seg) => acc.concat(seg), []);
    assertSameLex({ tokens, diagnostics: lexer.diagnostics }, expected, `trial ${trial} text ${JSON.stringify(text)}`);
  }
});
