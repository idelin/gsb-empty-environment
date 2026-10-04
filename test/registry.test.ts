import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SchemaRegistry } from '../src/registry.ts';

const int = (min: string, max: string): unknown => ({ kind: 'int64', min, max });
const rec = (fields: unknown[]): unknown => ({ kind: 'record', fields });
const fld = (id: number, type: unknown, opts: { name?: string; required?: boolean } = {}): unknown => ({
  id,
  type,
  required: opts.required ?? true,
  ...(opts.name !== undefined ? { name: opts.name } : {}),
});

const v1 = { version: 1, root: rec([fld(1, int('0', '100'), { name: 'age' })]) };
const v2 = { version: 2, root: rec([fld(1, int('0', '100'), { name: 'years' })]) };
const v2narrowed = { version: 2, root: rec([fld(1, int('0', '50'), { name: 'years' })]) };

test('converters are cached per version pair', () => {
  const reg = new SchemaRegistry();
  reg.register(v1);
  reg.register(v2);
  const a = reg.getConverter(1, 2);
  const b = reg.getConverter(1, 2);
  assert.equal(a, b); // same cached instance
});

test('re-registering a version invalidates the cache', () => {
  const reg = new SchemaRegistry();
  reg.register(v1);
  reg.register(v2);
  const before = reg.getConverter(1, 2);
  reg.register(v2narrowed);
  const after = reg.getConverter(1, 2);
  assert.notEqual(before, after);
});

test('hot swap: in-flight conversion keeps the version captured at start', () => {
  const reg = new SchemaRegistry();
  reg.register(v1);
  reg.register(v2);

  // Message starts under v1 -> v2 (upper bound 100).
  const inFlight = reg.beginConversion(1, 2);

  // Hot swap: version 2 is replaced by a narrowed schema mid-flight.
  reg.register(v2narrowed);

  // The in-flight message still executes against the original v2.
  const r1 = inFlight.convert({ '1': 80 });
  assert.equal(r1.ok, true);

  // A conversion started after the swap uses the new v2 (bound 50).
  const fresh = reg.beginConversion(1, 2);
  const r2 = fresh.convert({ '1': 80 });
  assert.equal(r2.ok, false);
  const r3 = fresh.convert({ '1': 50 });
  assert.equal(r3.ok, true);
});

test('converter binds schema snapshots, not registry lookups', () => {
  const reg = new SchemaRegistry();
  reg.register(v1);
  reg.register(v2);
  const conv = reg.getConverter(1, 2);
  reg.register(v2narrowed);
  // The previously obtained converter is unaffected by the swap.
  assert.equal(conv.convert({ '1': 80 }).ok, true);
  assert.equal(reg.getConverter(1, 2).convert({ '1': 80 }).ok, false);
});

test('unknown versions are rejected', () => {
  const reg = new SchemaRegistry();
  reg.register(v1);
  assert.throws(() => reg.getConverter(1, 99), /unknown schema version/);
});

test('invalid or unsupported schemas cannot be registered', () => {
  const reg = new SchemaRegistry();
  assert.throws(() => reg.register({ version: 5, root: { kind: 'float64' } }));
  assert.throws(() => reg.register({ version: 5, root: { kind: 'string' } })); // missing maxLength
});
