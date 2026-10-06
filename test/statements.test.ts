import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extractStatements } from '../src/index.ts';

test('extracts simple statements and ignores comments/strings/templates', () => {
  const source = [
    'const s = "a;b";',
    '// comment with ; and } {',
    'const t = `x ${y} z`;',
    'const r = /a}b/;',
    'foo();',
  ].join('\n');
  const stmts = extractStatements(source);
  assert.equal(stmts.length, 4);
  assert.equal(stmts[0].startLine, 1);
  assert.equal(stmts[1].startLine, 3);
  assert.equal(stmts[2].startLine, 4);
  assert.equal(stmts[3].startLine, 5);
  assert.ok(stmts[0].key.includes('"a;b"'));
  assert.ok(stmts[1].key.includes('`x ${y} z`'));
});

test('extracts compound statements with stable header keys plus inner statements', () => {
  const source = [
    'if (a > 0) {',
    '  doThing();',
    '} else {',
    '  doOther();',
    '}',
  ].join('\n');
  const stmts = extractStatements(source);
  const keys = stmts.map((s) => s.key);
  assert.deepEqual(keys, [
    '##block:if ( a > 0 )',
    'doThing ( ) ;',
    '##block:else',
    'doOther ( ) ;',
  ]);
  const compound = stmts[0];
  assert.equal(compound.compound, true);
  assert.equal(compound.startLine, 1);
  assert.equal(compound.endLine, 3);
});

test('statement keys are position-independent (move keeps identity key)', () => {
  const a = extractStatements('foo();\nbar();');
  const b = extractStatements('bar();\nfoo();');
  assert.equal(a[0].key, b[1].key);
  assert.equal(a[1].key, b[0].key);
});

test('object literals and destructuring do not split statements', () => {
  const source = [
    'const o = { a: 1, b: 2 };',
    'const { x, y } = make();',
    'use(o, x, y);',
  ].join('\n');
  const stmts = extractStatements(source);
  assert.equal(stmts.length, 3);
  assert.equal(stmts[1].startLine, 2);
});
