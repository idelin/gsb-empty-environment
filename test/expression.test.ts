import assert from 'node:assert/strict';
import test from 'node:test';
import {
  collectKeys,
  evaluateExpression,
  parseExpression,
  strictResolver,
} from '../src/index.ts';
import { ExpressionSyntaxError, UnknownContextKeyError } from '../src/index.ts';

const ctx = (entries: Record<string, boolean>) => strictResolver(new Map(Object.entries(entries)));

test('字面量与引用求值', () => {
  assert.equal(evaluateExpression(parseExpression('true'), ctx({})), true);
  assert.equal(evaluateExpression(parseExpression('false'), ctx({})), false);
  assert.equal(evaluateExpression(parseExpression('editorFocus'), ctx({ editorFocus: true })), true);
});

test('运算符优先级：! > && > ||', () => {
  const resolve = ctx({ a: false, b: true, c: true });
  assert.equal(evaluateExpression(parseExpression('a || b && c'), resolve), true);
  assert.equal(evaluateExpression(parseExpression('!a && b'), resolve), true);
  assert.equal(evaluateExpression(parseExpression('!(a || b)'), resolve), false);
});

test('括号改变结合顺序', () => {
  const resolve = ctx({ a: true, b: false, c: false });
  assert.equal(evaluateExpression(parseExpression('(a || b) && c'), resolve), false);
  assert.equal(evaluateExpression(parseExpression('a || b && c'), resolve), true);
});

test('短路求值不触碰未知键', () => {
  assert.equal(evaluateExpression(parseExpression('false && missing'), ctx({})), false);
  assert.equal(evaluateExpression(parseExpression('true || missing'), ctx({})), true);
});

test('未知键抛出 UnknownContextKeyError', () => {
  assert.throws(
    () => evaluateExpression(parseExpression('known && missing'), ctx({ known: true })),
    (error: unknown) => error instanceof UnknownContextKeyError && error.key === 'missing',
  );
});

test('语法错误：空表达式、悬空运算符、未闭合括号、尾部多余 token、非法字符', () => {
  for (const source of ['', '   ', 'a &&', '&& a', '(a', 'a b', 'a &&& b', 'a @ b', '!']) {
    assert.throws(() => parseExpression(source), ExpressionSyntaxError, source);
  }
});

test('collectKeys 汇总表达式引用的上下文键', () => {
  const keys = collectKeys(parseExpression('editor.focus && !readonly || mode.insert'));
  assert.deepEqual([...keys].sort(), ['editor.focus', 'mode.insert', 'readonly']);
});
