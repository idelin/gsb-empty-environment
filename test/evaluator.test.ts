import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate } from '../src/evaluator.ts';
import { field, literal, isSupportedRule, type Rule } from '../src/rules.ts';

test('equality: pass and fail', () => {
  const rules: Rule[] = [{ kind: 'equals', left: field('/a'), right: literal(1) }];
  assert.equal(evaluate({ a: 1 }, rules).status, 'pass');
  const bad = evaluate({ a: 2 }, rules);
  assert.equal(bad.status, 'fail');
  assert.equal(bad.issues[0].instancePath, '/a');
  assert.equal(bad.issues[0].rulePath, '/rules/0');
});

test('missing value is unknown, never false, never zero-filled', () => {
  const rules: Rule[] = [{ kind: 'equals', left: field('/a'), right: literal(0) }];
  const r = evaluate({}, rules);
  assert.equal(r.status, 'unknown');
  assert.equal(r.issues[0].severity, 'unknown');
  assert.equal(r.issues[0].instancePath, '/a');
  assert.equal(r.issues[0].rulePath, '/rules/0');
});

test('type check precedes relation: number vs string is a type-error', () => {
  const rules: Rule[] = [{ kind: 'compare', op: '<', left: field('/a'), right: field('/b') }];
  const r = evaluate({ a: 1, b: 'x' }, rules);
  assert.equal(r.status, 'fail');
  assert.equal(r.issues[0].severity, 'type-error');
  assert.match(r.issues[0].message, /requires numbers/);
});

test('equality across different primitive types is a type-error, not false', () => {
  const rules: Rule[] = [{ kind: 'equals', left: field('/a'), right: field('/b') }];
  const r = evaluate({ a: 1, b: '1' }, rules);
  assert.equal(r.issues[0].severity, 'type-error');
});

test('end < start violation reports BOTH paths', () => {
  const rules: Rule[] = [{ kind: 'compare', op: '<=', left: field('/start'), right: field('/end') }];
  const r = evaluate({ start: 10, end: 3 }, rules);
  assert.equal(r.status, 'fail');
  const issue = r.issues[0];
  assert.equal(issue.severity, 'violation');
  assert.deepEqual(issue.relatedPaths.sort(), ['/end', '/start']);
  assert.equal(issue.rulePath, '/rules/0');
});

test('requiredIf: condition true and field missing => violation', () => {
  const rules: Rule[] = [{
    kind: 'requiredIf',
    when: { kind: 'compare', op: '==', left: field('/kind'), right: literal('vip') },
    path: '/discount',
  }];
  const r = evaluate({ kind: 'vip' }, rules);
  assert.equal(r.status, 'fail');
  assert.equal(r.issues[0].instancePath, '/discount');
});

test('requiredIf: condition false => pass', () => {
  const rules: Rule[] = [{
    kind: 'requiredIf',
    when: { kind: 'compare', op: '==', left: field('/kind'), right: literal('vip') },
    path: '/discount',
  }];
  assert.equal(evaluate({ kind: 'basic' }, rules).status, 'pass');
});

test('requiredIf: unknown condition is reported, never silently skipped', () => {
  const rules: Rule[] = [{
    kind: 'requiredIf',
    when: { kind: 'compare', op: '==', left: field('/kind'), right: literal('vip') },
    path: '/discount',
  }];
  const r = evaluate({}, rules); // /kind missing => condition unknown
  assert.equal(r.status, 'unknown');
  const unknowns = r.issues.filter((i) => i.severity === 'unknown');
  assert.ok(unknowns.length >= 1);
  assert.ok(unknowns.some((i) => i.rulePath === '/rules/0/when'));
  // and it must NOT be reported as a pass
  assert.notEqual(r.status, 'pass');
});

test('sum: empty list sums to 0', () => {
  const rules: Rule[] = [{ kind: 'sum', listPath: '/items', op: '==', limit: 0 }];
  assert.equal(evaluate({ items: [] }, rules).status, 'pass');
});

test('sum: missing list is unknown, distinct from empty list', () => {
  const rules: Rule[] = [{ kind: 'sum', listPath: '/items', op: '==', limit: 0 }];
  const r = evaluate({}, rules);
  assert.equal(r.status, 'unknown');
  assert.equal(r.issues[0].severity, 'unknown');
  assert.equal(r.issues[0].instancePath, '/items');
});

test('sum: missing item field is unknown (no zero-fill)', () => {
  const rules: Rule[] = [{ kind: 'sum', listPath: '/items', itemPath: '/price', op: '<=', limit: 10 }];
  const r = evaluate({ items: [{ price: 3 }, {}] }, rules);
  assert.equal(r.status, 'unknown');
  assert.equal(r.issues[0].instancePath, '/items/1/price');
});

test('sum: non-number item is a type-error', () => {
  const rules: Rule[] = [{ kind: 'sum', listPath: '/items', op: '<=', limit: 10 }];
  const r = evaluate({ items: [1, 'x'] }, rules);
  assert.equal(r.issues[0].severity, 'type-error');
  assert.equal(r.issues[0].instancePath, '/items/1');
});

test('sum: violation carries list path and rule path', () => {
  const rules: Rule[] = [{ kind: 'sum', listPath: '/items', itemPath: '/price', op: '<=', limit: 5 }];
  const r = evaluate({ items: [{ price: 3 }, { price: 4 }] }, rules);
  assert.equal(r.status, 'fail');
  assert.equal(r.issues[0].instancePath, '/items');
  assert.equal(r.issues[0].rulePath, '/rules/0');
});

test('nested paths and array indices resolve', () => {
  const rules: Rule[] = [{ kind: 'equals', left: field('/a/1/b'), right: literal('x') }];
  assert.equal(evaluate({ a: [{ b: 'y' }, { b: 'x' }] }, rules).status, 'pass');
});

test('only the implemented rule subset is accepted', () => {
  assert.equal(isSupportedRule({ kind: 'sum' }), true);
  assert.equal(isSupportedRule({ kind: 'regexMatch' }), false);
  assert.equal(isSupportedRule(null), false);
});

test('every issue carries both instance path and rule path', () => {
  const rules: Rule[] = [
    { kind: 'compare', op: '>=', left: field('/end'), right: field('/start') },
    { kind: 'typeIs', path: '/n', type: 'number' },
  ];
  const r = evaluate({ start: 5, end: 1, n: 'oops' }, rules);
  assert.equal(r.issues.length, 2);
  for (const issue of r.issues) {
    assert.ok(issue.instancePath.length > 0, 'instance path present');
    assert.ok(/^\/rules\/\d+/.test(issue.rulePath), 'rule path present');
  }
});
