import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  UnitRegistry,
  compilePlan,
  migrate,
  type AuditEntry,
  type JsonValue,
} from '../src/index.ts';

const registry = () =>
  new UnitRegistry(
    [
      { name: 'm', dimension: 'length' },
      { name: 'mm', dimension: 'length' },
      { name: 'ft', dimension: 'length' },
      { name: 'K', dimension: 'temperature' },
      { name: 'C', dimension: 'temperature' },
    ],
    [
      { from: 'm', to: 'mm', ratio: 1000 },
      { from: 'ft', to: 'm', ratio: '381/1250' },
      { from: 'C', to: 'K', ratio: 1, offset: '273.15' },
    ],
  );

const weatherPlan = () =>
  compilePlan(registry(), {
    fields: [
      { path: 'sensors[*].tempC', from: 'C', to: 'K', quantity: 'absolute' },
      { path: 'sensors[*].tempDeltaC', from: 'C', to: 'K', quantity: 'delta' },
      {
        path: 'sensors[*].altitudeM',
        from: 'm',
        to: 'mm',
        quantity: 'absolute',
        precision: { type: 'integer', rounding: 'half-even' },
      },
      { path: 'envelope.minTempC', from: 'C', to: 'K', quantity: 'absolute' },
      { path: 'envelope.maxTempC', from: 'C', to: 'K', quantity: 'absolute' },
    ],
    constraints: [
      {
        name: 'envelope-ordered',
        check: (root: JsonValue) => {
          const env = (root as Record<string, JsonValue>).envelope as Record<string, JsonValue>;
          return Number(env.minTempC) < Number(env.maxTempC)
            ? null
            : { path: '$.envelope', message: 'min must be below max' };
        },
      },
    ],
  });

const payload = () => ({
  station: 'alpha',
  sensors: [
    { id: 's1', tempC: 20, tempDeltaC: '1.5', altitudeM: 1 },
    { id: 's2', tempC: '22.4', tempDeltaC: 2, altitudeM: '0.5004' },
  ],
  envelope: { minTempC: 15, maxTempC: 30 },
});

describe('migrate: happy path', () => {
  test('nested arrays and objects are migrated; 1 m is exactly 1000 mm', () => {
    const result = migrate(weatherPlan(), payload());
    assert.ok(result.ok);
    const value = result.value as Record<string, JsonValue>;
    const sensors = value.sensors as Record<string, JsonValue>[];
    assert.equal(sensors[0].altitudeM, 1000);
    assert.equal(sensors[0].tempC, 293.15);
    assert.equal(sensors[0].tempDeltaC, 1.5); // delta: no offset applied
    assert.equal(sensors[1].tempC, 295.55);
    assert.equal(sensors[1].tempDeltaC, 2);
    assert.equal(sensors[1].altitudeM, 500); // 500.4 mm rounded half-even
    assert.equal(value.station, 'alpha'); // untouched fields pass through
  });

  test('array order is preserved and object key order does not matter', () => {
    const a = migrate(weatherPlan(), payload());
    const reordered = {
      envelope: { maxTempC: 30, minTempC: 15 },
      sensors: [
        { altitudeM: 1, tempDeltaC: '1.5', tempC: 20, id: 's1' },
        { altitudeM: '0.5004', tempDeltaC: 2, tempC: '22.4', id: 's2' },
      ],
      station: 'alpha',
    };
    const b = migrate(weatherPlan(), reordered);
    assert.ok(a.ok && b.ok);
    const idsA = (a.value as Record<string, JsonValue>).sensors as Record<string, JsonValue>[];
    const idsB = (b.value as Record<string, JsonValue>).sensors as Record<string, JsonValue>[];
    assert.deepEqual(
      idsA.map((s) => s.id),
      ['s1', 's2'],
    );
    assert.deepEqual(
      idsB.map((s) => s.id),
      ['s1', 's2'],
    );
    assert.deepEqual(
      idsA.map((s) => s.altitudeM),
      idsB.map((s) => s.altitudeM),
    );
  });

  test('the input object is never mutated', () => {
    const input = payload();
    const snapshot = JSON.parse(JSON.stringify(input));
    const result = migrate(weatherPlan(), input);
    assert.ok(result.ok);
    assert.deepEqual(input, snapshot);
  });
});

describe('migrate: audit trail', () => {
  const auditFor = (audit: AuditEntry[], path: string): AuditEntry => {
    const entry = audit.find((e) => e.path === path);
    assert.ok(entry, `no audit entry for ${path}`);
    return entry;
  };

  test('integer targets record exact value, error and error interval', () => {
    const result = migrate(weatherPlan(), payload());
    assert.ok(result.ok);
    const entry = auditFor(result.audit, '$.sensors[1].altitudeM');
    assert.equal(entry.input, '0.5004');
    assert.equal(entry.exact, '500.4');
    assert.equal(entry.output, 500);
    assert.equal(entry.lossy, true);
    assert.equal(entry.error, '-0.4');
    assert.deepEqual(entry.errorInterval, {
      lo: '-0.5',
      loInclusive: true,
      hi: '0.5',
      hiInclusive: true,
    });
  });

  test('lossless integer conversions are marked lossy:false with zero error', () => {
    const result = migrate(weatherPlan(), payload());
    assert.ok(result.ok);
    const entry = auditFor(result.audit, '$.sensors[0].altitudeM');
    assert.equal(entry.exact, '1000');
    assert.equal(entry.output, 1000);
    assert.equal(entry.lossy, false);
    assert.equal(entry.error, '0');
  });

  test('absolute vs delta is visible in the audit', () => {
    const result = migrate(weatherPlan(), payload());
    assert.ok(result.ok);
    assert.equal(auditFor(result.audit, '$.sensors[0].tempC').quantity, 'absolute');
    assert.equal(auditFor(result.audit, '$.sensors[0].tempDeltaC').quantity, 'delta');
  });

  test('exact results that JSON numbers cannot represent become strings, not silent truncations', () => {
    const plan = compilePlan(registry(), {
      fields: [{ path: 'height', from: 'm', to: 'ft', quantity: 'absolute' }],
    });
    const result = migrate(plan, { height: 1 });
    assert.ok(result.ok);
    const value = result.value as Record<string, JsonValue>;
    assert.equal(value.height, '1250/381'); // 1 m in feet, exact
    const entry = result.audit[0];
    assert.equal(entry.lossy, false);
    assert.match(entry.note ?? '', /emitted as string/);
  });
});

describe('migrate: constraints and rollback', () => {
  test('constraint failure on the complete new object rolls everything back', () => {
    const input = { ...payload(), envelope: { minTempC: 15, maxTempC: 10 } };
    const snapshot = JSON.parse(JSON.stringify(input));
    const result = migrate(weatherPlan(), input);
    assert.ok(!result.ok);
    assert.equal(result.errors.length, 1);
    assert.equal(result.errors[0].code, 'CONSTRAINT');
    assert.match(result.errors[0].message, /envelope-ordered/);
    assert.equal(result.errors[0].path, '$.envelope');
    assert.ok(!('value' in result)); // no partial output escapes
    assert.deepEqual(input, snapshot); // input untouched
  });
});

describe('migrate: located errors and warnings', () => {
  test('non-numeric field values are located by path', () => {
    const input = payload();
    (input.sensors[1] as Record<string, JsonValue>).tempC = 'hot';
    const result = migrate(weatherPlan(), input);
    assert.ok(!result.ok);
    assert.equal(result.errors[0].code, 'VALUE_NOT_NUMERIC');
    assert.equal(result.errors[0].path, '$.sensors[1].tempC');
  });

  test('shape mismatches are located by path', () => {
    const input = { ...payload(), sensors: { id: 'not-an-array' } };
    const result = migrate(weatherPlan(), input as JsonValue);
    assert.ok(!result.ok);
    assert.equal(result.errors[0].code, 'PATH_TYPE');
    assert.equal(result.errors[0].path, '$.sensors');
  });

  test('non-JSON input is rejected with a path', () => {
    const result = migrate(weatherPlan(), { a: { b: Number.NaN } } as unknown as JsonValue);
    assert.ok(!result.ok);
    assert.equal(result.errors[0].code, 'INPUT_NOT_JSON');
    assert.equal(result.errors[0].path, '$.a.b');
  });

  test('missing keys become warnings, not failures', () => {
    const plan = compilePlan(registry(), {
      fields: [
        { path: 'sensors[*].tempC', from: 'C', to: 'K', quantity: 'absolute' },
        { path: 'envelope.minTempC', from: 'C', to: 'K', quantity: 'absolute' },
        { path: 'envelope.maxTempC', from: 'C', to: 'K', quantity: 'absolute' },
      ],
    });
    const input = payload() as Record<string, JsonValue>;
    delete input.envelope;
    const result = migrate(plan, input);
    assert.ok(result.ok);
    assert.equal(result.warnings.length, 2);
    assert.match(result.warnings[0].message, /envelope/);
  });
});
