import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/jsonpath/compiler.ts';
import { evaluate } from '../src/jsonpath/interpreter.ts';
import { ParseError } from '../src/jsonpath/errors.ts';
import type { JsonValue } from '../src/jsonpath/ast.ts';

test('谓词编译不经过宿主 eval：注入式查询串只是语法错误', () => {
  const hostile = [
    '$[?(process.exit(1))]',
    '$[?(@.x == globalThis.process)]',
    `$.a";process.exit(1);//`,
    '$[?(constructor.constructor("return 1")())]',
    '$[?(@.x == eval("1"))]',
  ];
  for (const q of hostile) assert.throws(() => compile(q), ParseError, q);
});

test('恶意键名只是数据：__proto__ 等键可查询但不污染原型', () => {
  const doc = JSON.parse('{"__proto__": {"polluted": true}, "a": 1}') as JsonValue;
  const res = evaluate(doc, compile(`$['__proto__'].polluted`));
  assert.equal(res.length, 1);
  assert.equal(({} as Record<string, unknown>).polluted, undefined);
});

test('函数字面量无法作为操作数注入', () => {
  assert.throws(() => compile('$[?(@.x == (()=>1)())]'), ParseError);
  assert.throws(() => compile('$[?(@.x == `template`)]'), ParseError);
});
