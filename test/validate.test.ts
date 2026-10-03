import { test } from 'node:test';
import assert from 'node:assert/strict';
import { defineMessage } from '../src/schema.ts';
import { assertValid, validateValue } from '../src/validate.ts';
import { DecodeError, ValidationError } from '../src/wire/errors.ts';
import { Address, Person, samplePerson } from './person.ts';

test('descriptor reflection: duplicate field number rejected', () => {
  assert.throws(
    () =>
      defineMessage('Bad', [
        { number: 1, name: 'a', type: 'uint32' },
        { number: 1, name: 'b', type: 'uint32' },
      ]),
    DecodeError,
  );
});

test('descriptor reflection: duplicate field name rejected', () => {
  assert.throws(
    () =>
      defineMessage('Bad', [
        { number: 1, name: 'a', type: 'uint32' },
        { number: 2, name: 'a', type: 'uint32' },
      ]),
    DecodeError,
  );
});

test('descriptor reflection: invalid field numbers rejected', () => {
  assert.throws(() => defineMessage('Bad', [{ number: 0, name: 'a', type: 'uint32' }]), DecodeError);
  assert.throws(
    () => defineMessage('Bad', [{ number: 0x20000000, name: 'a', type: 'uint32' }]),
    DecodeError,
  );
});

test('descriptor reflection: packed constraints enforced', () => {
  assert.throws(
    () => defineMessage('Bad', [{ number: 1, name: 'a', type: 'uint32', packed: true }]),
    DecodeError,
  );
  assert.throws(
    () =>
      defineMessage('Bad', [
        { number: 1, name: 'a', type: 'string', repeated: true, packed: true },
      ]),
    DecodeError,
  );
});

test('descriptor reflection: message field requires nested descriptor', () => {
  assert.throws(
    () => defineMessage('Bad', [{ number: 1, name: 'a', type: 'message' }]),
    DecodeError,
  );
});

test('valid sample passes with zero issues', () => {
  assert.deepEqual(validateValue(Person, samplePerson), []);
});

test('missing required field reported', () => {
  const issues = validateValue(Person, { id: 1n });
  assert.equal(issues.length, 1);
  assert.equal(issues[0].path, 'name');
  assert.match(issues[0].message, /required/);
});

test('type mismatches reported with paths', () => {
  const issues = validateValue(Person, {
    name: 'x',
    id: 'not-a-bigint',
    scores: 'nope',
    address: { zip: '310000' },
    lucky: [true, 'yes'],
  });
  const paths = issues.map((i) => i.path).sort();
  assert.deepEqual(paths, ['address.zip', 'id', 'lucky[1]', 'scores']);
});

test('integer range checks for unsigned and signed widths', () => {
  const M = defineMessage('M', [
    { number: 1, name: 'u32', type: 'uint32' },
    { number: 2, name: 'i32', type: 'int32' },
    { number: 3, name: 'u64', type: 'uint64' },
  ]);
  assert.equal(validateValue(M, { u32: 0xffffffff, i32: -0x80000000, u64: 0xffffffffffffffffn }).length, 0);
  assert.equal(validateValue(M, { u32: 0x100000000 }).length, 1);
  assert.equal(validateValue(M, { u32: -1 }).length, 1);
  assert.equal(validateValue(M, { i32: 0x80000000 }).length, 1);
  assert.equal(validateValue(M, { u64: 1n << 64n }).length, 1);
  assert.equal(validateValue(M, { u64: -1n }).length, 1);
});

test('unknown keys rejected unless allowed', () => {
  const value = { name: 'x', mystery: 1 };
  assert.equal(validateValue(Person, value).length, 1);
  assert.equal(validateValue(Person, value, { allowUnknownKeys: true }).length, 0);
});

test('nesting depth limit reported as issue', () => {
  const issues = validateValue(Person, { name: 'x', address: { city: 'hz' } }, { maxDepth: 0 });
  assert.ok(issues.some((i) => /depth/.test(i.message)));
});

test('assertValid throws ValidationError aggregating issues', () => {
  try {
    assertValid(Person, { id: 1n, scores: [1, 'two'] });
    assert.unreachable();
  } catch (err) {
    assert.ok(err instanceof ValidationError);
    assert.equal(err.issues.length, 2);
  }
});

test('bytes and bool type checks', () => {
  const M = defineMessage('M', [
    { number: 1, name: 'blob', type: 'bytes' },
    { number: 2, name: 'flag', type: 'bool' },
  ]);
  assert.equal(validateValue(M, { blob: new Uint8Array([1]), flag: false }).length, 0);
  assert.equal(validateValue(M, { blob: [1], flag: 0 }).length, 2);
});

test('Address descriptor exposes reflection maps', () => {
  assert.equal(Address.byNumber.get(1)?.name, 'city');
  assert.equal(Address.byName.get('zip')?.type, 'fixed32');
  assert.equal(Person.byName.get('address')?.message, Address);
});
