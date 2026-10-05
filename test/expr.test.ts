import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, parseExpression } from '../src/expr.ts';
import { ExpressionError, UnknownVariableError } from '../src/errors.ts';

const run = (src: string, vars: Record<string, boolean | string | number> = {}) =>
  evaluate(parseExpression(src), vars);

test('precedence: && binds tighter than ||', () => {
  assert.equal(run('A || B && C', { A: false, B: true, C: false }), false);
  assert.equal(run('A || B && C', { A: true, B: false, C: false }), true);
});

test('negation and parentheses', () => {
  assert.equal(run('!A && B', { A: false, B: true }), true);
  assert.equal(run('!(A || B)', { A: false, B: true }), false);
  assert.equal(run('(A || B) && C', { A: true, B: false, C: true }), true);
  assert.equal(run('!!A', { A: true }), true);
});

test('literals', () => {
  assert.equal(run('true'), true);
  assert.equal(run('false || true'), true);
});

test('short-circuit: && does not evaluate the right side when false', () => {
  assert.equal(run('false && UNKNOWN'), false);
  assert.equal(run('A && UNKNOWN', { A: false }), false);
});

test('short-circuit: || does not evaluate the right side when true', () => {
  assert.equal(run('true || UNKNOWN'), true);
  assert.equal(run('A || UNKNOWN', { A: true }), true);
});

test('unknown variable errors when actually evaluated', () => {
  assert.throws(() => run('MISSING'), (err: unknown) => {
    assert.ok(err instanceof UnknownVariableError);
    assert.equal((err as UnknownVariableError).varName, 'MISSING');
    return true;
  });
  assert.throws(() => run('true && MISSING'), UnknownVariableError);
  assert.throws(() => run('false || MISSING'), UnknownVariableError);
});

test('bare identifier must be boolean', () => {
  assert.throws(() => run('NAME', { NAME: 'x' }), ExpressionError);
  assert.throws(() => run('LEVEL', { LEVEL: 2 }), ExpressionError);
});

test('comparisons with == and !=', () => {
  assert.equal(run('MODE == "prod"', { MODE: 'prod' }), true);
  assert.equal(run("MODE == 'dev'", { MODE: 'prod' }), false);
  assert.equal(run('LEVEL != 3', { LEVEL: 2 }), true);
  assert.equal(run('LEVEL == 3', { LEVEL: 3 }), true);
  assert.equal(run('"prod" == MODE', { MODE: 'prod' }), true);
  assert.equal(run('ENABLED == true', { ENABLED: true }), true);
});

test('comparison with unknown variable errors', () => {
  assert.throws(() => run('MODE == "prod"'), UnknownVariableError);
});

test('syntax errors are rejected', () => {
  for (const bad of ['A &&', '&& A', '(A', 'A)', 'A B', 'A & B', 'A = 1', 'A ==', '1', '"x"', '']) {
    assert.throws(() => parseExpression(bad), ExpressionError, `expected error for: ${bad}`);
  }
});
