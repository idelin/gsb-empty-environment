import { test } from 'node:test';
import assert from 'node:assert/strict';
import { convertMessage } from '../src/convert.ts';
import { parseSchema } from '../src/validate.ts';
import type { Schema } from '../src/types.ts';

const S = (raw: unknown): Schema => {
  const { schema, issues } = parseSchema(raw);
  if (!schema) throw new Error('bad test schema: ' + JSON.stringify(issues));
  return schema;
};

const int = (min: string, max: string): unknown => ({ kind: 'int64', min, max });
const str = (maxLength: number): unknown => ({ kind: 'string', maxLength });
const rec = (fields: unknown[]): unknown => ({ kind: 'record', fields });
const fld = (
  id: number,
  type: unknown,
  opts: { name?: string; required?: boolean; def?: unknown } = {},
): unknown => {
  const f: Record<string, unknown> = { id, type, required: opts.required ?? true };
  if (opts.name !== undefined) f.name = opts.name;
  if ('def' in opts) f.default = opts.def;
  return f;
};

test('maps fields by ID; names are display-only', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '100'), { name: 'age' })]) });
  const to = S({ version: 2, root: rec([fld(1, int('0', '100'), { name: 'years' })]) });
  const r = convertMessage({ '1': 42 }, from, to);
  assert.deepEqual(r, { ok: true, data: { '1': 42 }, unknown: {} });
});

test('unknown fields are preserved in a separate region', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '100'))]) });
  const to = S({ version: 2, root: rec([fld(1, int('0', '100'))]) });
  const r = convertMessage({ '1': 7, '99': { anything: ['goes'] }, note: 'x' }, from, to);
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.data, { '1': 7 });
  assert.deepEqual(r.unknown, { '$/99': { anything: ['goes'] }, '$/note': 'x' });
});

test('fields dropped by the target schema are preserved, not lost', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '100')), fld(2, str(5))]) });
  const to = S({ version: 2, root: rec([fld(1, int('0', '100'))]) });
  const r = convertMessage({ '1': 7, '2': 'hi' }, from, to);
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.data, { '1': 7 });
  assert.deepEqual(r.unknown, { '$/2': 'hi' });
});

test('missing optional field stays absent; missing is not null', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '9')), fld(2, str(3), { required: false })]) });
  const to = S({ version: 2, root: rec([fld(1, int('0', '9')), fld(2, str(3), { required: false })]) });
  const r = convertMessage({ '1': 5 }, from, to);
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.data, { '1': 5 });
  assert.ok(!('2' in (r.data as object)));
});

test('missing required field fails the whole conversion (no partial record)', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '9')), fld(2, str(3), { required: false })]) });
  const to = S({ version: 2, root: rec([fld(1, int('0', '9')), fld(2, str(3), { required: true })]) });
  const r = convertMessage({ '1': 5 }, from, to);
  assert.equal(r.ok, false);
  if (r.ok) return;
  assert.equal(r.stage, 'convert');
  assert.ok(!('data' in r));
});

test('defaults are applied for absent fields', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '9'))]) });
  const to = S({
    version: 2,
    root: rec([fld(1, int('0', '9')), fld(2, int('0', '9'), { required: true, def: 4 })]),
  });
  const r = convertMessage({ '1': 5 }, from, to);
  assert.deepEqual(r, { ok: true, data: { '1': 5, '2': 4 }, unknown: {} });
});

test('invalid default in target schema fails atomically when field absent', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '9'))]) });
  const to = S({
    version: 2,
    root: rec([fld(1, int('0', '9')), fld(2, int('0', '3'), { required: false, def: 5 })]),
  });
  const r = convertMessage({ '1': 5 }, from, to);
  assert.equal(r.ok, false);
  if (r.ok) return;
  assert.match(r.errors[0].message, /default is invalid/);
});

test('atomicity: one invalid nested value fails the whole message', () => {
  const from = S({
    version: 1,
    root: rec([
      fld(1, int('0', '9')),
      fld(2, { kind: 'array', items: rec([fld(7, int('0', '10'))]), minLength: 0, maxLength: 3 }),
    ]),
  });
  const to = from;
  const bad = convertMessage({ '1': 3, '2': [{ '7': 5 }, { '7': 99 }] }, from, to);
  assert.equal(bad.ok, false);
  if (bad.ok) return;
  assert.equal(bad.stage, 'validate');
  assert.match(bad.errors[0].path, /\.\#2|#2/);
  assert.ok(!('data' in bad));
});

test('conversion against narrowed target fails atomically for offending values', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '100')), fld(2, str(2), { required: false })]) });
  const to = S({ version: 2, root: rec([fld(1, int('0', '50')), fld(2, str(2), { required: false })]) });
  const okR = convertMessage({ '1': 50, '2': 'ab' }, from, to);
  assert.equal(okR.ok, true);
  const badR = convertMessage({ '1': 51, '2': 'ab' }, from, to);
  assert.equal(badR.ok, false);
  if (badR.ok) return;
  assert.ok(!('data' in badR)); // no partial record
});

test('int64 values beyond 2^53 round-trip via decimal strings', () => {
  const big = '9223372036854775807';
  const from = S({ version: 1, root: rec([fld(1, int('-9223372036854775808', big))]) });
  const r = convertMessage({ '1': big }, from, from);
  assert.deepEqual(r, { ok: true, data: { '1': big }, unknown: {} });
});

test('nested unknown fields are preserved with their path', () => {
  const from = S({
    version: 1,
    root: rec([fld(1, rec([fld(2, int('0', '9'))]))]),
  });
  const to = from;
  const r = convertMessage({ '1': { '2': 3, '77': 'deep' } }, from, to);
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.data, { '1': { '2': 3 } });
  assert.deepEqual(r.unknown, { '$/1/77': 'deep' });
});

test('message failing source validation never produces output', () => {
  const from = S({ version: 1, root: rec([fld(1, int('0', '9'))]) });
  const r = convertMessage({ '1': 42 }, from, from);
  assert.equal(r.ok, false);
  if (r.ok) return;
  assert.equal(r.stage, 'validate');
});
