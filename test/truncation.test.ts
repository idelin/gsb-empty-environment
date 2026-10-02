import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage, type MessageSchema } from '../src/schema.ts';
import { CodecError, TruncatedMessageError, ValidationError } from '../src/errors.ts';

// All fields required so that ANY proper prefix of the encoding is invalid:
// it either cuts a field mid-value (TruncatedMessageError) or drops a
// required trailing field (ValidationError). Silent success is impossible.
const Doc: MessageSchema = {
  name: 'Doc',
  fields: [
    { name: 'id', number: 1, type: 'uint64', required: true },
    { name: 'title', number: 2, type: 'string', required: true },
    { name: 'body', number: 3, type: 'bytes', required: true },
    { name: 'nums', number: 4, type: 'sint32', repeated: true },
    { name: 'meta', number: 5, type: 'message', required: true, message: {
      name: 'Meta',
      fields: [
        { name: 'author', number: 1, type: 'string', required: true },
        { name: 'version', number: 2, type: 'fixed64', required: true },
      ],
    } },
    { name: 'pi', number: 6, type: 'double', required: true },
  ],
};

const fullValue = {
  id: 12345678901234567890n,
  title: 'truncation safety report',
  body: Uint8Array.from({ length: 64 }, (_, i) => i),
  nums: [1, -1, 1000, -1000, 2147483647],
  meta: { author: 'fuzz', version: 0xfeedfacecafef00dn },
  pi: Math.PI,
};

test('every proper prefix of a valid stream fails safely (never silently succeeds)', () => {
  const full = encodeMessage(Doc, fullValue);
  // sanity: the complete buffer decodes
  assert.deepEqual(decodeMessage(Doc, full), fullValue);

  let truncated = 0;
  let validation = 0;
  for (let len = 0; len < full.length; len++) {
    const prefix = full.subarray(0, len);
    try {
      decodeMessage(Doc, prefix);
      assert.fail(`prefix of length ${len}/${full.length} decoded without error`);
    } catch (err) {
      if (err instanceof TruncatedMessageError) truncated++;
      else if (err instanceof ValidationError) validation++;
      else throw err; // anything else is a bug (e.g. RangeError from OOB access)
    }
  }
  assert.ok(truncated > 0, 'mid-field cuts reported as TruncatedMessageError');
  assert.ok(validation > 0, 'boundary cuts reported as ValidationError (missing required)');
  assert.equal(truncated + validation, full.length);
});

test('truncation error carries precise offset/needed/remaining diagnostics', () => {
  const full = encodeMessage(Doc, fullValue);
  // cut inside the 64-byte body payload
  const bodyStart = full.indexOf(0x1a); // tag of field 3 (LEN)
  const cutAt = bodyStart + 2 + 10; // tag + len + 10 of 64 bytes
  try {
    decodeMessage(Doc, full.subarray(0, cutAt));
    assert.fail('should have thrown');
  } catch (err) {
    assert.ok(err instanceof TruncatedMessageError);
    assert.equal(err.remaining, 10);
    assert.equal(err.needed, 64);
    assert.match(err.message, /truncated bytes at offset/);
  }
});

test('truncation inside nested message and inside packed array is caught', () => {
  const full = encodeMessage(Doc, fullValue);
  // locate nested meta payload and cut inside it
  const metaTag = full.indexOf(0x2a); // field 5, LEN
  const metaLen = full[metaTag + 1];
  for (const cut of [metaTag + 2, metaTag + 2 + Math.floor(metaLen / 2), metaTag + 2 + metaLen - 1]) {
    assert.throws(() => decodeMessage(Doc, full.subarray(0, cut)), TruncatedMessageError);
  }
  // locate packed nums payload (field 4, LEN) and cut inside it
  const numsTag = full.indexOf(0x22);
  const numsLen = full[numsTag + 1];
  assert.throws(
    () => decodeMessage(Doc, full.subarray(0, numsTag + 2 + numsLen - 1)),
    TruncatedMessageError,
  );
});

test('fuzz: random garbage never causes out-of-bounds or foreign error types', () => {
  let seed = 0x2f6e2b1;
  const rand = () => {
    // xorshift32 — deterministic, no external deps
    seed ^= seed << 13;
    seed ^= seed >>> 17;
    seed ^= seed << 5;
    return (seed >>> 0) / 0x100000000;
  };
  let ok = 0;
  let codecErrors = 0;
  let validationErrors = 0;
  for (let i = 0; i < 5000; i++) {
    const len = Math.floor(rand() * 200);
    const buf = Uint8Array.from({ length: len }, () => Math.floor(rand() * 256));
    try {
      decodeMessage(Doc, buf);
      ok++;
    } catch (err) {
      if (err instanceof ValidationError) validationErrors++;
      else if (err instanceof CodecError) codecErrors++;
      else throw err; // RangeError/TypeError/etc would mean a bounds-check hole
    }
  }
  assert.equal(ok + codecErrors + validationErrors, 5000);
});

test('fuzz: mutated valid messages stay within safe error taxonomy', () => {
  const full = encodeMessage(Doc, fullValue);
  let seed = 12345;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  for (let i = 0; i < 2000; i++) {
    const buf = full.slice();
    const mutations = 1 + Math.floor(rand() * 4);
    for (let m = 0; m < mutations; m++) {
      buf[Math.floor(rand() * buf.length)] = Math.floor(rand() * 256);
    }
    try {
      decodeMessage(Doc, buf);
    } catch (err) {
      assert.ok(
        err instanceof CodecError || err instanceof ValidationError,
        `foreign error type: ${String(err)}`,
      );
    }
  }
});

test('hostile lengths: huge LEN prefix and huge packed count cannot allocate/OOB', () => {
  // field 3 (body, bytes) with a 4GB length prefix
  const evil = Uint8Array.of(0x1a, 0xff, 0xff, 0xff, 0xff, 0x0f, 0x01, 0x02);
  assert.throws(() => decodeMessage(Doc, evil), TruncatedMessageError);
  // field 4 (packed sint32) claiming 2^32-1 bytes of payload
  const evilPacked = Uint8Array.of(0x22, 0xff, 0xff, 0xff, 0xff, 0x0f);
  assert.throws(() => decodeMessage(Doc, evilPacked), TruncatedMessageError);
});
