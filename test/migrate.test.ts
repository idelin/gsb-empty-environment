import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rational, roundToInteger } from '../src/rational.ts';
import { getUnit } from '../src/units.ts';
import { migrate, invertMigration, analyzeChain } from '../src/migrate.ts';
import { compileMigration } from '../src/compile.ts';
import type { SchemaNode } from '../src/schema.ts';
import type { MigrationError } from '../src/errors.ts';

const stationSchema: SchemaNode = {
  type: 'object',
  fields: {
    name: { type: 'string' },
    altitude: { type: 'number', unit: 'm', integer: true, rounding: 'half-even' },
    readings: {
      type: 'array',
      items: {
        type: 'object',
        fields: {
          temp: { type: 'number', unit: 'degC' },
          spread: { type: 'number', unit: 'dC' },
        },
      },
    },
  },
};

test('nested objects and ordered arrays migrate together', () => {
  const result = migrate(
    stationSchema,
    [
      { path: 'readings[].temp', to: 'K' },
      { path: 'readings[].spread', to: 'dF' },
      { path: 'altitude', to: 'mm' },
    ],
    {
      name: 'alpha',
      altitude: 12,
      readings: [
        { temp: 20, spread: 9 },
        { temp: -40, spread: 18 },
      ],
    },
  );
  assert.ok(result.ok);
  const data = result.data as any;
  assert.equal(data.readings[0].temp, 293.15);
  assert.equal(data.readings[1].temp, 233.15);
  assert.equal(data.readings[0].spread, 16.2); // 9 dC -> 16.2 dF, no offset
  assert.equal(data.readings[1].spread, 32.4);
  assert.equal(data.altitude, 12000); // 12 m -> 12000 mm exactly
  assert.equal(data.name, 'alpha');
  assert.ok(result.lossless);
});

test('1 m migrates to exactly 1000 mm with a zero error interval', () => {
  const schema: SchemaNode = {
    type: 'object',
    fields: { d: { type: 'number', unit: 'm', integer: true, rounding: 'trunc' } },
  };
  const result = migrate(schema, [{ path: 'd', to: 'mm' }], { d: 1 });
  assert.ok(result.ok);
  assert.equal((result.data as any).d, 1000);
  const entry = result.audit[0]!;
  assert.equal(entry.kind, 'exact');
  assert.deepEqual(entry.errorInterval, { lo: '0', hi: '0' });
});

test('integer target rounding loss is audited with an exact error interval', () => {
  const schema: SchemaNode = {
    type: 'object',
    fields: { d: { type: 'number', unit: 'cm', integer: true, rounding: 'trunc' } },
  };
  const result = migrate(schema, [{ path: 'd', to: 'm' }], { d: 150.4 });
  assert.ok(result.ok);
  assert.equal((result.data as any).d, 1);
  assert.ok(!result.lossless);
  const entry = result.audit[0]!;
  assert.equal(entry.kind, 'rounding-loss');
  assert.equal(entry.path, 'd');
  assert.equal(entry.exactValue, '188/125'); // 1.504
  assert.equal(entry.roundingMode, 'trunc');
  // true value = output + 0.504 exactly
  assert.deepEqual(entry.errorInterval, { lo: '63/125', hi: '63/125' });
});

test('errors locate the concrete array path', () => {
  const result = migrate(
    stationSchema,
    [{ path: 'readings[].temp', to: 'K' }],
    { name: 'x', altitude: 1, readings: [{ temp: 1, spread: 1 }, { temp: 'hot', spread: 1 }] },
  );
  assert.ok(!result.ok);
  assert.equal(result.error.code, 'value-type-mismatch');
  assert.equal(result.error.path, 'readings[1].temp');
});

test('cross-field constraint failure rolls the whole migration back', () => {
  const schema: SchemaNode = {
    type: 'object',
    fields: {
      lo: { type: 'number', unit: 'm' },
      hi: { type: 'number', unit: 'm' },
    },
  };
  const input = { lo: 2000, hi: 1000 };
  const snapshot = JSON.parse(JSON.stringify(input));
  const result = migrate(schema, [{ path: 'lo', to: 'km' }, { path: 'hi', to: 'km' }], input, {
    constraints: [
      {
        name: 'lo<=hi',
        paths: ['lo', 'hi'],
        check: (root) => {
          const r = root as { lo: number; hi: number };
          return r.lo <= r.hi || `lo (${r.lo}) exceeds hi (${r.hi})`;
        },
      },
    ],
  });
  assert.ok(!result.ok);
  assert.equal(result.error.code, 'constraint-violation');
  assert.match(result.error.message, /lo<=hi/);
  assert.equal(result.error.path, 'lo, hi');
  assert.equal(result.data, input); // original reference returned
  assert.deepEqual(input, snapshot); // and it was never mutated
});

test('constraint passing on the complete new object yields the migrated tree', () => {
  const schema: SchemaNode = {
    type: 'object',
    fields: {
      lo: { type: 'number', unit: 'm' },
      hi: { type: 'number', unit: 'm' },
    },
  };
  const result = migrate(schema, [{ path: 'lo', to: 'km' }, { path: 'hi', to: 'km' }], { lo: 1, hi: 2 }, {
    constraints: [
      {
        name: 'lo<=hi',
        paths: ['lo', 'hi'],
        check: (root) => {
          const r = root as { lo: number; hi: number };
          return r.lo <= r.hi || 'inverted';
        },
      },
    ],
  });
  assert.ok(result.ok);
  assert.deepEqual(result.data, { lo: 0.001, hi: 0.002 });
});

test('inverse migration never claims to be lossless', () => {
  const schema: SchemaNode = {
    type: 'object',
    fields: { d: { type: 'number', unit: 'm', integer: true, rounding: 'trunc' } },
  };
  const forward = migrate(schema, [{ path: 'd', to: 'km' }], { d: 1500 });
  assert.ok(forward.ok);
  assert.equal((forward.data as any).d, 1); // 1.5 km truncated
  assert.ok(!forward.lossless);

  const inverse = invertMigration(compileMigration(schema, [{ path: 'd', to: 'km' }]), forward.audit);
  assert.equal(inverse.lossless, false);
  assert.match(inverse.caveat, /d/);

  // Run the inverse against a schema describing the migrated (km) shape.
  const inverseSchema: SchemaNode = {
    type: 'object',
    fields: { d: { type: 'number', unit: 'km' } },
  };
  const back = migrate(inverseSchema, inverse.rules, forward.data);
  assert.ok(back.ok);
  assert.equal((back.data as any).d, 1000); // not 1500: the loss is real
  assert.notEqual((back.data as any).d, 1500);
});

test('object key order does not affect the result', () => {
  const a = migrate(
    stationSchema,
    [{ path: 'readings[].temp', to: 'K' }],
    { name: 's', altitude: 3, readings: [{ temp: 1, spread: 2 }] },
  );
  const b = migrate(
    stationSchema,
    [{ path: 'readings[].temp', to: 'K' }],
    { readings: [{ spread: 2, temp: 1 }], altitude: 3, name: 's' },
  );
  assert.ok(a.ok && b.ok);
  assert.deepEqual(a.data, b.data);
  assert.deepEqual(a.audit, b.audit);
});

test('exact rational reference: deferred vs eager rounding over a chain', () => {
  const m = getUnit('m')!;
  const cm = getUnit('cm')!;
  const mm = getUnit('mm')!;
  const third = Rational.of(1n, 3n);
  const roundHalfUp = (v: Rational) => Rational.of(roundToInteger(v, 'half-up'));
  const analysis = analyzeChain([m, cm, mm], third, roundHalfUp);
  assert.equal(analysis.exactDeferred, '1000/3'); // 333.33… mm
  assert.equal(analysis.deferredRounded, '333');
  assert.equal(analysis.eagerRounded, '330'); // 33 cm -> 330 mm
  assert.equal(analysis.eagerVsDeferred, '-3');
});

test('exact rational reference exposes float64 pipeline error', () => {
  const degC = getUnit('degC')!;
  const K = getUnit('K')!;
  const degF = getUnit('degF')!;
  const identity = (v: Rational) => v;
  const analysis = analyzeChain([degC, K, degF], Rational.parse('0.1'), identity);
  // exact: 0.1 degC -> 273.25 K -> 32.18 degF
  assert.equal(analysis.exactDeferred, '1609/50');
  // float64 sequential computation is not exactly 32.18
  assert.notEqual(analysis.floatError, '0');
  assert.notEqual(analysis.floatSequential, 32.18);
});

test('non-representable float output is flagged in the audit', () => {
  // 1 m = 1250/381 ft: the exact rational has no finite decimal form,
  // so the float64 output cannot be exact and the audit must say so.
  const schema: SchemaNode = {
    type: 'object',
    fields: { d: { type: 'number', unit: 'm' } },
  };
  const result = migrate(schema, [{ path: 'd', to: 'ft' }], { d: 1 });
  assert.ok(result.ok);
  const entry = result.audit[0]!;
  assert.equal(entry.kind, 'float-roundtrip-loss');
  assert.equal(entry.exactValue, '1250/381');
  assert.notEqual(entry.errorInterval.lo, '0');
  assert.ok(!result.lossless);
});
