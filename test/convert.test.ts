import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseSchema } from '../src/schema.ts';
import { convertMessage } from '../src/convert.ts';

const rec = (fields: unknown[]) => ({ kind: 'record', fields });
const fld = (id: number, name: string, required: boolean, type: unknown, extra: object = {}) => ({
  id, name, required, type, ...extra,
});
const int = (min: number | string, max: number | string) => ({ kind: 'int64', min, max });
const str = (maxLen: number) => ({ kind: 'string', maxLen });
const byt = (maxLen: number) => ({ kind: 'bytes', maxLen });

const schema = (j: unknown) => {
  const r = parseSchema(j);
  if (!r.ok) throw new Error('bad test schema: ' + r.reason);
  return r.schema;
};

test('maps by field ID; names are irrelevant', () => {
  const o = schema(rec([fld(1, 'a', true, int(0, 9))]));
  const n = schema(rec([fld(1, 'renamed', true, int(0, 9))]));
  const r = convertMessage(o, n, { '1': 5 });
  assert.deepEqual(r, { ok: true, record: { fields: { '1': 5 }, unknown: {} } });
});

test('unknown fields are preserved in a separate area, top level and nested', () => {
  const o = schema(rec([fld(1, 'inner', true, rec([fld(2, 'x', true, int(0, 9))]))]));
  const n = schema(rec([fld(1, 'inner', true, rec([fld(2, 'x', true, int(0, 9))]))]));
  const r = convertMessage(o, n, { '1': { '2': 3, '7': 'mystery' }, '99': [1, 2], '01': 'non-canonical-key' });
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.record.unknown, { '99': [1, 2], '01': 'non-canonical-key' });
  assert.deepEqual(r.record.fields['1'], { fields: { '2': 3 }, unknown: { '7': 'mystery' } });
});

test('fields dropped by the new schema are preserved, not discarded', () => {
  const o = schema(rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', true, str(3))]));
  const n = schema(rec([fld(1, 'a', true, int(0, 9))]));
  const r = convertMessage(o, n, { '1': 5, '2': 'hi' });
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.record.fields, { '1': 5 });
  assert.deepEqual(r.record.unknown, { '2': 'hi' });
});

test('missing required field fails atomically: no partial record', () => {
  const o = schema(rec([fld(1, 'a', true, int(0, 9))]));
  const n = schema(rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', true, str(3))]));
  const r = convertMessage(o, n, { '1': 5 });
  assert.equal(r.ok, false);
  assert.equal('record' in r, false);
  if (!r.ok) assert.match(r.reason, /missing required field 2/);
});

test('null is never a valid value; missing is not null', () => {
  const o = schema(rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', false, str(3))]));
  const n = schema(rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', false, str(3))]));
  const r = convertMessage(o, n, { '1': 5, '2': null });
  assert.equal(r.ok, false);
});

test('absent optional field stays absent; defaults are not materialized', () => {
  const o = schema(rec([fld(1, 'a', true, int(0, 9))]));
  const n = schema(rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', false, str(3), { default: 'x' })]));
  const r = convertMessage(o, n, { '1': 5 });
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.record.fields, { '1': 5 });
});

test('invalid field value fails the whole message atomically', () => {
  const o = schema(rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', true, str(3))]));
  const n = schema(rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', true, str(3))]));
  const r = convertMessage(o, n, { '1': 5, '2': 'toolong' });
  assert.equal(r.ok, false);
  assert.equal('record' in r, false);
});

test('value valid under old but not new schema fails (defense in depth)', () => {
  // Schemas are incompatible here; the converter must still refuse.
  const o = schema(rec([fld(1, 'a', true, int(0, 100))]));
  const n = schema(rec([fld(1, 'a', true, int(0, 50))]));
  const r = convertMessage(o, n, { '1': 75 });
  assert.equal(r.ok, false);
  if (!r.ok) assert.match(r.reason, /out of range/);
});

test('integers accept number or decimal string; big values stay exact', () => {
  const o = schema(rec([fld(1, 'a', true, int('-9223372036854775808', '9223372036854775807'))]));
  const n = schema(rec([fld(1, 'a', true, int('-9223372036854775808', '9223372036854775807'))]));
  const r = convertMessage(o, n, { '1': '9223372036854775807' });
  assert.equal(r.ok, true);
  if (!r.ok) return;
  assert.deepEqual(r.record.fields, { '1': '9223372036854775807' });
  const small = convertMessage(o, n, { '1': '42' });
  assert.equal(small.ok, true);
  if (small.ok) assert.deepEqual(small.record.fields, { '1': 42 });
});

test('bytes round-trip as canonical base64', () => {
  const o = schema(rec([fld(1, 'b', true, byt(4))]));
  const n = schema(rec([fld(1, 'b', true, byt(4))]));
  const r = convertMessage(o, n, { '1': '+A==' });
  assert.equal(r.ok, true);
  if (r.ok) assert.deepEqual(r.record.fields, { '1': '+A==' });
  assert.equal(convertMessage(o, n, { '1': '!!!' }).ok, false);
});

test('regression: bytes value is never accepted as a record', () => {
  const o = schema(rec([fld(1, 'b', true, byt(0))]));
  const n = schema(rec([fld(1, 'b', true, rec([fld(2, 'x', false, int(0, 9))]))]));
  const r = convertMessage(o, n, { '1': '' });
  assert.equal(r.ok, false);
});

test('bytes to string field keeps the base64 representation', () => {
  const o = schema(rec([fld(1, 'b', true, byt(2))]));
  const n = schema(rec([fld(1, 'b', true, str(4))]));
  const r = convertMessage(o, n, { '1': '++0=' });
  assert.equal(r.ok, true);
  if (r.ok) assert.deepEqual(r.record.fields, { '1': '++0=' });
});

test('string to bytes field validates base64', () => {
  const o = schema(rec([fld(1, 's', true, str(4))]));
  const n = schema(rec([fld(1, 's', true, byt(3))]));
  assert.equal(convertMessage(o, n, { '1': '+A==' }).ok, true);
  assert.equal(convertMessage(o, n, { '1': ' ' }).ok, false);
});
