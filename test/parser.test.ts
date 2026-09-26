import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseSelector, specificityOf } from '../src/css/selector.ts';
import { parseStylesheet } from '../src/css/parser.ts';
import { parseValueTokens, tokensToString } from '../src/css/values.ts';

test('selector parsing: compound, combinators, pseudo-classes', () => {
  const sel = parseSelector('div.card > p.desc:hover span');
  assert.equal(sel.parts.length, 3);
  assert.equal(sel.parts[1].combinator, '>');
  assert.equal(sel.parts[2].combinator, ' ');
  assert.deepEqual(specificityOf(sel), [0, 3, 3]);
});

test('specificity ordering: id > class/state > type', () => {
  assert.deepEqual(specificityOf(parseSelector('#a')), [1, 0, 0]);
  assert.deepEqual(specificityOf(parseSelector('.a.b:hover')), [0, 3, 0]);
  assert.deepEqual(specificityOf(parseSelector('div p')), [0, 0, 2]);
  assert.deepEqual(specificityOf(parseSelector('*')), [0, 0, 0]);
  assert.deepEqual(specificityOf(parseSelector('li:first-child')), [0, 1, 1]);
});

test('stylesheet parsing: comma selectors, custom properties, comments', () => {
  const rules = parseStylesheet(`
    /* a comment */
    .a, #b { color: red; --gap: 8; }
    div > p { margin: 0; }
  `);
  assert.equal(rules.length, 2);
  assert.equal(rules[0].selectors.length, 2);
  assert.deepEqual(rules[0].declarations.map((d) => d.prop), ['color', '--gap']);
  assert.equal(rules[0].id, 0);
  assert.equal(rules[1].id, 1);
});

test('value tokenization: var() with nested fallback chains', () => {
  const tokens = parseValueTokens('var(--a, var(--b, 12px)) solid');
  assert.equal(tokens.length, 2);
  const v = tokens[0];
  assert.equal(v.kind, 'var');
  if (v.kind === 'var') {
    assert.equal(v.name, '--a');
    assert.ok(v.fallback);
    assert.equal(tokensToString(v.fallback), ' var(--b, 12px)');
  }
  assert.equal(tokensToString(tokens), 'var(--a, var(--b, 12px)) solid');
});

test('value tokenization: parentheses inside literals do not break parsing', () => {
  const tokens = parseValueTokens('rgb(1, 2, 3) var(--x, url(a(b).png))');
  assert.equal(tokens[0].kind, 'text');
  assert.equal(tokens[1].kind, 'var');
  if (tokens[1].kind === 'var') assert.equal(tokensToString(tokens[1].fallback!), ' url(a(b).png)');
});

test('unknown properties are dropped, custom properties kept case-sensitively', () => {
  const rules = parseStylesheet('.a { colr: red; --Case: 1; color: blue; }');
  assert.deepEqual(rules[0].declarations.map((d) => d.prop), ['--Case', 'color']);
});
