import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkCompat } from '../src/check.ts';

const rec = (fields: unknown[]) => ({ kind: 'record', fields });
const fld = (id: number, name: string, required: boolean, type: unknown, extra: object = {}) => ({
  id, name, required, type, ...extra,
});
const int = (min: number | string, max: number | string) => ({ kind: 'int64', min, max });
const uint = (min: number | string, max: number | string) => ({ kind: 'uint64', min, max });
const str = (maxLen: number) => ({ kind: 'string', maxLen });
const byt = (maxLen: number) => ({ kind: 'bytes', maxLen });
const arr = (element: unknown, maxLen: number) => ({ kind: 'array', element, maxLen });

test('field rename with same IDs is compatible (names are display-only)', () => {
  const oldS = rec([fld(1, 'userName', true, str(10)), fld(2, 'age', true, int(0, 150))]);
  const newS = rec([fld(1, 'displayName', true, str(10)), fld(2, 'years', true, int(0, 150))]);
  assert.deepEqual(checkCompat(oldS, newS), { result: 'compatible' });
});

test('int range narrowing: upper bound', () => {
  const r = checkCompat(rec([fld(1, 'n', true, int(0, 100))]), rec([fld(1, 'n', true, int(0, 50))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': 51 });
  assert.equal(r.encoding, '{"1":51}');
  assert.equal(r.path, '.1(n)');
});

test('int range narrowing: lower bound and both bounds', () => {
  const lower = checkCompat(rec([fld(1, 'n', true, int(0, 100))]), rec([fld(1, 'n', true, int(10, 100))]));
  assert.equal(lower.result, 'incompatible');
  if (lower.result === 'incompatible') assert.equal(lower.encoding, '{"1":0}');
  const both = checkCompat(rec([fld(1, 'n', true, int(0, 100))]), rec([fld(1, 'n', true, int(10, 50))]));
  assert.equal(both.result, 'incompatible');
  // {"1":0} (7 bytes) beats {"1":51} (8 bytes)
  if (both.result === 'incompatible') assert.equal(both.encoding, '{"1":0}');
});

test('range widening is compatible', () => {
  const r = checkCompat(rec([fld(1, 'n', true, int(5, 50))]), rec([fld(1, 'n', true, int(0, 100))]));
  assert.deepEqual(r, { result: 'compatible' });
});

test('string maxLen narrowing: counterexample is spaces of new maxLen + 1', () => {
  const r = checkCompat(rec([fld(1, 's', true, str(5))]), rec([fld(1, 's', true, str(3))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': '    ' });
  assert.equal(r.encoding, '{"1":"    "}');
});

test('bytes maxLen narrowing: counterexample is lexicographically minimal base64', () => {
  const r = checkCompat(rec([fld(1, 'b', true, byt(2))]), rec([fld(1, 'b', true, byt(1))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': '++0=' });
  assert.equal(r.encoding, '{"1":"++0="}');
});

test('array maxLen narrowing', () => {
  const r = checkCompat(rec([fld(1, 'a', true, arr(int(0, 9), 3))]), rec([fld(1, 'a', true, arr(int(0, 9), 1))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': [0, 0] });
  assert.equal(r.encoding, '{"1":[0,0]}');
});

test('array element narrowing', () => {
  const r = checkCompat(rec([fld(1, 'a', true, arr(int(0, 9), 2))]), rec([fld(1, 'a', true, arr(int(0, 5), 2))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': [6] });
});

test('optional to required is incompatible; counterexample omits the field', () => {
  const r = checkCompat(rec([fld(1, 'n', false, int(0, 9))]), rec([fld(1, 'n', true, int(0, 9))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, {});
  assert.equal(r.encoding, '{}');
});

test('required to optional is compatible', () => {
  const r = checkCompat(rec([fld(1, 'n', true, int(0, 9))]), rec([fld(1, 'n', false, int(0, 9))]));
  assert.deepEqual(r, { result: 'compatible' });
});

test('new required field without old guarantee is incompatible', () => {
  const oldS = rec([fld(1, 'a', true, int(0, 9))]);
  const newS = rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', true, str(3))]);
  const r = checkCompat(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': 0 });
  assert.equal(r.path, '.2(b)');
});

test('new optional field is compatible', () => {
  const oldS = rec([fld(1, 'a', true, int(0, 9))]);
  const newS = rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', false, str(3))]);
  assert.deepEqual(checkCompat(oldS, newS), { result: 'compatible' });
});

test('dropped field is compatible (preserved in unknown area by converter)', () => {
  const oldS = rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', true, str(3))]);
  const newS = rec([fld(1, 'a', true, int(0, 9))]);
  assert.deepEqual(checkCompat(oldS, newS), { result: 'compatible' });
});

test('default invalidation: changed default', () => {
  const oldS = rec([fld(1, 'n', false, int(0, 9), { default: 5 })]);
  const newS = rec([fld(1, 'n', false, int(0, 9), { default: 6 })]);
  const r = checkCompat(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, {});
  assert.match(r.reason, /default/);
});

test('default invalidation: removed default', () => {
  const oldS = rec([fld(1, 'n', false, int(0, 9), { default: 5 })]);
  const newS = rec([fld(1, 'n', false, int(0, 9))]);
  assert.equal(checkCompat(oldS, newS).result, 'incompatible');
});

test('default invalidation: default outside narrowed range beats narrowing counterexample', () => {
  const oldS = rec([fld(1, 'n', false, int(0, 9), { default: 5 })]);
  const newS = rec([fld(1, 'n', false, int(0, 3), { default: 2 })]);
  const r = checkCompat(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  // "{}" (2 bytes) beats {"1":4} (7 bytes)
  assert.equal(r.encoding, '{}');
  assert.match(r.reason, /default/);
});

test('preserved default is compatible', () => {
  const oldS = rec([fld(1, 'n', false, int(0, 9), { default: 5 })]);
  const newS = rec([fld(1, 'n', false, int(0, 9), { default: 5 })]);
  assert.deepEqual(checkCompat(oldS, newS), { result: 'compatible' });
});

test('new schema whose own default is invalid is unsupported, not compatible', () => {
  const oldS = rec([fld(1, 'n', false, int(0, 9), { default: 5 })]);
  const newS = rec([fld(1, 'n', false, int(0, 3), { default: 5 })]);
  const r = checkCompat(oldS, newS);
  assert.equal(r.result, 'unsupported');
  if (r.result === 'unsupported') assert.match(r.reason, /default/);
});

test('deep error path through record -> array -> record', () => {
  const leaf = (max: number) =>
    rec([
      fld(1, 'one', true, rec([fld(2, 'two', true, arr(rec([fld(3, 'three', true, int(0, max))]), 2))])),
    ]);
  const r = checkCompat(leaf(9), leaf(5));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.equal(r.path, '.1(one).2(two)[].3(three)');
  assert.deepEqual(r.counterexample, { '1': { '2': [{ '3': 6 }] } });
  assert.equal(r.encoding, '{"1":{"2":[{"3":6}]}}');
});

test('type kind change is incompatible with minimal old value', () => {
  const r = checkCompat(rec([fld(1, 'x', true, str(4))]), rec([fld(1, 'x', true, int(0, 9))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': '' });
});

test('numeric cross-kind: int64 to uint64 requires non-negative coverage', () => {
  const ok = checkCompat(rec([fld(1, 'n', true, uint(0, 10))]), rec([fld(1, 'n', true, int(0, 20))]));
  assert.deepEqual(ok, { result: 'compatible' });
  const bad = checkCompat(rec([fld(1, 'n', true, int(-5, 5))]), rec([fld(1, 'n', true, uint(0, 10))]));
  assert.equal(bad.result, 'incompatible');
  if (bad.result === 'incompatible') assert.equal(bad.encoding, '{"1":-1}');
});

test('uint64 max beyond int64 max is caught, with bigint-exact counterexample', () => {
  const oldS = rec([fld(1, 'n', true, uint(0, '18446744073709551615'))]);
  const newS = rec([fld(1, 'n', true, int('' + -(2 ** 53), '9223372036854775807'))]);
  const r = checkCompat(oldS, newS);
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': '9223372036854775808' });
  assert.equal(r.encoding, '{"1":9223372036854775808}');
});

test('unsupported: unknown keyword, recursion, missing bounds, bad defaults', () => {
  const base = () => rec([fld(1, 'n', true, int(0, 9))]);
  // unknown keyword (e.g. a pattern rule we cannot check)
  const withPattern = rec([fld(1, 's', true, { kind: 'string', maxLen: 5, pattern: '^a+$' })]);
  assert.equal(checkCompat(base(), withPattern).result, 'unsupported');
  // recursive reference
  const recursive = rec([fld(1, 'next', false, { kind: 'record', fields: [], $ref: '#' })]);
  assert.equal(checkCompat(base(), recursive).result, 'unsupported');
  // missing maxLen
  const noBound = rec([fld(1, 's', true, { kind: 'string' })]);
  assert.equal(checkCompat(base(), noBound).result, 'unsupported');
  const noArrBound = rec([fld(1, 'a', true, { kind: 'array', element: int(0, 1) })]);
  assert.equal(checkCompat(base(), noArrBound).result, 'unsupported');
  // default on required field
  const defReq = rec([fld(1, 'n', true, int(0, 9), { default: 3 })]);
  assert.equal(checkCompat(base(), defReq).result, 'unsupported');
  // default not validating
  const badDef = rec([fld(1, 'n', false, int(0, 9), { default: 42 })]);
  assert.equal(checkCompat(base(), badDef).result, 'unsupported');
  // duplicate field id
  const dup = rec([fld(1, 'a', true, int(0, 9)), fld(1, 'b', true, int(0, 9))]);
  assert.equal(checkCompat(base(), dup).result, 'unsupported');
  // min > max
  const badRange = rec([fld(1, 'n', true, int(9, 0))]);
  assert.equal(checkCompat(base(), badRange).result, 'unsupported');
  // range outside int64 domain
  const outside = rec([fld(1, 'n', true, int(0, '18446744073709551615'))]);
  assert.equal(checkCompat(base(), outside).result, 'unsupported');
  // missing explicit required
  const noRequired = rec([{ id: 1, name: 'n', type: int(0, 9) }]);
  assert.equal(checkCompat(base(), noRequired).result, 'unsupported');
});

test('unsupported old schema is also reported, never defaults to compatible', () => {
  const bad = rec([fld(1, 's', true, { kind: 'string' })]);
  const good = rec([fld(1, 's', true, str(1))]);
  const r = checkCompat(bad, good);
  assert.equal(r.result, 'unsupported');
  if (r.result === 'unsupported') assert.match(r.reason, /^old schema:/);
});

test('string to bytes: empty-only string is covered, otherwise minimal non-base64 string', () => {
  const emptyOnly = checkCompat(rec([fld(1, 's', true, str(0))]), rec([fld(1, 's', true, byt(0))]));
  assert.deepEqual(emptyOnly, { result: 'compatible' });
  const r = checkCompat(rec([fld(1, 's', true, str(2))]), rec([fld(1, 's', true, byt(8))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.deepEqual(r.counterexample, { '1': ' ' });
  assert.equal(r.encoding, '{"1":" "}');
});

test('bytes to string: only base64 lengths that fit are covered', () => {
  // 1 byte -> 4 base64 chars, fits in maxLen 4
  const ok = checkCompat(rec([fld(1, 'b', true, byt(1))]), rec([fld(1, 'b', true, str(4))]));
  assert.deepEqual(ok, { result: 'compatible' });
  // 4 bytes -> 8 base64 chars, exceeds maxLen 4
  const r = checkCompat(rec([fld(1, 'b', true, byt(4))]), rec([fld(1, 'b', true, str(4))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.equal(r.encoding, '{"1":"+++++A=="}');
});

test('bytes narrowing: minimal counterexample within a shared base64-length group', () => {
  // bad lengths are {2, 3}; both base64-encode to 4 chars, and "++++" (3
  // bytes) is lexicographically smaller than "++0=" (2 bytes).
  const r = checkCompat(rec([fld(1, 'b', true, byt(3))]), rec([fld(1, 'b', true, byt(1))]));
  assert.equal(r.result, 'incompatible');
  if (r.result !== 'incompatible') return;
  assert.equal(r.encoding, '{"1":"++++"}');
  // bytes -> string with the same grouping
  const r2 = checkCompat(rec([fld(1, 'b', true, byt(3))]), rec([fld(1, 'b', true, str(3))]));
  assert.equal(r2.result, 'incompatible');
  if (r2.result === 'incompatible') assert.equal(r2.encoding, '{"1":"++++"}');
});
