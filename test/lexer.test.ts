import { test } from 'node:test';
import assert from 'node:assert/strict';
import { lexAll } from '../src/index.ts';

function bracketOffsets(text: string): Array<[string, number]> {
  return lexAll(text)
    .filter((t) => t.type === 'open' || t.type === 'close')
    .map((t) => [t.char!, t.start]);
}

test('brackets in strings are not lexed as brackets', () => {
  assert.deepEqual(bracketOffsets('a "([{" b'), []);
  assert.deepEqual(bracketOffsets("a ')]}' b"), []);
});

test('escaped quotes do not end a string', () => {
  // "a\"(" -> the ( is still inside the string
  assert.deepEqual(bracketOffsets('x = "a\\"(b" + (1)'), [['(', 14], [')', 16]]);
});

test('brackets in line and block comments are ignored', () => {
  assert.deepEqual(bracketOffsets('// ( [ {\ncode'), []);
  assert.deepEqual(bracketOffsets('/* ( [ { */ (1)'), [['(', 12], [')', 14]]);
  assert.deepEqual(bracketOffsets('/* multi\nline ( ) */ ()'), [['(', 21], [')', 22]]);
});

test('unterminated string stops at end of line (LF and CRLF)', () => {
  const lf = lexAll('"abc\n(');
  assert.equal(lf[0].type, 'string');
  assert.equal(lf[0].unterminated, true);
  assert.deepEqual(bracketOffsets('"abc\n('), [['(', 5]]);

  const crlf = lexAll('"abc\r\n(');
  assert.equal(crlf[0].type, 'string');
  assert.equal(crlf[0].unterminated, true);
  assert.deepEqual(bracketOffsets('"abc\r\n('), [['(', 6]]);
});

test('unterminated block comment runs to EOF', () => {
  const tokens = lexAll('a /* b ( c');
  const comment = tokens.find((t) => t.type === 'block-comment')!;
  assert.equal(comment.unterminated, true);
  assert.equal(comment.end, 10);
  assert.deepEqual(bracketOffsets('a /* b ( c'), []);
});

test('a slash that is not a comment is plain code', () => {
  assert.deepEqual(bracketOffsets('a / b / * c (1)'), [['(', 12], [')', 14]]);
});
