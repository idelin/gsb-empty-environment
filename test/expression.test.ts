import assert from 'node:assert/strict';
import test from 'node:test';
import {
  ConditionParseError,
  evaluateCondition,
  parseCondition,
} from '../src/expression.ts';

const evalWith = (source: string, ctx: Record<string, unknown>): boolean =>
  evaluateCondition(parseCondition(source), (key) => ctx[key]);

test('标识符按真值求值', () => {
  assert.equal(evalWith('dirty', { dirty: true }), true);
  assert.equal(evalWith('dirty', { dirty: 0 }), false);
  assert.equal(evalWith('missing', {}), false);
});

test('布尔字面量', () => {
  assert.equal(evalWith('true', {}), true);
  assert.equal(evalWith('false', {}), false);
  assert.equal(evalWith('!false', {}), true);
});

test('运算符优先级：! > == > && > ||', () => {
  assert.equal(evalWith('a || b && c', { a: true, b: false, c: false }), true);
  assert.equal(evalWith('a || b && c', { a: false, b: true, c: false }), false);
  assert.equal(evalWith('!a && b', { a: false, b: true }), true);
  assert.equal(evalWith('!(a && b)', { a: true, b: true }), false);
  assert.equal(evalWith('!(a == true)', { a: false }), true);
});

test('括号改变结合顺序', () => {
  assert.equal(evalWith('(a || b) && c', { a: true, b: false, c: false }), false);
});

test('相等与不等比较', () => {
  const ctx = { focus: 'editor', count: 3, readonly: false };
  assert.equal(evalWith("focus == 'editor'", ctx), true);
  assert.equal(evalWith("focus == 'terminal'", ctx), false);
  assert.equal(evalWith("focus != 'terminal'", ctx), true);
  assert.equal(evalWith('count == 3', ctx), true);
  assert.equal(evalWith('count != 3', ctx), false);
  assert.equal(evalWith('readonly == false', ctx), true);
  assert.equal(evalWith('missing == 1', ctx), false);
});

test('点号键名（focus.editor）', () => {
  assert.equal(evalWith('focus.editor && !readonly', { 'focus.editor': true, readonly: false }), true);
});

test('解析错误：非法与残缺表达式', () => {
  const cases: Array<[string, number]> = [
    ['', 0],
    ['a &&', 4],
    ['a b', 2],
    ['(a', 2],
    ['a = 1', 2],
    ['a & b', 2],
    ['a == b', 5],
    ['1 == 1', 0],
    ["'x'", 0],
    ['a == ', 5],
  ];
  for (const [source, offset] of cases) {
    assert.throws(
      () => parseCondition(source),
      (err: unknown) => {
        assert.ok(err instanceof ConditionParseError, `expected ConditionParseError for ${source}`);
        assert.equal(err.offset, offset, `offset mismatch for ${JSON.stringify(source)}`);
        return true;
      },
    );
  }
});

test('解析错误信息包含源表达式', () => {
  try {
    parseCondition('focus.editor &&');
    assert.fail('should have thrown');
  } catch (err) {
    assert.ok(err instanceof ConditionParseError);
    assert.match(err.message, /focus\.editor &&/);
  }
});
