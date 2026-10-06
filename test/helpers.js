'use strict';

const assert = require('assert');
const { Lexer, IncrementalLexer } = require('../src');

/** One-shot lex of a whole string. */
function lexAll(text) {
  const lexer = new Lexer();
  lexer.feed(text);
  lexer.end();
  return { tokens: lexer.tokens, diagnostics: lexer.diagnostics };
}

function assertSameLex(actual, expected, what) {
  assert.deepStrictEqual(actual.tokens, expected.tokens, `${what}: tokens differ`);
  assert.deepStrictEqual(actual.diagnostics, expected.diagnostics, `${what}: diagnostics differ`);
}

/** Compare an IncrementalLexer against a fresh one-shot lex of its text. */
function assertConsistent(inc, what) {
  assertSameLex({ tokens: inc.tokens, diagnostics: inc.diagnostics }, lexAll(inc.text), what);
  // tokens must tile the whole document
  let pos = 0;
  for (const t of inc.tokens) {
    assert.strictEqual(t.start, pos, `${what}: gap before token at ${t.start}`);
    assert.strictEqual(t.text, inc.text.slice(t.start, t.end), `${what}: token text mismatch`);
    pos = t.end;
  }
  assert.strictEqual(pos, inc.text.length, `${what}: tokens do not cover input`);
}

/** Deterministic PRNG (mulberry32). */
function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

module.exports = { lexAll, assertSameLex, assertConsistent, rng, IncrementalLexer, Lexer, assert };
