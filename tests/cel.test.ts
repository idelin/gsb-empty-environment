import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, CelInt } from '../src/cel/index.ts';

describe('CEL subset: literals and arithmetic', () => {
  test('integer arithmetic uses int64 semantics with truncated division', () => {
    assert.deepEqual(evaluate('7 / 2', {}), new CelInt(3));
    assert.deepEqual(evaluate('-7 / 2', {}), new CelInt(-3));
    assert.deepEqual(evaluate('2 + 3 * 4', {}), new CelInt(14));
    assert.deepEqual(evaluate('0x10 + 1', {}), new CelInt(17));
  });

  test('division by zero raises a runtime error', () => {
    assert.throws(() => evaluate('1 / 0', {}), /division by zero/);
    assert.throws(() => evaluate('1 % 0', {}), /modulo by zero/);
  });

  test('double arithmetic stays on floats', () => {
    assert.equal(evaluate('7.0 / 2.0', {}), 3.5);
  });

  test('string concatenation and lists', () => {
    assert.equal(evaluate('"a" + "b"', {}), 'ab');
    assert.deepEqual(evaluate('[1, 2] + [3]', {}), [
      new CelInt(1),
      new CelInt(2),
      new CelInt(3),
    ]);
  });
});

describe('CEL subset: logical short circuiting', () => {
  test('&& does not evaluate the right side when the left is false', () => {
    // The right side would error on division by zero if eagerly evaluated.
    assert.equal(evaluate('false && (1 / 0 == 1)', {}), false);
  });

  test('|| does not evaluate the right side when the left is true', () => {
    assert.equal(evaluate('true || (1 / 0 == 1)', {}), true);
  });

  test('conditional only evaluates the taken branch', () => {
    assert.deepEqual(evaluate('true ? 1 : (1 / 0)', {}), new CelInt(1));
    assert.deepEqual(evaluate('false ? (1 / 0) : 2', {}), new CelInt(2));
  });
});

describe('CEL subset: field access and has()', () => {
  const env = { object: { metadata: { name: 'pod-1', labels: { app: 'web' } } } };

  test('missing field access throws', () => {
    assert.throws(
      () => evaluate('object.spec.replicas', env),
      /no such key/,
    );
  });

  test('has() tolerates missing intermediate and leaf keys', () => {
    assert.equal(evaluate('has(object.metadata.name)', env), true);
    assert.equal(evaluate('has(object.metadata.missing)', env), false);
    assert.equal(evaluate('has(object.spec.replicas)', env), false);
  });

  test('in operator covers lists, maps and substrings', () => {
    assert.equal(evaluate('"web" in ["web", "db"]', {}), true);
    assert.equal(evaluate('"app" in object.metadata.labels', env), true);
    assert.equal(evaluate('"od" in "pod"', {}), true);
  });
});

describe('CEL subset: builtins and macros', () => {
  test('string functions', () => {
    assert.equal(evaluate('"Pod-1".lowerAscii()', {}), 'pod-1');
    assert.equal(evaluate('"pod-x".startsWith("pod")', {}), true);
    assert.deepEqual(evaluate('"a,b,c".split(",").size()', {}), new CelInt(3));
    assert.equal(evaluate('"abc".matches("^a.c$")', {}), true);
  });

  test('list macros all/exists/exists_one/filter/map', () => {
    const env = { xs: [1, 2, 3, 4].map((n) => new CelInt(n)) };
    assert.equal(evaluate('xs.all(x, x > 0)', env), true);
    assert.equal(evaluate('xs.exists(x, x == 3)', env), true);
    assert.equal(evaluate('xs.exists_one(x, x > 3)', env), true);
    assert.equal(evaluate('xs.exists_one(x, x > 2)', env), false);
    assert.deepEqual(
      evaluate('xs.filter(x, x % 2 == 0)', env),
      [new CelInt(2), new CelInt(4)],
    );
    assert.deepEqual(
      evaluate('xs.map(x, x * 10)', env),
      [10, 20, 30, 40].map((n) => new CelInt(n)),
    );
  });

  test('size works across strings, lists and maps', () => {
    assert.deepEqual(evaluate('size("hello")', {}), new CelInt(5));
    assert.deepEqual(evaluate('size([1, 2])', {}), new CelInt(2));
    assert.deepEqual(evaluate('size({"a": 1})', {}), new CelInt(1));
  });

  test('type conversions', () => {
    assert.deepEqual(evaluate('int("42")', {}), new CelInt(42));
    assert.deepEqual(evaluate('int(3.9)', {}), new CelInt(3));
    assert.equal(evaluate('string(42)', {}), '42');
    assert.equal(evaluate('double("1.5")', {}), 1.5);
  });
});
