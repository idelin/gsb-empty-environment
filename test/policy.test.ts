import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { InvalidPolicyError } from '../src/errors.ts';
import {
  createPolicy,
  evaluateRule,
  freezeAttributes,
  validateRule,
  type Rule,
} from '../src/policy.ts';

describe('policy validation', () => {
  it('accepts finite sets, integer conditions and boolean combinators', () => {
    const rule: Rule = {
      kind: 'and',
      rules: [
        { kind: 'in', attr: 'region', values: ['cn-north', 'cn-east'] },
        { kind: 'int', attr: 'seats', cmp: 'lte', value: 100 },
        { kind: 'or', rules: [
          { kind: 'int', attr: 'seats', cmp: 'gte', value: 1 },
          { kind: 'not', rule: { kind: 'in', attr: 'tier', values: ['trial'] } },
        ] },
      ],
    };
    assert.doesNotThrow(() => validateRule(rule));
  });

  it('rejects non-finite set members', () => {
    for (const bad of [Number.NaN, Number.POSITIVE_INFINITY, {}, null, undefined]) {
      assert.throws(
        () => validateRule({ kind: 'in', attr: 'a', values: [bad] }),
        InvalidPolicyError,
      );
    }
  });

  it('rejects non-integer conditions', () => {
    for (const bad of [1.5, Number.NaN, Number.MAX_SAFE_INTEGER + 1, '5']) {
      assert.throws(
        () => validateRule({ kind: 'int', attr: 'a', cmp: 'eq', value: bad }),
        InvalidPolicyError,
      );
    }
  });

  it('rejects unknown kinds, empty sets and deep nesting', () => {
    assert.throws(() => validateRule({ kind: 'regex', attr: 'a' }), InvalidPolicyError);
    assert.throws(() => validateRule({ kind: 'in', attr: 'a', values: [] }), InvalidPolicyError);
    let deep: Rule = { kind: 'in', attr: 'a', values: [1] };
    for (let i = 0; i < 20; i += 1) deep = { kind: 'not', rule: deep };
    assert.throws(() => validateRule(deep), InvalidPolicyError);
  });
});

describe('policy evaluation', () => {
  const attrs = freezeAttributes({ region: 'cn-north', seats: 10, trial: false });

  it('evaluates in / int / and / or / not', () => {
    assert.equal(evaluateRule({ kind: 'in', attr: 'region', values: ['cn-north'] }, attrs), true);
    assert.equal(evaluateRule({ kind: 'in', attr: 'region', values: ['eu-west'] }, attrs), false);
    assert.equal(evaluateRule({ kind: 'int', attr: 'seats', cmp: 'gte', value: 10 }, attrs), true);
    assert.equal(evaluateRule({ kind: 'int', attr: 'seats', cmp: 'lt', value: 10 }, attrs), false);
    assert.equal(evaluateRule({ kind: 'not', rule: { kind: 'in', attr: 'trial', values: [true] } }, attrs), true);
    assert.equal(
      evaluateRule({ kind: 'and', rules: [
        { kind: 'in', attr: 'region', values: ['cn-north'] },
        { kind: 'or', rules: [
          { kind: 'int', attr: 'seats', cmp: 'gt', value: 100 },
          { kind: 'int', attr: 'seats', cmp: 'eq', value: 10 },
        ] },
      ] }, attrs),
      true,
    );
  });

  it('treats missing attributes as not matching', () => {
    assert.equal(evaluateRule({ kind: 'in', attr: 'nope', values: [1] }, attrs), false);
    assert.equal(evaluateRule({ kind: 'int', attr: 'nope', cmp: 'eq', value: 1 }, attrs), false);
    assert.equal(evaluateRule({ kind: 'not', rule: { kind: 'in', attr: 'nope', values: [1] } }, attrs), true);
  });

  it('requires integer attributes for integer conditions', () => {
    const loose = freezeAttributes({ seats: 10.5, label: '10' });
    assert.equal(evaluateRule({ kind: 'int', attr: 'seats', cmp: 'eq', value: 10 }, loose), false);
    assert.equal(evaluateRule({ kind: 'int', attr: 'label', cmp: 'eq', value: 10 }, loose), false);
  });

  it('keeps policies and attribute sets read-only', () => {
    const policy = createPolicy(1, 'p', { kind: 'in', attr: 'a', values: [1] });
    assert.equal(Object.isFrozen(policy), true);
    assert.equal(Object.isFrozen(policy.rule), true);
    assert.equal(Object.isFrozen((policy.rule as { values: unknown }).values), true);
    assert.equal(Object.isFrozen(attrs), true);
    assert.throws(() => {
      (policy as { version: number }).version = 99;
    }, TypeError);
  });

  it('rejects non-primitive attribute values', () => {
    assert.throws(() => freezeAttributes({ bad: { nested: 1 } }), InvalidPolicyError);
    assert.throws(() => freezeAttributes({ bad: Number.NaN }), InvalidPolicyError);
  });
});
