import assert from 'node:assert/strict';
import { validate } from './validator.ts';
import { ErrorDigest } from './digest.ts';
import { encode, decode } from './serialize.ts';
import { spaceReport } from './report.ts';
import type { Schema, ValidationError } from './types.ts';

const compactTemplate = (t: {
  kind: string;
  prefix?: unknown[];
  suffix?: unknown[];
  runs?: Array<{ start: number; end: number; idStart?: number; ids?: unknown[] }>;
  path?: unknown[];
  ids?: unknown[];
}) =>
  t.kind === 'range'
    ? {
        kind: t.kind,
        prefix: t.prefix,
        suffix: t.suffix,
        runs: t.runs!.map((r) => ({
          start: r.start,
          end: r.end,
          ids: r.idStart !== undefined ? `idStart=${r.idStart}` : `${r.ids!.length} explicit id lists`,
        })),
      }
    : t;

const itemSchema = (minimum: number): Schema => ({
  type: 'object',
  required: ['id', 'name'],
  properties: {
    id: { type: 'integer', minimum },
    name: { type: 'string', minLength: 1, pattern: '^[a-z]+$' },
    tag: { enum: ['a', 'b', 'c'] },
  },
});

const schemaV1: Schema = { type: 'array', items: itemSchema(1) };
const schemaV2: Schema = { type: 'array', items: itemSchema(5) };

const instance: unknown[] = [];
for (let i = 0; i < 120; i++) {
  instance.push({ id: i + 1, name: 'ok', tag: 'a' });
}
for (let i = 1; i <= 100; i++) {
  (instance[i] as Record<string, unknown>).id = 0; // violates minimum in v1 and v2
}
for (let i = 10; i <= 20; i++) {
  if (i === 15) continue; // a gap: ranges must NOT be written as continuous
  (instance[i] as Record<string, unknown>).name = ''; // violates minLength and pattern
}
(instance[0] as Record<string, unknown>).id = 'zero'; // violates type
(instance[5] as Record<string, unknown>).tag = 'nope'; // violates enum

const rawErrors: ValidationError[] = [
  ...validate(instance, schemaV1, 'v1'),
  ...validate(instance, schemaV2, 'v2'),
];

const digest = new ErrorDigest();
const idByError = new Map<string, number>();
for (const e of rawErrors) {
  idByError.set(JSON.stringify(e), digest.add(e));
}

console.log('=== Summary (grouped by schemaVersion / keyword / rule path) ===');
console.table(digest.summaries());

const doc1 = encode(digest);
const minimumGroup = doc1.groups.find((g) => g.keyword === 'minimum' && g.schemaVersion === 'v1')!;
console.log('=== v1 minimum group: items 1..100 compressed into one range ===');
console.log(JSON.stringify(minimumGroup.templates.map(compactTemplate), null, 2));

const minLengthGroup = doc1.groups.find((g) => g.keyword === 'minLength' && g.schemaVersion === 'v1')!;
console.log('=== v1 minLength group: item 15 missing, so two ranges (no fake continuity) ===');
console.log(JSON.stringify(minLengthGroup.templates.map(compactTemplate), null, 2));

// Online retract: withdraw the v1 minimum error at item 50 without rebuilding anything.
const victim = rawErrors.find(
  (e) => e.schemaVersion === 'v1' && e.keyword === 'minimum' && e.instancePath.join('/') === '50/id',
)!;
digest.remove(idByError.get(JSON.stringify(victim))!);
rawErrors.splice(rawErrors.indexOf(victim), 1);

const doc2 = encode(digest);
const minimumAfter = doc2.groups.find((g) => g.keyword === 'minimum' && g.schemaVersion === 'v1')!;
console.log('=== After retracting item 50 (v1 minimum): range splits into [1,49] and [51,100] ===');
console.log(JSON.stringify(minimumAfter.templates.map(compactTemplate), null, 2));

// Conservation: expansion must equal the remaining raw error set exactly.
const canonical = (e: ValidationError) =>
  JSON.stringify({
    schemaVersion: e.schemaVersion,
    keyword: e.keyword,
    schemaPath: e.schemaPath,
    message: e.message,
    instancePath: e.instancePath,
  });
const asMultiset = (xs: ValidationError[]) => xs.map(canonical).sort();
assert.deepEqual(asMultiset(digest.expandAll()), asMultiset(rawErrors));
assert.equal(digest.count, rawErrors.length);
assert.equal(digest.summaries().reduce((a, s) => a + s.count, 0), rawErrors.length);
console.log(`conservation check: digest.count = ${digest.count} == raw errors = ${rawErrors.length} OK`);

// Round trip: decompress(serialize(x)) == original error set.
const restored = decode(doc2);
assert.deepEqual(asMultiset(restored), asMultiset(rawErrors));
console.log(`round-trip check: decode(encode(digest)) = ${restored.length} errors, identical OK`);

// Random access expansions.
console.log('\n=== Expansion samples ===');
console.log('by rule (v2 minimum), first 3:', JSON.stringify(digest.expandByRule('minimum', 'v2').slice(0, 3), null, 1));
console.log('by prefix [10] (item 10):', JSON.stringify(digest.expandByPrefix([10]), null, 1));
const inPathOrder = digest.expandAll('path');
console.log('first 3 in path order:', JSON.stringify(inPathOrder.slice(0, 3), null, 1));

// Space savings.
const report = spaceReport(rawErrors, doc2);
console.log('\n=== Space report ===');
console.table({
  errors: report.errorCount,
  'raw bytes': report.rawBytes,
  'compressed bytes': report.compressedBytes,
  'saved bytes': report.savedBytes,
  'compressed ratio': `${(report.compressedRatio * 100).toFixed(1)}%`,
});
