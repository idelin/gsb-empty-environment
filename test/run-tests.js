import assert from 'node:assert/strict';
import { lexLine, State } from '../src/lexer.js';
import { IncrementalHighlighter, fullHighlight } from '../src/highlighter.js';

const tests = [];
function test(name, fn) { tests.push({ name, fn }); }

/** Assert incremental result equals a from-scratch full highlight. */
function assertMatchesFull(h) {
  const expected = fullHighlight(h.getText());
  assert.deepEqual(h.getTokens(), expected, 'incremental tokens must equal full highlight');
}

/** Tokens of one line from a token list. */
function tokensOnLine(tokens, line) {
  return tokens.filter(t => t.line === line);
}

// ---------- lexer basics ----------

test('lexes keywords, identifiers, numbers, punctuation', () => {
  const { tokens, outputState } = lexLine('const x = 0x1F + 2.5;', State.Code);
  assert.deepEqual(tokens.map(t => t.type),
    ['keyword', 'identifier', 'punctuation', 'number', 'punctuation', 'number', 'punctuation']);
  assert.equal(outputState, State.Code);
});

test('single-quoted and double-quoted strings do not cross lines', () => {
  const r1 = lexLine('const s = "abc', State.Code);
  assert.deepEqual(r1.tokens.map(t => t.type), ['keyword', 'identifier', 'punctuation', 'string']);
  assert.equal(r1.outputState, State.Code, 'unterminated " string must not leak to next line');
  const r2 = lexLine("const t = 'it\\'s ok';", State.Code);
  const str = r2.tokens.find(t => t.type === 'string');
  assert.equal(str.end - str.start, "'it\\'s ok'".length);
});

test('block comment state spans lines and closes mid-line', () => {
  const open = lexLine('let a = 1; /* start', State.Code);
  assert.equal(open.outputState, State.BlockComment);
  const mid = lexLine('still comment', open.outputState);
  assert.deepEqual(mid.tokens, [{ start: 0, end: 13, type: 'comment' }]);
  assert.equal(mid.outputState, State.BlockComment);
  const close = lexLine('end */ let b = 2;', mid.outputState);
  assert.equal(close.outputState, State.Code);
  assert.deepEqual(close.tokens.map(t => t.type),
    ['comment', 'keyword', 'identifier', 'punctuation', 'number', 'punctuation']);
});

test('template literal state spans lines', () => {
  const open = lexLine('const s = `hello', State.Code);
  assert.equal(open.outputState, State.Template);
  const close = lexLine('world`;', open.outputState);
  assert.equal(close.outputState, State.Code);
  assert.deepEqual(close.tokens.map(t => t.type), ['template', 'punctuation']);
});

test('escaped backtick does not close a template', () => {
  const r = lexLine('const s = `a\\`b`;', State.Code);
  assert.equal(r.outputState, State.Code);
  const tpl = r.tokens.find(t => t.type === 'template');
  assert.equal(tpl.end - tpl.start, '`a\\`b`'.length);
});

// ---------- UTF-16 ranges ----------

test('token ranges are UTF-16 code unit offsets', () => {
  // The emoji is one code point but two UTF-16 code units.
  const line = 'const s = "😀"; // 😀';
  const { tokens } = lexLine(line, State.Code);
  const str = tokens.find(t => t.type === 'string');
  assert.equal(line.slice(str.start, str.end), '"😀"');
  assert.equal(str.end - str.start, 4, 'emoji counts as 2 UTF-16 units');
  const comment = tokens.find(t => t.type === 'comment');
  assert.equal(line.slice(comment.start, comment.end), '// 😀');
});

test('absolute UTF-16 ranges slice the document correctly', () => {
  const text = 'const emoji = "😀";\n// 😀 tail\nlet x = 1;';
  const h = new IncrementalHighlighter(text);
  for (const tok of h.getTokens()) {
    assert.equal(text.slice(tok.start, tok.end).length > 0, true);
  }
  const comment = h.getTokens().find(t => t.type === 'comment');
  assert.equal(text.slice(comment.start, comment.end), '// 😀 tail');
});

// ---------- incremental behavior ----------

test('editing a plain code line only re-lexes that line', () => {
  const lines = ['const a = 1;'];
  for (let i = 1; i < 200; i++) lines.push(`const v${i} = ${i};`);
  const h = new IncrementalHighlighter(lines.join('\n'));
  const changed = h.editLine(5, 'let renamed = 42;');
  assert.equal(changed, 1, 'only the edited line changed');
  assert.equal(h.lastRelexedLines, 1);
  assertMatchesFull(h);
});

test('deleting a template backtick propagates to the far end, restoring recovers', () => {
  const original = [
    'const s = `hello',
    'world of code();',
    'const inside = 1;',
    'end`;',
    'const after = 2;',
  ].join('\n');
  const h = new IncrementalHighlighter(original);
  const before = h.getTokens();
  assertMatchesFull(h);

  // Delete the opening backtick: lines 1-3 stop being template text.
  h.editLine(0, 'const s = hello');
  assertMatchesFull(h);
  const afterDelete = h.getTokens();
  assert.notDeepEqual(afterDelete, before);
  const line2 = tokensOnLine(afterDelete, 2);
  assert.deepEqual(line2.map(t => t.type),
    ['keyword', 'identifier', 'punctuation', 'number', 'punctuation'],
    'far-end line is now lexed as code, not template');
  assert.equal(h.getLineState(3).inputState, State.Code);

  // Restore the backtick: everything recovers.
  h.editLine(0, 'const s = `hello');
  assertMatchesFull(h);
  assert.deepEqual(h.getTokens(), before, 'tokens recover after restoring the quote');
});

test('deleting a block comment opener propagates arbitrarily far (no fixed line cap)', () => {
  const body = [];
  for (let i = 0; i < 100; i++) body.push(`const inside${i} = ${i};`);
  const lines = ['const head = 0;', '/* comment start', ...body, 'comment end */', 'const tail = 1;'];
  const h = new IncrementalHighlighter(lines.join('\n'));
  const before = h.getTokens();
  assertMatchesFull(h);
  assert.equal(h.getLineState(50).inputState, State.BlockComment);

  // Delete the '*' of '/*': the opener is gone, 100+ lines change.
  h.editLine(1, '/ comment start');
  assertMatchesFull(h);
  assert.equal(h.lastRelexedLines > 100, true,
    `state change must propagate through all 100+ lines, re-lexed ${h.lastRelexedLines}`);
  const line90 = tokensOnLine(h.getTokens(), 90);
  assert.equal(line90[0].type, 'keyword', 'line 90 is code again after opener removal');
  assert.equal(h.getLineState(102).inputState, State.Code);

  // Restore the opener: full recovery.
  h.editLine(1, '/* comment start');
  assertMatchesFull(h);
  assert.deepEqual(h.getTokens(), before);
});

test('removing the closing */ of a block comment re-lexes to end of document', () => {
  const lines = ['/* a', 'b */', 'const x = 1;', 'const y = 2;'];
  const h = new IncrementalHighlighter(lines.join('\n'));
  h.editLine(1, 'b ');
  assertMatchesFull(h);
  assert.equal(h.getLineState(3).outputState, State.BlockComment);
  assert.deepEqual(tokensOnLine(h.getTokens(), 3).map(t => t.type), ['comment']);
});

test('unterminated double-quoted string does not affect following lines', () => {
  const h = new IncrementalHighlighter('const s = "oops\nconst t = 2;');
  assert.equal(h.getLineState(1).inputState, State.Code);
  const changed = h.editLine(0, 'const s = "fixed";');
  assert.equal(changed, 1);
  assertMatchesFull(h);
});

test('edit that shifts line length keeps downstream tokens stable', () => {
  const lines = ['let a = 1;', 'let bb = 22;', 'let ccc = 333;'];
  const h = new IncrementalHighlighter(lines.join('\n'));
  const changed = h.editLine(0, 'let aMuchLongerName = 1000;');
  assert.equal(changed, 1, 'downstream lines keep identical relative tokens and state');
  assertMatchesFull(h);
  // Absolute ranges shifted correctly.
  const text = h.getText();
  for (const tok of h.getTokens()) {
    const expected = fullHighlight(text).find(t =>
      t.line === tok.line && t.start === tok.start && t.end === tok.end);
    assert.ok(expected, 'absolute UTF-16 range consistent after shift');
  }
});

test('comment and template interact correctly after edits', () => {
  const h = new IncrementalHighlighter([
    'const a = `tpl /* not comment`;',
    '/* comment ` not template */',
    'const b = 2;',
  ].join('\n'));
  assertMatchesFull(h);
  assert.equal(h.getLineState(2).inputState, State.Code);
  // `//` inside a template is template text, not a comment.
  h.editLine(0, 'const a = `tpl // not comment');
  assertMatchesFull(h);
  assert.equal(h.getLineState(1).inputState, State.Template);
  // Line 1 starts as template text (its `/*` is not a comment opener);
  // the backtick in it closes the template and the rest is code again.
  assert.deepEqual(tokensOnLine(h.getTokens(), 1).map(t => t.type),
    ['template', 'identifier', 'identifier', 'punctuation', 'punctuation']);
  assert.equal(h.getLineState(2).inputState, State.Code);
});

// ---------- runner ----------

let failed = 0;
for (const { name, fn } of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failed += 1;
    console.error(`FAIL - ${name}`);
    console.error(err.message);
    if (err.actual !== undefined) {
      console.error('actual:  ', JSON.stringify(err.actual)?.slice(0, 400));
      console.error('expected:', JSON.stringify(err.expected)?.slice(0, 400));
    }
  }
}
console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
process.exit(failed === 0 ? 0 : 1);
