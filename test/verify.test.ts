import { test } from 'node:test';
import assert from 'node:assert/strict';
import { verify } from '../src/verify.ts';

const int = (min: string, max: string): unknown => ({ kind: 'int64', min, max });
const uint = (min: string, max: string): unknown => ({ kind: 'uint64', min, max });
const str = (maxLength: number): unknown => ({ kind: 'string', maxLength });
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

function assertConsistent(oldS: unknown, newS: unknown, expect: string): void {
  const r = verify(oldS, newS);
  assert.equal(r.checkResult, expect, r.problems.join('; '));
  assert.equal(r.consistent, true, r.problems.join('; '));
  assert.equal(r.exhaustive, true, 'expected exhaustive enumeration');
}

test('exhaustive verification: rename is compatible', () => {
  const oldS = schema(1, rec([fld(1, int('0', '3'), { name: 'a' })]));
  const newS = schema(2, rec([fld(1, int('0', '3'), { name: 'b' })]));
  assertConsistent(oldS, newS, 'compatible');
});

test('exhaustive verification: narrowing with minimal counterexample', () => {
  const oldS = schema(1, rec([fld(1, int('0', '4'))]));
  const newS = schema(2, rec([fld(1, int('0', '2'))]));
  assertConsistent(oldS, newS, 'incompatible');
});

test('exhaustive verification: optional -> required', () => {
  const oldS = schema(1, rec([fld(1, int('0', '2')), fld(2, str(1), { required: false })]));
  const newS = schema(2, rec([fld(1, int('0', '2')), fld(2, str(1), { required: true })]));
  assertConsistent(oldS, newS, 'incompatible');
});

test('exhaustive verification: default invalidation', () => {
  const oldS = schema(1, rec([fld(1, int('0', '4'), { required: false, def: 3 })]));
  const newS = schema(2, rec([fld(1, int('0', '2'), { required: false, def: 3 })]));
  assertConsistent(oldS, newS, 'incompatible');
});

test('exhaustive verification: deep path (record > array > record)', () => {
  const inner = (mx: string): unknown =>
    rec([fld(2, arr(rec([fld(7, int('0', mx))]), 0, 1))]);
  assertConsistent(schema(1, inner('3')), schema(2, inner('1')), 'incompatible');
});

test('exhaustive verification: string narrowing over finite alphabet', () => {
  const oldS = schema(1, rec([fld(1, str(2))]));
  const newS = schema(2, rec([fld(1, str(1))]));
  const r = verify(oldS, newS, { stringAlphabet: [' ', 'a'] });
  assert.equal(r.checkResult, 'incompatible');
  assert.equal(r.consistent, true, r.problems.join('; '));
  assert.equal(r.exhaustive, true);
});

test('exhaustive verification: array bound changes', () => {
  const oldS = schema(1, rec([fld(1, arr({ kind: 'bool' }, 0, 2))]));
  const newS = schema(2, rec([fld(1, arr({ kind: 'bool' }, 1, 2))]));
  assertConsistent(oldS, newS, 'incompatible');
  const newS2 = schema(3, rec([fld(1, arr({ kind: 'bool' }, 0, 3))]));
  assertConsistent(oldS, newS2, 'compatible');
});

test('exhaustive verification: cross-kind numeric ranges', () => {
  const oldS = schema(1, rec([fld(1, uint('0', '10'))]));
  const newS = schema(2, rec([fld(1, int('0', '20'))]));
  assertConsistent(oldS, newS, 'compatible');
  const newS2 = schema(3, rec([fld(1, int('0', '5'))]));
  assertConsistent(oldS, newS2, 'incompatible');
});

test('exhaustive sweep: all small integer range pairs are verified', () => {
  // 28 ranges [a,b] with -3 <= a <= b <= 3 -> 784 ordered pairs
  const ranges: [number, number][] = [];
  for (let a = -3; a <= 3; a++) for (let b = a; b <= 3; b++) ranges.push([a, b]);
  let checked = 0;
  for (const [a, b] of ranges) {
    for (const [c, d] of ranges) {
      const oldS = schema(1, rec([fld(1, int(String(a), String(b)))]));
      const newS = schema(2, rec([fld(1, int(String(c), String(d)))]));
      const r = verify(oldS, newS);
      assert.equal(r.exhaustive, true);
      assert.equal(r.consistent, true, `[${a},${b}] vs [${c},${d}]: ${r.problems.join('; ')}`);
      assert.equal(r.checkResult, c <= a && b <= d ? 'compatible' : 'incompatible');
      checked++;
    }
  }
  assert.equal(checked, 784);
});

test('unsupported schemas are reported, never silently compatible', () => {
  const bad = schema(1, rec([fld(1, { kind: 'string' })]));
  const ok = schema(2, rec([fld(1, str(1))]));
  const r = verify(bad, ok);
  assert.equal(r.checkResult, 'unsupported');
  assert.equal(r.consistent, false);
});
