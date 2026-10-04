import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SchemaRegistry } from '../src/registry.ts';

const rec = (fields: unknown[]) => ({ kind: 'record', fields });
const fld = (id: number, name: string, required: boolean, type: unknown) => ({ id, name, required, type });
const int = (min: number, max: number) => ({ kind: 'int64', min, max });
const str = (maxLen: number) => ({ kind: 'string', maxLen });

const oldS = rec([fld(1, 'a', true, int(0, 9))]);
const newV1 = rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', false, str(3))]);
const newV2 = rec([fld(1, 'a', true, int(0, 9)), fld(2, 'b', true, str(3))]);

test('versions are immutable once registered', () => {
  const reg = new SchemaRegistry();
  assert.equal(reg.register(1, oldS, newV1).ok, true);
  const again = reg.register(1, oldS, newV1);
  assert.equal(again.ok, false);
  if (!again.ok) assert.match(again.reason, /immutable/);
});

test('setCurrent rejects unknown versions', () => {
  const reg = new SchemaRegistry();
  reg.register(1, oldS, newV1);
  assert.throws(() => reg.setCurrent(99), /unknown schema version/);
});

test('invalid schemas are rejected at registration', () => {
  const reg = new SchemaRegistry();
  const bad = rec([fld(1, 's', true, { kind: 'string' })]);
  const r = reg.register(1, oldS, bad);
  assert.equal(r.ok, false);
  assert.equal(reg.currentVersion, null);
});

test('hot swap: in-flight sessions keep the version they started with', () => {
  const reg = new SchemaRegistry();
  reg.register(1, oldS, newV1);
  const inFlight = reg.begin(); // pinned to v1
  reg.register(2, oldS, newV2);
  reg.setCurrent(2); // hot swap
  const fresh = reg.begin(); // pinned to v2

  assert.equal(inFlight.version, 1);
  assert.equal(fresh.version, 2);

  const msg = { '1': 5 }; // lacks field 2: fine under v1, missing-required under v2
  const r1 = inFlight.convert(msg);
  const r2 = fresh.convert(msg);
  assert.equal(r1.ok, true);
  assert.equal(r2.ok, false);

  // and the in-flight session is unaffected by the swap, repeatedly
  assert.equal(inFlight.convert(msg).ok, true);
});

test('compatibility verdict is cached on the version binding', () => {
  const reg = new SchemaRegistry();
  reg.register(1, oldS, newV1);
  const s = reg.begin();
  assert.deepEqual(s.compat, { result: 'compatible' });
  reg.register(2, oldS, newV2);
  reg.setCurrent(2);
  assert.equal(reg.begin().compat.result, 'incompatible');
});
