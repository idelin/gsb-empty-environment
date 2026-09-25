import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  UnitRegistry,
  compilePlan,
  invertPlan,
  migrate,
  type JsonValue,
} from '../src/index.ts';

const registry = () =>
  new UnitRegistry(
    [
      { name: 'm', dimension: 'length' },
      { name: 'mm', dimension: 'length' },
    ],
    [{ from: 'm', to: 'mm', ratio: 1000 }],
  );

describe('invertPlan', () => {
  test('round-trip of an exact forward migration is lossless', () => {
    const plan = compilePlan(registry(), {
      fields: [{ path: 'd', from: 'm', to: 'mm', quantity: 'absolute' }],
    });
    const forward = migrate(plan, { d: 2 });
    assert.ok(forward.ok);
    assert.equal((forward.value as Record<string, JsonValue>).d, 2000);

    const back = migrate(invertPlan(plan, forward), forward.value);
    assert.ok(back.ok);
    assert.equal((back.value as Record<string, JsonValue>).d, 2);
    assert.equal(back.audit[0].lossless, true);
    assert.equal(back.audit[0].note, undefined);
  });

  test('inverse of a lossy forward migration never claims to be lossless', () => {
    const plan = compilePlan(registry(), {
      fields: [
        {
          path: 'd',
          from: 'm',
          to: 'mm',
          quantity: 'absolute',
          precision: { type: 'integer', rounding: 'half-up' },
        },
      ],
    });
    const forward = migrate(plan, { d: '0.5004' });
    assert.ok(forward.ok);
    assert.equal((forward.value as Record<string, JsonValue>).d, 500);
    assert.equal(forward.audit[0].lossy, true);

    const back = migrate(invertPlan(plan, forward), forward.value);
    assert.ok(back.ok);
    // 500 mm -> 0.5 m: the 0.0004 m lost to rounding is not recovered.
    assert.equal((back.value as Record<string, JsonValue>).d, 0.5);
    assert.equal(back.audit[0].lossless, false);
    assert.match(back.audit[0].note ?? '', /approximate/);
  });

  test('inverse affine is exact: offsets invert too', () => {
    const reg = new UnitRegistry(
      [
        { name: 'K', dimension: 'temperature' },
        { name: 'C', dimension: 'temperature' },
      ],
      [{ from: 'C', to: 'K', ratio: 1, offset: '273.15' }],
    );
    const plan = compilePlan(reg, {
      fields: [{ path: 't', from: 'C', to: 'K', quantity: 'absolute' }],
    });
    const forward = migrate(plan, { t: 20 });
    assert.ok(forward.ok);
    assert.equal((forward.value as Record<string, JsonValue>).t, 293.15);
    const back = migrate(invertPlan(plan, forward), forward.value);
    assert.ok(back.ok);
    assert.equal((back.value as Record<string, JsonValue>).t, 20);
    assert.equal(back.audit[0].lossless, true);
  });
});
