import { test } from 'node:test';
import assert from 'node:assert/strict';
import { IncrementalLexer, type Token } from '../src/lexer.ts';

function types(tokens: Token[]): string[] {
  return tokens.map((t) => t.type);
}

function texts(lexer: IncrementalLexer, tokens: Token[]): string[] {
  const doc = lexer.getText();
  return tokens.map((t) => doc.slice(t.start, t.end));
}

test('tokenizes keywords, identifiers, numbers, strings, comments with UTF-16 offsets', () => {
  const src = 'let name = "hi"\n// note\nlet n = 3.14';
  const lex = new IncrementalLexer(src);
  const tokens = lex.getTokens();
  assert.deepEqual(types(tokens), [
    'keyword', 'identifier', 'punctuation', 'string',
    'comment',
    'keyword', 'identifier', 'punctuation', 'number',
  ]);
  assert.deepEqual(texts(lex, tokens), [
    'let', 'name', '=', '"hi"',
    '// note',
    'let', 'n', '=', '3.14',
  ]);
  // offsets are exact UTF-16 indices into the source
  assert.deepEqual(tokens[3], { type: 'string', start: 11, end: 15 });
  assert.equal(src.slice(11, 15), '"hi"');
  assert.deepEqual(tokens[4], { type: 'comment', start: 16, end: 23 });
});

test('escaped quote does not terminate a string', () => {
  const lex = new IncrementalLexer('let s = "a\\"b" + "c"');
  const tokens = lex.getTokens();
  const strings = tokens.filter((t) => t.type === 'string');
  assert.equal(strings.length, 2);
  assert.deepEqual(texts(lex, strings), ['"a\\"b"', '"c"']);
  // the escaped quote is inside the first string, not a terminator
  assert.equal(strings[0].start, 8); // 'let s = '.length
  assert.equal(strings[0].end, 14);
});

test('unterminated string ends at end of line and does not leak state', () => {
  const lex = new IncrementalLexer('let s = "oops\nlet t = 1');
  assert.deepEqual(lex.getLineState(0), { entry: 'code', exit: 'code' });
  assert.deepEqual(types(lex.getLineTokens(1)), [
    'keyword', 'identifier', 'punctuation', 'number',
  ]);
});

test('block comment spans lines; lines inside are comment tokens', () => {
  const src = 'let a = 1\n/* start\nmiddle */\nlet b = 2';
  const lex = new IncrementalLexer(src);
  assert.deepEqual(lex.getLineState(1), { entry: 'code', exit: 'comment' });
  assert.deepEqual(lex.getLineState(2), { entry: 'comment', exit: 'code' });
  assert.deepEqual(types(lex.getLineTokens(1)), ['comment']);
  assert.deepEqual(types(lex.getLineTokens(2)), ['comment']);
  assert.deepEqual(types(lex.getLineTokens(3)), [
    'keyword', 'identifier', 'punctuation', 'number',
  ]);
});

test('"/*/" is an unterminated block comment, not a closed one', () => {
  const lex = new IncrementalLexer('/*/ still comment');
  assert.deepEqual(types(lex.getLineTokens(0)), ['comment']);
  assert.equal(lex.getLineState(0).exit, 'comment');
});

test('CRLF line endings: offsets and line states are correct', () => {
  const src = 'let a = 1\r\n/* c\r\nd */\r\nlet b = 2';
  const lex = new IncrementalLexer(src);
  assert.equal(lex.getLineCount(), 4);
  assert.equal(lex.getLineStart(1), 11); // 9 chars + CRLF
  assert.deepEqual(lex.getLineState(1), { entry: 'code', exit: 'comment' });
  assert.deepEqual(lex.getLineState(2), { entry: 'comment', exit: 'code' });
  const tokens = lex.getTokens();
  assert.deepEqual(texts(lex, tokens), [
    'let', 'a', '=', '1',
    '/* c',
    'd */',
    'let', 'b', '=', '2',
  ]);
  // no token covers the \r or \n
  for (const t of tokens) {
    assert.ok(!src.slice(t.start, t.end).includes('\r'));
    assert.ok(!src.slice(t.start, t.end).includes('\n'));
  }
});

test('mixed LF and CRLF in one document', () => {
  const src = 'a\nb\r\nc';
  const lex = new IncrementalLexer(src);
  assert.equal(lex.getLineCount(), 3);
  assert.equal(lex.getLineStart(1), 2);
  assert.equal(lex.getLineStart(2), 5);
  assert.deepEqual(texts(lex, lex.getTokens()), ['a', 'b', 'c']);
});

test('deleting an early */ recolors many following lines', () => {
  const src = '/* header */\nlet a = 1\nlet b = 2\nlet c = 3';
  const lex = new IncrementalLexer(src);
  assert.deepEqual(types(lex.getLineTokens(1)), [
    'keyword', 'identifier', 'punctuation', 'number',
  ]);

  // delete the "*/" on line 0 -> comment now swallows everything below
  const close = src.indexOf('*/');
  const { invalidatedLines } = lex.applyEdit(close, close + 2, '');
  assert.deepEqual(invalidatedLines, [0, 1, 2, 3]);
  assert.deepEqual(types(lex.getLineTokens(1)), ['comment']);
  assert.deepEqual(types(lex.getLineTokens(3)), ['comment']);
  assert.equal(lex.getLineState(3).exit, 'comment');
});

test('inserting */ at a line end terminates the comment and recolors downstream', () => {
  const src = '/* header */\nlet a = 1\nlet b = 2\nlet c = 3';
  const lex = new IncrementalLexer(src);
  const close = src.indexOf('*/');
  lex.applyEdit(close, close + 2, ''); // comment now runs to EOF

  // terminate the comment at the end of line 1 (just before its newline)
  const line1End = lex.getLineStart(2) - 1;
  const { invalidatedLines } = lex.applyEdit(line1End, line1End, ' */');
  assert.deepEqual(invalidatedLines, [1, 2, 3]);
  assert.deepEqual(types(lex.getLineTokens(2)), [
    'keyword', 'identifier', 'punctuation', 'number',
  ]);
  assert.deepEqual(types(lex.getLineTokens(3)), [
    'keyword', 'identifier', 'punctuation', 'number',
  ]);
});

test('state propagation stops where downstream lines stabilize', () => {
  // two independent comments; deleting the first terminator extends the
  // comment only until the SECOND terminator, so lines 3-4 never re-lex
  const src = '/* a */\nx1\n/* b */\ny1\nz1';
  const lex = new IncrementalLexer(src);
  lex.getTokens(); // warm the cache

  const close = src.indexOf('*/');
  const { invalidatedLines } = lex.applyEdit(close, close + 2, '');
  assert.deepEqual(invalidatedLines, [0, 1, 2]);
  assert.deepEqual(types(lex.getLineTokens(1)), ['comment']);
  assert.deepEqual(types(lex.getLineTokens(2)), ['comment']);
  // lines past the second terminator are untouched
  assert.deepEqual(texts(lex, lex.getLineTokens(3)), ['y1']);
  assert.deepEqual(texts(lex, lex.getLineTokens(4)), ['z1']);
});

test('single-char edit inside an identifier invalidates only its own line', () => {
  const src = 'let alpha = 1\nlet beta = 2\nlet gamma = 3\nlet delta = 4';
  const lex = new IncrementalLexer(src);
  lex.getTokens(); // warm the cache

  const pos = src.indexOf('beta') + 1; // inside "beta"
  const { invalidatedLines } = lex.applyEdit(pos, pos + 1, 'x');
  assert.deepEqual(invalidatedLines, [1]);

  // downstream lines keep their exact token objects (no recompute)
  assert.deepEqual(texts(lex, lex.getLineTokens(2)), ['let', 'gamma', '=', '3']);
  assert.deepEqual(texts(lex, lex.getLineTokens(3)), ['let', 'delta', '=', '4']);
});

test('edit inside a comment line does not propagate past the comment end', () => {
  const src = '/* one\ntwo\nthree */\nlet x = 1\nlet y = 2';
  const lex = new IncrementalLexer(src);
  lex.getTokens();

  const pos = src.indexOf('two') + 1;
  const { invalidatedLines } = lex.applyEdit(pos, pos + 1, 'X');
  assert.deepEqual(invalidatedLines, [1]); // exit state unchanged: still 'comment'
  assert.deepEqual(types(lex.getLineTokens(3)), [
    'keyword', 'identifier', 'punctuation', 'number',
  ]);
});

test('newline insertion splits lines and invalidates only the affected region', () => {
  const src = 'let a = 1\nlet b = 2';
  const lex = new IncrementalLexer(src);
  lex.getTokens();

  const pos = src.indexOf('= 1') + 2; // before "1"
  const { invalidatedLines } = lex.applyEdit(pos, pos, '\n');
  assert.deepEqual(invalidatedLines, [0, 1]);
  assert.equal(lex.getLineCount(), 3);
  assert.deepEqual(texts(lex, lex.getLineTokens(2)), ['let', 'b', '=', '2']);
});

test('UTF-16 offsets: astral chars count as 2 code units, pairs never split', () => {
  const emoji = '\u{1F600}'; // 😀, 2 UTF-16 code units
  const src = `let s = "${emoji}"\nlet t = 1`;
  const lex = new IncrementalLexer(src);
  const str = lex.getTokens().find((t) => t.type === 'string')!;
  assert.equal(str.end - str.start, 2 + emoji.length); // quotes + 2 code units
  assert.equal(src.slice(str.start, str.end), `"${emoji}"`);
  assert.equal(lex.getLineStart(1), str.end + 1); // + '\n'

  // astral identifier is a single token spanning both surrogate halves
  const lex2 = new IncrementalLexer(`let ${emoji}x = 1`);
  const ident = lex2.getTokens()[1];
  assert.equal(ident.type, 'identifier');
  assert.equal(ident.end - ident.start, 3); // 😀(2) + x(1)
});

test('edit boundary inside a surrogate pair is rejected', () => {
  const emoji = '\u{1F600}';
  const src = `let s = "${emoji}"`;
  const lex = new IncrementalLexer(src);
  const pairStart = src.indexOf(emoji);
  // boundary between high and low surrogate
  assert.throws(() => lex.applyEdit(pairStart + 1, pairStart + 1, 'x'), RangeError);
  assert.throws(() => lex.applyEdit(pairStart, pairStart + 1, ''), RangeError);
  // boundaries around the whole pair are fine
  lex.applyEdit(pairStart, pairStart + 2, 'ok');
  assert.deepEqual(texts(lex, lex.getTokens().filter((t) => t.type === 'string')), ['"ok"']);
});

test('invalid edit ranges are rejected', () => {
  const lex = new IncrementalLexer('abc');
  assert.throws(() => lex.applyEdit(-1, 0, ''), RangeError);
  assert.throws(() => lex.applyEdit(2, 1, ''), RangeError);
  assert.throws(() => lex.applyEdit(0, 4, ''), RangeError);
  assert.throws(() => lex.applyEdit(0.5, 1, ''), RangeError);
});

test('line comment runs to end of line only', () => {
  const lex = new IncrementalLexer('a // c\nb');
  assert.deepEqual(types(lex.getLineTokens(0)), ['identifier', 'comment']);
  assert.deepEqual(types(lex.getLineTokens(1)), ['identifier']);
});

test('string/comment starters inside comments and strings are inert', () => {
  const lex = new IncrementalLexer('/* " // */ let x = "/*"');
  const tokens = lex.getTokens();
  assert.deepEqual(types(tokens), [
    'comment', 'keyword', 'identifier', 'punctuation', 'string',
  ]);
  assert.equal(lex.getLineState(0).exit, 'code');
});

test('empty document and document ending with newline', () => {
  const lex = new IncrementalLexer('');
  assert.equal(lex.getLineCount(), 1);
  assert.deepEqual(lex.getTokens(), []);

  const lex2 = new IncrementalLexer('a\n');
  assert.equal(lex2.getLineCount(), 2);
  assert.deepEqual(texts(lex2, lex2.getTokens()), ['a']);
});
