import { test } from 'node:test';
import assert from 'node:assert/strict';
import { defineMessage, validateMessage } from '../src/schema.ts';
import { ValidationError } from '../src/errors.ts';

const Inner = defineMessage('Inner', [
  { name: 'n', number: 1, type: 'int32' },
]);
const Outer = defineMessage('Outer', [
  { name: 'inner', number: 1, type: 'message', message: Inner },
  { name: 'names', number: 2, type: 'string', repeated: true },
  { name: 'flag', number: 3, type: 'bool' },
]);

test('valid message passes reflection validation', () => {
  validateMessage(Outer, { inner: { n: 5 }, names: ['a'], flag: false });
});

test('unknown field is rejected with full path', () => {
  assert.throws(() => validateMessage(Outer, { nope: 1 }),
    (e) => e instanceof ValidationError && /Outer\.nope/.test(e.message));
});

test('type mismatch reports the nested path', () => {
  assert.throws(() => validateMessage(Outer, { inner: { n: 'x' } }),
    (e) => e instanceof ValidationError && /Outer\.inner\.n/.test(e.message));
});

test('repeated element mismatch reports the index path', () => {
  assert.throws(() => validateMessage(Outer, { names: ['ok', 3] }),
    (e) => e instanceof ValidationError && /names\.1/.test(e.message));
});

test('int32 range is enforced', () => {
  assert.throws(() => validateMessage(Inner, { n: 2 ** 31 }), ValidationError);
  assert.throws(() => validateMessage(Inner, { n: 1.5 }), ValidationError);
  validateMessage(Inner, { n: -(2 ** 31) });
});

test('int64 accepts bigint and number, enforces range', () => {
  const S = defineMessage('S', [{ name: 'v', number: 1, type: 'int64' }]);
  validateMessage(S, { v: 2n ** 63n - 1n });
  validateMessage(S, { v: 42 });
  assert.throws(() => validateMessage(S, { v: 2n ** 63n }), ValidationError);
  assert.throws(() => validateMessage(S, { v: '1' }), ValidationError);
});

test('bytes requires Uint8Array; bool requires boolean', () => {
  const S = defineMessage('S', [
    { name: 'b', number: 1, type: 'bytes' },
    { name: 'f', number: 2, type: 'bool' },
  ]);
  validateMessage(S, { b: new Uint8Array(0), f: true });
  assert.throws(() => validateMessage(S, { b: [1] }), ValidationError);
  assert.throws(() => validateMessage(S, { f: 1 }), ValidationError);
});

test('schema definition rejects duplicate field numbers and bad packed types', () => {
  assert.throws(() => defineMessage('Bad', [
    { name: 'a', number: 1, type: 'int32' },
    { name: 'b', number: 1, type: 'int32' },
  ]), /duplicate/);
  assert.throws(() => defineMessage('Bad', [
    { name: 's', number: 1, type: 'string', packed: true },
  ]), /cannot be packed/);
  assert.throws(() => defineMessage('Bad', [
    { name: 'm', number: 1, type: 'message' },
  ]), /nested schema/);
});
