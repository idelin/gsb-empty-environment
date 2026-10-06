import assert from 'node:assert/strict';
import { parseStatements, innermostStatementAt } from '../src/statements.js';

const kinds = (src) => parseStatements(src).filter((s) => s.depth === 0).map((s) => s.kind);

test('parser: splits semicolon-terminated statements', () => {
  assert.deepEqual(kinds('const a = 1;\nlet b = 2;\nfoo();'), ['var', 'var', 'expression']);
});

test('parser: ASI splits newline-terminated statements', () => {
  assert.deepEqual(kinds('const a = 1\nconst b = 2\nfoo()\n'), ['var', 'var', 'expression']);
});

test('parser: no ASI across member/call/operator continuations', () => {
  assert.deepEqual(kinds('foo()\n  .bar()\n  .baz();'), ['expression']);
  assert.deepEqual(kinds('const x = a +\n  b;'), ['var']);
  assert.deepEqual(kinds('const y = cond\n  ? 1\n  : 2;'), ['var']);
});

test('parser: block-bodied statements stay whole', () => {
  assert.deepEqual(
    kinds('if (a) { b(); } else { c(); }\nwhile (x) { y(); }\ndo { z(); } while (ok);'),
    ['if', 'while', 'do'],
  );
});

test('parser: object literals do not split statements', () => {
  assert.deepEqual(kinds('const o = { a: 1, b: { c: 2 } };\nconst p = 3;'), ['var', 'var']);
});

test('parser: nested statements are discovered with parents', () => {
  const src = 'function f() {\n  if (x) {\n    return 1;\n  }\n}\n';
  const ss = parseStatements(src);
  assert.equal(ss.length, 3);
  const [fn, ifStmt, ret] = ss;
  assert.equal(fn.kind, 'function');
  assert.equal(ifStmt.parent, 0);
  assert.equal(ret.parent, 1);
  assert.equal(ret.depth, 2);
});

test('parser: template literals and regex stay inside one statement', () => {
  const src = 'const re = /a{b}\\n/g;\nconst t = `x${1 + 2}\ny`;\nconst z = 3;';
  assert.deepEqual(kinds(src), ['var', 'var', 'var']);
});

test('parser: innermost lookup prefers the smallest span', () => {
  const src = 'function f() {\n  return 1;\n}\n';
  const ss = parseStatements(src);
  const offset = src.indexOf('return') + 2;
  const idx = innermostStatementAt(ss, offset);
  assert.equal(ss[idx].kind, 'return');
});

test('parser: content key ignores whitespace and comments', () => {
  const a = parseStatements('const a = 1; // note\n')[0];
  const b = parseStatements('const  a=1;')[0];
  const c = parseStatements('const a = 2;')[0];
  assert.equal(a.key, b.key);
  assert.notEqual(a.key, c.key);
});
