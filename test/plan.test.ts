import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { UnitMigrationError, UnitRegistry, compilePlan } from '../src/index.ts';

const registry = () =>
  new UnitRegistry(
    [
      { name: 'm', dimension: 'length' },
      { name: 'mm', dimension: 'length' },
    ],
    [{ from: 'm', to: 'mm', ratio: 1000 }],
  );

const rule = (path: string) => ({ path, from: 'm', to: 'mm', quantity: 'absolute' as const });

describe('compilePlan path consistency', () => {
  test('duplicate path patterns are rejected', () => {
    assert.throws(
      () => compilePlan(registry(), { fields: [rule('a.b'), rule('a.b')] }),
      (e: unknown) => {
        assert.ok(e instanceof UnitMigrationError);
        assert.equal(e.code, 'COMPILE_PATH');
        assert.match(e.message, /duplicate/);
        return true;
      },
    );
  });

  test('a pattern that prefixes another is rejected', () => {
    assert.throws(
      () => compilePlan(registry(), { fields: [rule('a'), rule('a.b')] }),
      (e: unknown) => {
        assert.ok(e instanceof UnitMigrationError);
        assert.equal(e.code, 'COMPILE_PATH');
        assert.match(e.message, /prefix/);
        return true;
      },
    );
    assert.throws(
      () => compilePlan(registry(), { fields: [rule('a[*]'), rule('a[*].b')] }),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_PATH',
    );
  });

  test('sibling patterns and wildcard/object splits are fine', () => {
    const plan = compilePlan(registry(), {
      fields: [rule('a.b'), rule('a.c'), rule('list[*].d'), rule('list[*].e')],
    });
    assert.equal(plan.fields.length, 4);
  });

  test('invalid patterns are rejected', () => {
    assert.throws(
      () => compilePlan(registry(), { fields: [rule('a..b')] }),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_PATH',
    );
    assert.throws(
      () => compilePlan(registry(), { fields: [rule('a[0]')] }),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_PATH',
    );
  });
});

describe('compilePlan unit and precision checks', () => {
  test('unknown units are compile errors', () => {
    assert.throws(
      () =>
        compilePlan(registry(), {
          fields: [{ path: 'a', from: 'm', to: 'furlong', quantity: 'absolute' }],
        }),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_UNIT',
    );
  });

  test('unknown rounding modes are compile errors', () => {
    assert.throws(
      () =>
        compilePlan(registry(), {
          fields: [
            {
              path: 'a',
              from: 'm',
              to: 'mm',
              quantity: 'absolute',
              precision: { type: 'integer', rounding: 'random' as never },
            },
          ],
        }),
      (e: unknown) => e instanceof UnitMigrationError && e.code === 'COMPILE_PATH',
    );
  });

  test('root pattern "" addresses the root value', () => {
    const plan = compilePlan(registry(), { fields: [rule('')] });
    assert.equal(plan.fields[0].segments.length, 0);
  });
});
