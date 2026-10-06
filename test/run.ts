import { ChunkLexer, lexAll } from '../src/chunkLexer';
import { IncrementalDocument } from '../src/incremental';
import { Token, Unterminated } from '../src/scanner';

declare const process: { exit(code: number): void };

let failures = 0;
let checks = 0;

function eq(actual: unknown, expected: unknown, label: string): void {
  checks++;
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) {
    failures++;
    console.error(`FAIL ${label}\n  expected: ${e}\n  actual:   ${a}`);
  }
}

function ok(cond: boolean, label: string): void {
  checks++;
  if (!cond) {
    failures++;
    console.error(`FAIL ${label}`);
  }
}

function lexViaChunks(chunks: string[]): { tokens: Token[]; unterminated: Unterminated[] } {
  const lexer = new ChunkLexer();
  for (const c of chunks) lexer.feed(c);
  const unterminated = lexer.finish();
  return { tokens: lexer.tokens, unterminated };
}

function* splits2(s: string): Generator<string[]> {
  for (let i = 1; i < s.length; i++) yield [s.slice(0, i), s.slice(i)];
}

function* splits3(s: string): Generator<string[]> {
  for (let i = 1; i < s.length - 1; i++)
    for (let j = i + 1; j < s.length; j++)
      yield [s.slice(0, i), s.slice(i, j), s.slice(j)];
}

// mulberry32 seeded PRNG
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------------------------------------------------------------- basics

{
  const { tokens, unterminated } = lexAll('a/*b*/c');
  eq(tokens, [
    { kind: 'code', start: 0, end: 1 },
    { kind: 'blockComment', start: 1, end: 6 },
    { kind: 'code', start: 6, end: 7 },
  ], 'basic block comment');
  eq(unterminated, [], 'basic: no unterminated');
}

{
  // Comment markers inside a string are inert; escapes hide quotes.
  const { tokens, unterminated } = lexAll('"/*" "//" x"\\""y');
  eq(tokens, [
    { kind: 'string', start: 0, end: 4 },
    { kind: 'code', start: 4, end: 5 },
    { kind: 'string', start: 5, end: 9 },
    { kind: 'code', start: 9, end: 11 },
    { kind: 'string', start: 11, end: 15 },
    { kind: 'code', start: 15, end: 16 },
  ], 'comment markers inside strings are inert');
  eq(unterminated, [], 'strings: no unterminated');
}

{
  // Quotes inside comments are inert; nested block comments.
  const { tokens } = lexAll('/* " /* // */ " */x');
  eq(tokens, [
    { kind: 'blockComment', start: 0, end: 18 },
    { kind: 'code', start: 18, end: 19 },
  ], 'nested block comment with inert quotes');
}

{
  const { tokens } = lexAll('// a " /* \nb');
  eq(tokens, [
    { kind: 'lineComment', start: 0, end: 11 },
    { kind: 'code', start: 11, end: 12 },
  ], 'line comment ends at newline, markers inert inside');
}

{
  // UTF-16 offsets: an emoji is two code units.
  const { tokens } = lexAll('😀"a"');
  eq(tokens, [
    { kind: 'code', start: 0, end: 2 },
    { kind: 'string', start: 2, end: 5 },
  ], 'UTF-16 absolute offsets across astral chars');
}

{
  // Unterminated string and nested unterminated comments report starts.
  const r1 = lexAll('ab"cd');
  eq(r1.unterminated, [{ kind: 'string', start: 2 }], 'unterminated string start');
  const r2 = lexAll('x/*a/*b*/c/*d');
  eq(r2.unterminated, [
    { kind: 'blockComment', start: 1 },
    { kind: 'blockComment', start: 10 },
  ], 'unterminated nested comment starts');
}

// --------------------------------------- chunk-split enumeration property

const tricky = [
  'a/*b*/c',
  '"/*"',
  '"//"',
  '// " /*\nx',
  '/* /* */ */',
  '"esc\\""',
  'a/b/*c*/d//e\nf"g"',
  '//*',
  '**/',
  '/*/',
  '"\\\\"',
  '😀"😀"/*😀*/',
  'x/*"y"*/"/*z*/"',
  '/* unterminated',
  '"unterminated',
  'a/*b/*c*/d*/e"f\\"g"//h\ni',
];

for (const s of tricky) {
  const oneShot = lexAll(s);
  for (const chunks of splits2(s)) {
    eq(lexViaChunks(chunks), oneShot, `2-split ${JSON.stringify(s)} @${chunks[0].length}`);
  }
  if (s.length <= 18) {
    for (const chunks of splits3(s)) {
      eq(lexViaChunks(chunks), oneShot,
        `3-split ${JSON.stringify(s)} @${chunks[0].length},${chunks[0].length + chunks[1].length}`);
    }
  }
}

// Exhaustive: every string over a marker-rich alphabet up to length 4,
// every 2-chunk split, must equal the one-shot result.
{
  const alphabet = ['a', '"', '/', '*', '\\', '\n'];
  const gen = (prefix: string, depth: number): void => {
    const oneShot = lexAll(prefix);
    for (const chunks of splits2(prefix)) {
      eq(lexViaChunks(chunks), oneShot, `exhaustive ${JSON.stringify(prefix)}`);
    }
    if (depth > 0) for (const c of alphabet) gen(prefix + c, depth - 1);
  };
  gen('', 4);
}

// ------------------------------------------------------- pause / resume

{
  const samples = tricky.concat(['', 'a', '/', '"', '/*', '/*x*/']);
  for (const s of samples) {
    const oneShot = lexAll(s);
    for (let cut = 0; cut <= s.length; cut++) {
      const lexer = new ChunkLexer();
      lexer.feed(s.slice(0, cut));
      // Pause: checkpoint is plain JSON-serializable data.
      const cp = JSON.parse(JSON.stringify(lexer.checkpoint()));
      const resumed = ChunkLexer.resume(cp);
      resumed.feed(s.slice(cut));
      const unterminated = resumed.finish();
      // Tokens completed before the pause stay with the first lexer; the
      // resumed lexer emits the rest. Together they equal the one-shot run.
      eq({ tokens: lexer.tokens.concat(resumed.tokens), unterminated }, oneShot,
        `pause/resume ${JSON.stringify(s)} @${cut}`);
    }
  }
}

// Pause mid-string and mid-nested-comment, resume in a fresh process state.
{
  const s = 'pre "str\\" still" mid /* l1 /* l2 */ done */ post';
  for (const cut of [8, 12, 30, 36, 44]) {
    const lexer = new ChunkLexer();
    lexer.feed(s.slice(0, cut));
    const cp = lexer.checkpoint();
    ok(cp.offset === cut, `checkpoint offset ${cut}`);
    const resumed = ChunkLexer.resume(cp);
    resumed.feed(s.slice(cut));
    const unterminated = resumed.finish();
    eq({ tokens: lexer.tokens.concat(resumed.tokens), unterminated }, lexAll(s),
      `mid-construct resume @${cut}`);
  }
}

// ---------------------------------------------------- incremental editing

function fresh(text: string): { tokens: Token[]; unterminated: Unterminated[] } {
  return lexAll(text);
}

function checkDoc(doc: IncrementalDocument, label: string): void {
  const expected = fresh(doc.getText());
  eq(doc.getTokens(), expected.tokens, `${label}: tokens`);
  eq(doc.getDiagnostics(), expected.unterminated, `${label}: diagnostics`);
  // Tokens must tile the whole text.
  const toks = doc.getTokens();
  let pos = 0;
  for (const t of toks) {
    ok(t.start === pos, `${label}: tiling at ${pos}`);
    pos = t.end;
  }
  ok(pos === doc.getText().length, `${label}: tiling covers end`);
}

// Remote comment-state propagation, one character at a time: typing `/*`
// at the top must re-lex code far below into a comment, and deleting it
// char by char must restore it.
{
  const body = 'let a = "x";\nlet b = /* c */ 2;\nlet c = "/*";\n';
  const doc = new IncrementalDocument(body);
  checkDoc(doc, 'remote: initial');

  doc.applyEdit({ start: 0, end: 0, newText: '/' });
  checkDoc(doc, 'remote: typed /');
  doc.applyEdit({ start: 1, end: 1, newText: '*' });
  checkDoc(doc, 'remote: typed /* (whole body now commented)');
  eq(doc.getTokens(), [{ kind: 'blockComment', start: 0, end: body.length + 2 }],
    'remote: single unterminated comment token');
  // Nested comments: the `/*` inside the string literal nests too, so both
  // the outer opener at 0 and the nested one at 43 stay unclosed.
  eq(doc.getDiagnostics(), [
    { kind: 'blockComment', start: 0 },
    { kind: 'blockComment', start: 43 },
  ], 'remote: unterminated nested openers');

  doc.applyEdit({ start: 1, end: 2, newText: '' });
  checkDoc(doc, 'remote: deleted *');
  doc.applyEdit({ start: 0, end: 1, newText: '' });
  checkDoc(doc, 'remote: deleted / (back to original)');
  eq(doc.getTokens(), fresh(body).tokens, 'remote: original token stream restored');
}

// A single-char deletion far upstream flips comment state downstream.
{
  const text = '/* a */ code1 /* b */ code2 "str" /* c';
  const doc = new IncrementalDocument(text);
  checkDoc(doc, 'prop: initial');
  // Delete the '/' of the very first '/*': everything downstream re-lexes.
  doc.applyEdit({ start: 0, end: 1, newText: '' });
  checkDoc(doc, 'prop: deleted opening slash');
  // Delete one char of the closing '*/' of the first comment instead.
  const doc2 = new IncrementalDocument(text);
  doc2.applyEdit({ start: 6, end: 7, newText: '' });
  checkDoc(doc2, 'prop: deleted closing star');
}

// Old-version coordinates: sequential edits each refer to the version they
// are applied to, never to the original.
{
  const doc = new IncrementalDocument('ab');
  doc.applyEdit({ start: 1, end: 1, newText: 'XY' }); // aXYb
  doc.applyEdit({ start: 2, end: 3, newText: '"/*"' }); // aX"/*"b
  checkDoc(doc, 'old-coords: sequential edits');
  eq(doc.getText(), 'aX"/*"b', 'old-coords: text');
}

// Rescan stops at the first stable token boundary: an edit near the start
// of a large stable tail must not re-scan to end of input.
{
  const tail = 'x ". " /* n /* n */ n */ y // z\n'.repeat(200);
  const text = 'let a = 1;\n' + tail;
  const doc = new IncrementalDocument(text);
  doc.applyEdit({ start: 5, end: 6, newText: 'b' }); // rename a -> b
  checkDoc(doc, 'stable tail: tokens');
  ok(doc.getLastRescanChars() < 100,
    `stable tail: rescan stopped early (${doc.getLastRescanChars()} chars, tail is ${tail.length})`);
}

// An edit that opens an unterminated comment at the very start must rescan
// to EOF (no stable point exists), proving rescan length is state-driven.
{
  const tail = 'code "str" /* nested */ more\n'.repeat(100);
  const doc = new IncrementalDocument(tail);
  doc.applyEdit({ start: 0, end: 0, newText: '/*' });
  checkDoc(doc, 'unstable tail: tokens');
  ok(doc.getLastRescanChars() >= tail.length,
    `unstable tail: rescanned to EOF (${doc.getLastRescanChars()} chars)`);
}

// Randomized fuzz: random texts, random small edits in old coordinates,
// always compared against a full re-lex.
{
  const alphabet = ['a', 'b', '"', '\\', '/', '*', '\n', '😀', ';'];
  for (let seed = 1; seed <= 30; seed++) {
    const rand = rng(seed);
    const pick = () => alphabet[Math.floor(rand() * alphabet.length)];
    let text = '';
    for (let i = 0; i < 60; i++) text += pick();
    const doc = new IncrementalDocument(text);
    checkDoc(doc, `fuzz seed=${seed} initial`);
    for (let step = 0; step < 60; step++) {
      const len = doc.getText().length;
      const start = Math.floor(rand() * (len + 1));
      const end = start + Math.floor(rand() * (len - start + 1));
      let newText = '';
      const n = Math.floor(rand() * 4);
      for (let k = 0; k < n; k++) newText += pick();
      doc.applyEdit({ start, end, newText });
      checkDoc(doc, `fuzz seed=${seed} step=${step} [${start},${end})+${JSON.stringify(newText)}`);
    }
  }
}

// Per-character deletion sweep: for a marker-dense text, delete every
// single character in turn (each from the original text) and verify.
{
  const text = 'a/*b"c"*/d"/*e*/"f//g\nh/*i/*j*/k*/l';
  for (let i = 0; i < text.length; i++) {
    const doc = new IncrementalDocument(text);
    doc.applyEdit({ start: i, end: i + 1, newText: '' });
    checkDoc(doc, `sweep delete @${i}`);
  }
  // Per-character insertion sweep of each marker character.
  for (const ch of ['/', '*', '"', '\\']) {
    for (let i = 0; i <= text.length; i++) {
      const doc = new IncrementalDocument(text);
      doc.applyEdit({ start: i, end: i, newText: ch });
      checkDoc(doc, `sweep insert ${JSON.stringify(ch)} @${i}`);
    }
  }
}

// ---------------------------------------------------------------- summary

console.log(`${checks} checks, ${failures} failures`);
if (failures > 0) process.exit(1);
console.log('ALL TESTS PASSED');
