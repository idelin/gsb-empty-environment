import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  assertValid,
  validateMessage,
  validateSchema,
  type MessageSchema,
} from '../src/schema.ts';
import { encodeMessage } from '../src/protobuf.ts';
import { ValidationError } from '../src/errors.ts';
import { ENVELOPE_SCHEMA, SAMPLE_VALUE } from './fixtures.ts';

test('valid sample passes reflection validation', () => {
  assert.deepEqual(validateMessage(ENVELOPE_SCHEMA, SAMPLE_VALUE), []);
  assert.doesNotThrow(() => assertValid(ENVELOPE_SCHEMA, SAMPLE_VALUE));
});

test('missing required field is reported with full path', () => {
  const issues = validateMessage(ENVELOPE_SCHEMA, { label: 'x' });
  assert.equal(issues.length, 1);
  assert.equal(issues[0].path, 'Envelope.header');
  assert.match(issues[0].message, /required/);
});

test('nested required violation reports deep path', () => {
  const value = { header: { id: 1n, origin: { x: 1 } } }; // origin.y missing
  const issues = validateMessage(ENVELOPE_SCHEMA, value);
  assert.ok(issues.some((i) => i.path === 'Envelope.header.origin.y'));
});

test('unknown struct fields are rejected', () => {
  const issues = validateMessage(ENVELOPE_SCHEMA, { ...SAMPLE_VALUE, hacker: 1 });
  assert.ok(issues.some((i) => i.path === 'Envelope.hacker' && /unknown/.test(i.message)));
});

test('scalar type mismatches are all caught', () => {
  const issues = validateMessage(ENVELOPE_SCHEMA, {
    header: { id: 'not-a-bigint' },
    label: 42,
    active: 'yes',
    blob: [1, 2, 3],
    checksum: 123, // number instead of bigint
  });
  const paths = issues.map((i) => i.path);
  assert.ok(paths.includes('Envelope.header.id'));
  assert.ok(paths.includes('Envelope.label'));
  assert.ok(paths.includes('Envelope.active'));
  assert.ok(paths.includes('Envelope.blob'));
  assert.ok(paths.includes('Envelope.checksum'));
});

test('range overflow is caught for 32-bit and 64-bit types', () => {
  const issues = validateMessage(ENVELOPE_SCHEMA, {
    header: { id: 2n ** 64n }, // uint64 overflow
    samples: [2 ** 31], // sint32 overflow
  });
  assert.ok(issues.some((i) => i.path === 'Envelope.header.id' && /range/.test(i.message)));
  assert.ok(issues.some((i) => i.path === 'Envelope.samples[0]' && /range/.test(i.message)));
});

test('repeated field must be an array; singular must not be', () => {
  const bad1 = validateMessage(ENVELOPE_SCHEMA, { samples: 5 });
  assert.ok(bad1.some((i) => /array/.test(i.message)));
  const bad2 = validateMessage(ENVELOPE_SCHEMA, { label: ['a'] });
  assert.ok(bad2.some((i) => /singular/.test(i.message)));
});

test('schema self-validation: duplicates and illegal packed', () => {
  const bad: MessageSchema = {
    name: 'Bad',
    fields: [
      { name: 'a', number: 1, type: 'int32' },
      { name: 'b', number: 1, type: 'int32' }, // duplicate number
      { name: 'a', number: 2, type: 'int32' }, // duplicate name
      { name: 's', number: 3, type: 'string', repeated: true, packed: true }, // illegal packed
      { name: 'z', number: 0, type: 'int32' }, // illegal number
    ],
  };
  const issues = validateSchema(bad);
  assert.ok(issues.some((i) => /duplicate field number/.test(i.message)));
  assert.ok(issues.some((i) => /duplicate field name/.test(i.message)));
  assert.ok(issues.some((i) => /packed/.test(i.message)));
  assert.ok(issues.some((i) => /illegal field number/.test(i.message)));
});

test('encode refuses invalid structs via assertValid', () => {
  assert.throws(
    () => encodeMessage(ENVELOPE_SCHEMA, { header: { id: -1n } } as never),
    ValidationError,
  );
});
