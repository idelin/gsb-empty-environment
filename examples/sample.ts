/**
 * End-to-end demo: schema-driven unit migration of a weather-station
 * payload, with exact rational arithmetic, integer rounding audits,
 * cross-field constraints with atomic rollback, inverse migration and a
 * deferred-vs-stepwise rounding comparison.
 *
 * Run with: npm run sample
 */
import {
  UnitRegistry,
  chainExact,
  compilePlan,
  deferredVsStepwise,
  formatRational,
  invertPlan,
  migrate,
  type JsonValue,
} from '../src/index.ts';

const show = (title: string, value: unknown): void => {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(value, null, 2));
};

// --- 1. Units: dimensions, affine offsets, absolute vs delta -------------
const registry = new UnitRegistry(
  [
    { name: 'm', dimension: 'length' },
    { name: 'mm', dimension: 'length' },
    { name: 'km', dimension: 'length' },
    { name: 'ft', dimension: 'length' },
    { name: 'K', dimension: 'temperature' },
    { name: 'C', dimension: 'temperature' },
    { name: 'F', dimension: 'temperature' },
    { name: 'u1', dimension: 'demo' },
    { name: 'u2', dimension: 'demo' },
    { name: 'u3', dimension: 'demo' },
  ],
  [
    { from: 'm', to: 'mm', ratio: 1000 },
    { from: 'km', to: 'm', ratio: 1000 },
    { from: 'ft', to: 'm', ratio: '381/1250' }, // 0.3048 exactly
    { from: 'C', to: 'K', ratio: 1, offset: '273.15' },
    { from: 'F', to: 'C', ratio: '5/9', offset: '-160/9' }, // C = (F - 32) * 5/9
    { from: 'u1', to: 'u2', ratio: '2/3' },
    { from: 'u2', to: 'u3', ratio: '2/3' },
  ],
);

// Absolute vs delta temperature: the affine offset applies only to absolute.
show('absolute vs delta temperature', {
  '20 C (absolute) -> K': formatRational(registry.convert(20, 'C', 'K', 'absolute')),
  'delta 20 C -> delta K': formatRational(registry.convert(20, 'C', 'K', 'delta')),
  '20 C (absolute) -> F': formatRational(registry.convert(20, 'C', 'F', 'absolute')),
  'delta 20 C -> delta F': formatRational(registry.convert(20, 'C', 'F', 'delta')),
});

// --- 2. Plan: nested arrays, integer precision, cross-field constraint ---
const plan = compilePlan(registry, {
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
        const min = Number(env.minTempC);
        const max = Number(env.maxTempC);
        return min < max ? null : { path: '$.envelope', message: `min ${min} K must be below max ${max} K` };
      },
    },
    {
      name: 'sensors-within-envelope',
      check: (root: JsonValue) => {
        const env = (root as Record<string, JsonValue>).envelope as Record<string, JsonValue>;
        const sensors = (root as Record<string, JsonValue>).sensors as Record<string, JsonValue>[];
        for (const s of sensors) {
          const t = Number(s.tempC);
          if (!(t >= Number(env.minTempC) && t <= Number(env.maxTempC))) {
            return { path: `$.sensors`, message: `sensor ${s.id} reads ${t} K, outside the envelope` };
          }
        }
        return null;
      },
    },
  ],
});

const payload = {
  station: 'alpha',
  sensors: [
    { id: 's1', tempC: 20, tempDeltaC: '1.5', altitudeM: 1 },
    { id: 's2', tempC: '22.4', tempDeltaC: 2, altitudeM: '0.5004' },
  ],
  envelope: { minTempC: 15, maxTempC: 30 },
};

const result = migrate(plan, payload);
if (!result.ok) throw new Error('unexpected migration failure');
show('migrated object (1 m -> exactly 1000 mm; 20 C -> 293.15 K)', result.value);
show('audit trail (rounding loss is recorded, never silently truncated)', result.audit);

// --- 3. Inverse migration: honest about loss -----------------------------
const inverse = invertPlan(plan, result);
const back = migrate(inverse, result.value);
if (!back.ok) throw new Error('unexpected inverse failure');
show('inverse migration of the migrated object', back.value);
show(
  'inverse audit: only the path that lost information is marked lossless:false',
  back.audit.filter((a) => a.to === 'm'),
);

// --- 4. Constraint failure: atomic rollback ------------------------------
const badPayload = { ...payload, envelope: { minTempC: 15, maxTempC: 10 } };
const failed = migrate(plan, badPayload);
show(
  'constraint failure -> rolled back (no partial output)',
  failed.ok ? failed : failed.errors.map((e) => ({ code: e.code, path: e.path, message: e.message })),
);
console.log('input object untouched by the failed migration:', JSON.stringify(badPayload.envelope));

// --- 5. Multi-step conversion: deferred vs stepwise rounding -------------
const chain = chainExact(registry, 1, ['m', 'km', 'mm'], 'absolute');
show('1 m -> km -> mm, exact at every step (cross-checked against direct m -> mm)', {
  steps: chain.steps.map((s) => `${formatRational(s.value)} ${s.unit}`),
});

const cmp = deferredVsStepwise(registry, 8, ['u1', 'u2', 'u3'], 'absolute', 'half-up');
show('8 u1 -> u2 -> u3: deferred rounding vs rounding at every step', {
  exact: formatRational(cmp.exact),
  deferred: formatRational(cmp.deferred),
  stepwise: formatRational(cmp.stepwise),
  difference: formatRational(cmp.difference),
});
