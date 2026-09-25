/**
 * Runnable demo: `npm run demo` (Node >= 22.6, type stripping).
 */
import { Rational, roundToInteger } from '../src/rational.ts';
import { getUnit } from '../src/units.ts';
import { migrate, invertMigration, analyzeChain } from '../src/migrate.ts';
import { compileMigration } from '../src/compile.ts';
import type { SchemaNode } from '../src/schema.ts';

const show = (title: string, value: unknown) => {
  console.log(`\n=== ${title} ===`);
  console.log(typeof value === 'string' ? value : JSON.stringify(value, null, 2));
};

// A weather-station payload: absolute temperatures, temperature spreads,
// and an integer altitude that must stay an integer after migration.
const schema: SchemaNode = {
  type: 'object',
  fields: {
    station: { type: 'string' },
    altitudeM: { type: 'number', unit: 'm', integer: true, rounding: 'half-even' },
    readings: {
      type: 'array',
      items: {
        type: 'object',
        fields: {
          tempC: { type: 'number', unit: 'degC' },   // absolute temperature
          spreadC: { type: 'number', unit: 'dC' },   // temperature difference
        },
      },
    },
  },
};

const payload = {
  station: 'hilltop',
  altitudeM: 12,
  readings: [
    { tempC: 20, spreadC: 9 },
    { tempC: -40, spreadC: 18 },
  ],
};

const rules = [
  { path: 'readings[].tempC', to: 'K' },       // absolute: keeps the offset
  { path: 'readings[].spreadC', to: 'dF' },    // difference: no offset
  { path: 'altitudeM', to: 'mm' },             // 1 m is exactly 1000 mm
];

const result = migrate(schema, rules, payload, {
  constraints: [
    {
      name: 'spread-non-negative',
      paths: ['readings[].spreadC'],
      check: (root) => {
        const r = root as { readings: { spreadC: number }[] };
        return r.readings.every((x) => x.spreadC >= 0) || 'negative spread';
      },
    },
  ],
});

if (result.ok) {
  show('migrated object', result.data);
  show('audit (rounding loss is recorded, never silent)', result.audit);
  console.log(`\nlossless: ${result.lossless}`);
} else {
  show('migration failed and rolled back', result.error.message);
}

// Rounding loss on an integer target, with an exact error interval.
const rounding = migrate(
  {
    type: 'object',
    fields: { d: { type: 'number', unit: 'cm', integer: true, rounding: 'trunc' } },
  },
  [{ path: 'd', to: 'm' }],
  { d: 150.4 },
);
show('integer target: 150.4 cm -> m (trunc)', rounding.ok ? rounding.data : rounding.error.message);
if (rounding.ok) show('rounding audit', rounding.audit);

// Inverse migration never claims to be lossless.
const forward = migrate(
  { type: 'object', fields: { d: { type: 'number', unit: 'm', integer: true, rounding: 'trunc' } } },
  [{ path: 'd', to: 'km' }],
  { d: 1500 },
);
if (forward.ok) {
  const inverse = invertMigration(
    compileMigration(
      { type: 'object', fields: { d: { type: 'number', unit: 'm', integer: true, rounding: 'trunc' } } },
      [{ path: 'd', to: 'km' }],
    ),
    forward.audit,
  );
  show('inverse migration', {
    lossless: inverse.lossless,
    caveat: inverse.caveat,
    rules: inverse.rules,
  });
}

// Exact rational reference: multi-step chain, deferred vs eager rounding.
const analysis = analyzeChain(
  [getUnit('m')!, getUnit('cm')!, getUnit('mm')!],
  Rational.of(1n, 3n),
  (v) => Rational.of(roundToInteger(v, 'half-up')),
);
show('multi-step check: 1/3 m -> cm -> mm', analysis);

// Constraint violation rolls everything back.
const rolledBack = migrate(
  {
    type: 'object',
    fields: { lo: { type: 'number', unit: 'm' }, hi: { type: 'number', unit: 'm' } },
  },
  [{ path: 'lo', to: 'km' }, { path: 'hi', to: 'km' }],
  { lo: 2000, hi: 1000 },
  {
    constraints: [
      {
        name: 'lo<=hi',
        paths: ['lo', 'hi'],
        check: (root) => {
          const r = root as { lo: number; hi: number };
          return r.lo <= r.hi || `lo (${r.lo} km) exceeds hi (${r.hi} km)`;
        },
      },
    ],
  },
);
if (!rolledBack.ok) {
  show('constraint violation -> full rollback', {
    code: rolledBack.error.code,
    path: rolledBack.error.path,
    message: rolledBack.error.message,
    originalIntact: rolledBack.data,
  });
}
