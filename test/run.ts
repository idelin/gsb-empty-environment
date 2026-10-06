import * as assert from 'assert';
import { IncrementalHighlighter } from '../src/highlighter';
import { equalStates } from '../src/state';
import { Token } from '../src/tokenizer';

let failures = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failures++;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}

/** Assert the incremental highlighter agrees with a from-scratch full highlight. */
function assertMatchesFullHighlight(inc: IncrementalHighlighter): void {
  const full = new IncrementalHighlighter(inc.getText());
  assert.deepStrictEqual(inc.getTokens(), full.getTokens(), 'token stream differs from full highlight');
  assert.strictEqual(inc.lineCount, full.lineCount, 'line count differs');
  for (let i = 0; i < inc.lineCount; i++) {
    assert.ok(
      equalStates(inc.getLineState(i), full.getLineState(i)),
      `line ${i} input state differs from full highlight`,
    );
  }
}

function tokenTypes(tokens: Token[]): string[] {
  return tokens.map((t) => t.type);
}

// ---------------------------------------------------------------- basic lexing

test('tokenizes keywords, identifiers, numbers, strings, comments', () => {
  const h = new IncrementalHighlighter(`const x = 42; // answer\nlet s = "hi";`);
  assert.deepStrictEqual(
    tokenTypes(h.getLineTokens(0)),
    ['keyword', 'identifier', 'operator', 'number', 'punctuation', 'comment'],
  );
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(1)), ['keyword', 'identifier', 'operator', 'string', 'punctuation']);
});

test('block comment state carries across lines', () => {
  const h = new IncrementalHighlighter('/* a\nb\nc */ let x');
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(1)), ['comment']);
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(2)), ['comment', 'keyword', 'identifier']);
  assert.strictEqual(h.getLineState(1).contexts[0].type, 'blockComment');
  assert.strictEqual(h.getLineState(2).contexts[0].type, 'blockComment');
});

test('template literal spans lines and interpolates expressions', () => {
  const h = new IncrementalHighlighter('const s = `hello\n${name.toUpperCase()} world\nend`;');
  assert.strictEqual(h.getLineState(1).contexts[0].type, 'template');
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(1)), ['punctuation', 'identifier', 'punctuation', 'identifier', 'punctuation', 'punctuation', 'punctuation', 'string']);
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(2)), ['string', 'punctuation']);
});

test('nested template literal inside interpolation', () => {
  const h = new IncrementalHighlighter('const s = `a ${ `b ${x} c` } d`;');
  assertMatchesFullHighlight(h);
  const types = tokenTypes(h.getLineTokens(0));
  assert.strictEqual(types[0], 'keyword');
  assert.ok(types.filter((t) => t === 'string').length >= 4);
});

test('backslash line-continuation keeps string state on next line', () => {
  const h = new IncrementalHighlighter('const s = "abc\\\ndef";\nlet y = 1;');
  const st = h.getLineState(1);
  assert.strictEqual(st.contexts[0].type, 'continuedString');
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(1)), ['string', 'punctuation']);
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(2)), ['keyword', 'identifier', 'operator', 'number', 'punctuation']);
});

// ---------------------------------------------------------------- UTF-16 ranges

test('ranges are UTF-16 code-unit offsets (astral chars count as 2)', () => {
  const text = 'let \u{1F600}x = "\u{1F600}"; // tail';
  const h = new IncrementalHighlighter(text);
  const tokens = h.getTokens();
  // "let " = 4, emoji = 2 UTF-16 units; emoji+x lex as one identifier [4, 7)
  assert.deepStrictEqual(tokens[1], { start: 4, end: 7, type: 'identifier' });
  // string starts after "x = " -> 7 + 3 = 10, emoji inside counts 2
  assert.deepStrictEqual(tokens[3], { start: 10, end: 14, type: 'string' });
  // comment token: absolute offset of "// tail"
  const commentStart = text.indexOf('//');
  const last = tokens[tokens.length - 1];
  assert.deepStrictEqual(last, { start: commentStart, end: text.length, type: 'comment' });
  // sanity: offsets really are JS string indices
  assert.strictEqual(text.slice(4, 7), '\u{1F600}x');
  assert.strictEqual(text.slice(10, 14), '"\u{1F600}"');
});

test('absolute offsets account for emoji on earlier lines', () => {
  const text = '\u{1F600}\u{1F600}\nlet x = 1;';
  const h = new IncrementalHighlighter(text);
  const tokens = h.getTokens();
  // line 0: two emoji = 4 UTF-16 units + '\n' => line 1 starts at 5
  assert.deepStrictEqual(tokens[1], { start: 5, end: 8, type: 'keyword' });
});

// ---------------------------------------------------------------- incremental behaviour

test('edit without state change retokenizes exactly one line', () => {
  const body = Array.from({ length: 50 }, (_, i) => `let v${i} = ${i};`).join('\n');
  const h = new IncrementalHighlighter(body);
  const offset = body.indexOf('v10');
  h.applyEdit({ start: offset, end: offset + 3, text: 'renamed' });
  assert.strictEqual(h.lastRetokenized, 1);
  assertMatchesFullHighlight(h);
});

test('deleting a template backtick propagates to EOF (no fixed-line truncation)', () => {
  const inner = Array.from({ length: 500 }, (_, i) => `padding line ${i} with words`).join('\n');
  const text = `const s = \`\n${inner}\n\`;\nlet after = 1;`;
  const h = new IncrementalHighlighter(text);
  const before = h.getTokens();

  const backtick = text.indexOf('`');
  h.applyEdit({ start: backtick, end: backtick + 1, text: '' });

  // Every one of the 500+ lines must be re-examined; no fixed window cut-off.
  assert.ok(h.lastRetokenized >= 500, `expected >=500 retokenized lines, got ${h.lastRetokenized}`);
  assertMatchesFullHighlight(h);

  // The far-away line really did change token types (string -> code).
  const farLine = h.getLineTokens(400);
  assert.deepStrictEqual(tokenTypes(farLine), ['identifier', 'identifier', 'number', 'identifier', 'identifier']);

  // Restoring the backtick recovers the exact original token stream.
  h.applyEdit({ start: backtick, end: backtick, text: '`' });
  assert.deepStrictEqual(h.getTokens(), before);
  assertMatchesFullHighlight(h);
});

test('deleting the opening quote of a continued string shifts far lines and recovers', () => {
  const text = 'const s = "abc\\\ndef\\\nghi";\nlet x = 1;\nlet y = 2;';
  const h = new IncrementalHighlighter(text);
  const before = h.getTokens();

  const quote = text.indexOf('"');
  h.applyEdit({ start: quote, end: quote + 1, text: '' });
  assertMatchesFullHighlight(h);
  assert.notDeepStrictEqual(h.getTokens(), before);

  h.applyEdit({ start: quote, end: quote, text: '"' });
  assert.deepStrictEqual(h.getTokens(), before);
  assertMatchesFullHighlight(h);
});

test('deleting a block-comment closer extends the comment to EOF', () => {
  const text = 'let a = 1;\n/* note */\nlet b = 2;\nlet c = 3;';
  const h = new IncrementalHighlighter(text);
  const before = h.getTokens();

  const close = text.indexOf('*/');
  h.applyEdit({ start: close, end: close + 2, text: '' });
  assert.strictEqual(h.lastRetokenized, 3); // lines 1..3, stops when stable... actually runs to EOF
  assertMatchesFullHighlight(h);
  assert.deepStrictEqual(tokenTypes(h.getLineTokens(3)), ['comment']);

  h.applyEdit({ start: close, end: close, text: '*/' });
  assert.deepStrictEqual(h.getTokens(), before);
  assertMatchesFullHighlight(h);
});

test('deleting a line-comment opener un-comments the line only', () => {
  const text = 'let a = 1;\n// let b = 2;\nlet c = 3;';
  const h = new IncrementalHighlighter(text);
  h.applyEdit({ start: text.indexOf('//'), end: text.indexOf('//') + 2, text: '' });
  assert.strictEqual(h.lastRetokenized, 1);
  assertMatchesFullHighlight(h);
});

test('edits that insert and remove whole lines keep line table consistent', () => {
  const text = 'let a = 1;\nlet b = 2;';
  const h = new IncrementalHighlighter(text);
  const insertAt = text.indexOf('let b');
  h.applyEdit({ start: insertAt, end: insertAt, text: '/* open\nmiddle\nclose */\n' });
  assert.strictEqual(h.lineCount, 5);
  assertMatchesFullHighlight(h);
  h.applyEdit({ start: insertAt, end: insertAt + '/* open\nmiddle\nclose */\n'.length, text: '' });
  assert.strictEqual(h.lineCount, 2);
  assertMatchesFullHighlight(h);
});

test('edit spanning multiple lines (delete across a template boundary)', () => {
  const text = 'const s = `one\ntwo\nthree`;\nlet z = 9;';
  const h = new IncrementalHighlighter(text);
  const from = text.indexOf('`one');
  const to = text.indexOf('three`') + 'three`'.length;
  h.applyEdit({ start: from, end: to, text: '"plain"' });
  assert.strictEqual(h.lineCount, 2);
  assertMatchesFullHighlight(h);
});

// ---------------------------------------------------------------- fuzz vs full highlight

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const FRAGMENTS = [
  'let x = 1;\n', 'const s = "str";\n', '// note\n', '/* block\ncomment */\n',
  'const t = `tpl\n${x + 1}\nend`;\n', 'if (x > 2) { x++; }\n', 'var q = "a\\\nb";\n',
  '\u{1F600}var emoji = 0;\n', 'unterminated = "oops\n', '/* never closed\n',
];

test('fuzz: random edits always match a full re-highlight', () => {
  const rand = mulberry32(20261006);
  let text = '';
  for (let i = 0; i < 40; i++) {
    text += FRAGMENTS[Math.floor(rand() * FRAGMENTS.length)];
  }
  const h = new IncrementalHighlighter(text);
  assertMatchesFullHighlight(h);

  for (let iter = 0; iter < 300; iter++) {
    const doc = h.getText();
    const start = Math.floor(rand() * (doc.length + 1));
    const end = start + Math.floor(rand() * (doc.length - start + 1));
    let inserted = '';
    const pieces = Math.floor(rand() * 3);
    for (let p = 0; p < pieces; p++) {
      inserted += FRAGMENTS[Math.floor(rand() * FRAGMENTS.length)];
    }
    h.applyEdit({ start, end, text: inserted });
    assertMatchesFullHighlight(h);
  }
});

if (failures > 0) {
  console.error(`\n${failures} test(s) failed`);
  process.exit(1);
}
console.log('\nall tests passed');
