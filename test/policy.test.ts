import assert from 'node:assert/strict';
import test from 'node:test';
import { PolicyEngine, PolicyError } from '../src/policy.ts';

test('finite set membership condition', () => {
  const engine = new PolicyEngine();
  engine.register('auth', {
    name: 'canPublish',
    when: { op: 'in', prop: 'role', set: ['admin', 'editor'] },
    effect: 'allow',
  });
  assert.equal(engine.evaluate('auth', 'canPublish', { role: 'admin' }), 'allow');
  assert.equal(engine.evaluate('auth', 'canPublish', { role: 'viewer' }), 'deny');
});

test('integer conditions and boolean algebra', () => {
  const engine = new PolicyEngine();
  engine.register('quota', {
    name: 'withinLimit',
    when: {
      op: 'and',
      conditions: [
        { op: 'ge', prop: 'age', value: 18 },
        { op: 'lt', prop: 'count', value: 10 },
        {
          op: 'or',
          conditions: [
            { op: 'eq', prop: 'tier', value: 2 },
            { op: 'not', condition: { op: 'in', prop: 'region', set: ['blocked'] } },
          ],
        },
      ],
    },
    effect: 'allow',
  });
  assert.equal(engine.evaluate('quota', 'withinLimit', { age: 20, count: 3, tier: 2, region: 'blocked' }), 'allow');
  assert.equal(engine.evaluate('quota', 'withinLimit', { age: 20, count: 3, tier: 1, region: 'ok' }), 'allow');
  assert.equal(engine.evaluate('quota', 'withinLimit', { age: 20, count: 3, tier: 1, region: 'blocked' }), 'deny');
  assert.equal(engine.evaluate('quota', 'withinLimit', { age: 17, count: 3, tier: 2, region: 'ok' }), 'deny');
});

test('conditions are limited to finite sets and integers', () => {
  const engine = new PolicyEngine();
  assert.throws(
    () => engine.register('m', { name: 'r', when: { op: 'in', prop: 'x', set: [Infinity] }, effect: 'allow' }),
    (err: unknown) => err instanceof PolicyError && (err as PolicyError).code === 'INVALID_CONDITION',
  );
  assert.throws(
    () => engine.register('m', { name: 'r2', when: { op: 'gt', prop: 'x', value: 1.5 }, effect: 'allow' }),
    /integer/,
  );
  assert.throws(
    () =>
      engine.register('m', {
        name: 'r3',
        when: { op: 'in', prop: 'x', set: 'not-an-array' as unknown as string[] },
        effect: 'allow',
      }),
    /finite array/,
  );
});

test('properties are explicit and read-only: unknown property is an error', () => {
  const engine = new PolicyEngine();
  engine.register('m', { name: 'r', when: { op: 'eq', prop: 'level', value: 1 }, effect: 'allow' });
  assert.throws(
    () => engine.evaluate('m', 'r', { other: 1 }),
    (err: unknown) => err instanceof PolicyError && (err as PolicyError).code === 'UNKNOWN_PROPERTY',
  );
  const props = { level: 1 };
  assert.equal(engine.evaluate('m', 'r', props), 'allow');
  assert.deepEqual(props, { level: 1 }, 'evaluation never mutates the caller context');
});

test('same-named rules in two modules never override each other', () => {
  const engine = new PolicyEngine();
  engine.register('modA', { name: 'canEdit', when: { op: 'eq', prop: 'level', value: 1 }, effect: 'allow' });
  engine.register('modB', { name: 'canEdit', when: { op: 'eq', prop: 'level', value: 9 }, effect: 'allow' });
  assert.equal(engine.evaluate('modA', 'canEdit', { level: 1 }), 'allow');
  assert.equal(engine.evaluate('modB', 'canEdit', { level: 1 }), 'deny');
  assert.equal(engine.evaluate('modB', 'canEdit', { level: 9 }), 'allow');
  assert.throws(
    () => engine.register('modA', { name: 'canEdit', when: { op: 'eq', prop: 'level', value: 2 }, effect: 'allow' }),
    (err: unknown) => err instanceof PolicyError && (err as PolicyError).code === 'DUPLICATE_RULE',
  );
});
