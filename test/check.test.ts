import { test } from 'node:test';
import assert from 'node:assert/strict';
import { check } from '../src/check.ts';

const int = (min: string, max: string): unknown => ({ kind: 'int64', min, max });
const uint = (min: string, max: string): unknown => ({ kind: 'uint64', min, max });
const str = (maxLength: number): unknown => ({ kind: 'string', maxLength });
const byt = (maxLength: number): unknown => ({ kind: 'bytes', maxLength });
const arr = (items: unknown, minLength: number, maxLength: number): unknown => ({
  kind: 'array',
  items,
  minLength,
  maxLength,
});
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
const schema = (version: number, root: unknown): unknown => ({ version, root });

test('field rename (same IDs) is compatible', () => {
  const oldS = schema(1, rec([fld(1, int('0', '10'), { name: 'age' })]));
  const newS = schema(2, rec([fld(1, int('0', '10'), { name: 'years' })]));
  assert.deepEqual(check(oldS, newS), { result: 'compatible' });
});

test('numeric narrowing (upper bound) yields minimal counterexample 51', () => {
  const oldS = schema(1, rec([fld(1, int('0', '100'))]));
  const newS = schema(2, rec([fld(1, int('0', '50'))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': 51 });
  assert.equal(r.sizeBytes, '{"1":51}'.length);
  assert.deepEqual(r.path, [{ field: 1 }]);
});

test('numeric narrowing (lower bound) prefers minimal size then lexicographic', () => {
  // candidates -5..-1, all 2 bytes; canonical-lexicographic min is "-1"
  const oldS = schema(1, rec([fld(1, int('-5', '5'))]));
  const newS = schema(2, rec([fld(1, int('0', '5'))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': -1 });
});

test('uint64 to int64: counterexample is 2^63', () => {
  const oldS = schema(1, rec([fld(1, uint('0', '18446744073709551615'))]));
  const newS = schema(2, rec([fld(1, int('-9223372036854775808', '9223372036854775807'))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': '9223372036854775808' });
});

test('int64 to uint64 covering range is compatible', () => {
  const oldS = schema(1, rec([fld(1, int('0', '100'))]));
  const newS = schema(2, rec([fld(1, uint('0', '18446744073709551615'))]));
  assert.deepEqual(check(oldS, newS), { result: 'compatible' });
});

test('string maxLength narrowing: counterexample is spaces of length new+1', () => {
  const oldS = schema(1, rec([fld(1, str(5))]));
  const newS = schema(2, rec([fld(1, str(3))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': '    ' });
  assert.equal(r.sizeBytes, '{"1":"    "}'.length); // 12 bytes
});

test('bytes maxLength narrowing: counterexample is zero bytes', () => {
  const oldS = schema(1, rec([fld(1, byt(2))]));
  const newS = schema(2, rec([fld(1, byt(1))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': '0000' });
});

test('array minLength raised: counterexample is the shortest old array', () => {
  const oldS = schema(1, rec([fld(1, arr({ kind: 'bool' }, 0, 3))]));
  const newS = schema(2, rec([fld(1, arr({ kind: 'bool' }, 2, 3))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': [] });
});

test('array maxLength narrowed', () => {
  const oldS = schema(1, rec([fld(1, arr({ kind: 'bool' }, 0, 3))]));
  const newS = schema(2, rec([fld(1, arr({ kind: 'bool' }, 0, 2))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': [false, false, false] });
});

test('array item narrowing: single offending item at minimal length', () => {
  const oldS = schema(1, rec([fld(1, arr(int('0', '10'), 0, 2))]));
  const newS = schema(2, rec([fld(1, arr(int('0', '5'), 0, 2))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': [6] });
  assert.deepEqual(r.path, [{ field: 1 }, 'items']);
});

test('optional to required is incompatible; counterexample omits the field', () => {
  const oldS = schema(1, rec([fld(1, int('0', '9')), fld(2, str(3), { required: false })]));
  const newS = schema(2, rec([fld(1, int('0', '9')), fld(2, str(3), { required: true })]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': 0 });
  assert.deepEqual(r.path, [{ field: 2 }]);
});

test('default invalidation: narrowed type keeps out-of-range default', () => {
  const oldS = schema(1, rec([fld(1, int('0', '10'), { required: false, def: 5 })]));
  const newS = schema(2, rec([fld(1, int('0', '3'), { required: false, def: 5 })]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  // minimal witness: the empty record (absence was accepted, no longer is)
  assert.deepEqual(r.counterexample, {});
  assert.deepEqual(r.path, [{ field: 1 }]);
});

test('new field with valid default keeps compatibility', () => {
  const oldS = schema(1, rec([fld(1, int('0', '9'))]));
  const newS = schema(2, rec([fld(1, int('0', '9')), fld(2, int('0', '9'), { required: true, def: 3 })]));
  assert.deepEqual(check(oldS, newS), { result: 'compatible' });
});

test('new required field without default is incompatible', () => {
  const oldS = schema(1, rec([fld(1, int('0', '9'))]));
  const newS = schema(2, rec([fld(1, int('0', '9')), fld(2, int('0', '9'), { required: true })]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': 0 });
  assert.deepEqual(r.path, [{ field: 2 }]);
});

test('dropped field stays compatible (data preserved as unknown)', () => {
  const oldS = schema(1, rec([fld(1, int('0', '9')), fld(2, str(2))]));
  const newS = schema(2, rec([fld(1, int('0', '9'))]));
  assert.deepEqual(check(oldS, newS), { result: 'compatible' });
});

test('deep error path through record -> array -> record', () => {
  const inner = (mx: string): unknown =>
    rec([fld(2, arr(rec([fld(7, int('0', mx))]), 0, 2), { name: 'items' })]);
  const oldS = schema(1, inner('10'));
  const newS = schema(2, inner('5'));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '2': [{ '7': 6 }] });
  assert.deepEqual(r.path, [{ field: 2, name: 'items' }, 'items', { field: 7 }]);
});

test('kind change bool -> int64 is incompatible with minimal bool witness', () => {
  const oldS = schema(1, rec([fld(1, { kind: 'bool' })]));
  const newS = schema(2, rec([fld(1, int('0', '9'))]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': false });
});

test('unsupported: unknown type kind', () => {
  const oldS = schema(1, rec([fld(1, { kind: 'float64' })]));
  const newS = schema(2, rec([fld(1, { kind: 'float64' })]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'unsupported');
});

test('unsupported: reference mechanisms (recursion risk)', () => {
  const oldS = schema(1, { kind: 'record', fields: [], $ref: '#/types/node' });
  const newS = schema(2, rec([]));
  const r = check(oldS, newS);
  assert.equal(r.result, 'unsupported');
});

test('unsupported: missing explicit bounds', () => {
  const noMax = schema(1, rec([fld(1, { kind: 'string' })]));
  const ok = schema(2, rec([fld(1, str(1))]));
  assert.equal(check(noMax, ok).result, 'unsupported');

  const noRange = schema(1, rec([fld(1, { kind: 'int64' })]));
  assert.equal(check(noRange, ok).result, 'unsupported');

  const noArrMax = schema(1, rec([fld(1, { kind: 'array', items: { kind: 'bool' }, minLength: 0 })]));
  assert.equal(check(noArrMax, ok).result, 'unsupported');
});

test('unsupported: unknown extra keyword (cannot reason about it)', () => {
  const oldS = schema(1, rec([fld(1, { kind: 'string', maxLength: 3, pattern: '^a+$' })]));
  const newS = schema(2, rec([fld(1, str(3))]));
  assert.equal(check(oldS, newS).result, 'unsupported');
});

test('unsupported: missing explicit required flag', () => {
  const oldS = schema(1, rec([{ id: 1, type: int('0', '9') }]));
  const newS = schema(2, rec([fld(1, int('0', '9'))]));
  assert.equal(check(oldS, newS).result, 'unsupported');
});

test('unsupported: structurally invalid schema (duplicate ids, min > max)', () => {
  const dup = schema(1, rec([fld(1, int('0', '9')), fld(1, int('0', '9'))]));
  const ok = schema(2, rec([fld(1, int('0', '9'))]));
  assert.equal(check(dup, ok).result, 'unsupported');

  const inverted = schema(1, rec([fld(1, int('9', '0'))]));
  assert.equal(check(inverted, ok).result, 'unsupported');
});

test('array with maxLength 0: item type is unobservable, stays compatible', () => {
  const oldS = schema(1, rec([fld(1, arr(int('0', '10'), 0, 0))]));
  const newS = schema(2, rec([fld(1, arr(int('0', '5'), 0, 0))]));
  assert.deepEqual(check(oldS, newS), { result: 'compatible' });
});

test('widening is compatible', () => {
  const oldS = schema(1, rec([fld(1, int('0', '5')), fld(2, str(2)), fld(3, arr({ kind: 'bool' }, 1, 2))]));
  const newS = schema(2, rec([fld(1, int('-10', '50')), fld(2, str(10)), fld(3, arr({ kind: 'bool' }, 0, 9))]));
  assert.deepEqual(check(oldS, newS), { result: 'compatible' });
});
