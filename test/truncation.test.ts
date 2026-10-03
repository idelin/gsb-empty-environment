import test from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { ProtobufDecodeError } from '../src/errors.ts';
import { addressBookSchema, sampleBook } from './helpers/fixture.ts';

test('every truncated prefix fails safely with ProtobufDecodeError or decodes cleanly', () => {
  const full = encodeMessage(addressBookSchema, sampleBook());
  let thrown = 0;
  for (let len = 0; len < full.length; len += 1) {
    const prefix = full.subarray(0, len);
    try {
      decodeMessage(addressBookSchema, prefix);
      // A prefix ending exactly on a field boundary is a valid shorter message.
    } catch (e) {
      thrown += 1;
      assert.ok(
        e instanceof ProtobufDecodeError,
        `prefix ${len}: expected ProtobufDecodeError, got ${String(e)}`,
      );
    }
  }
  assert.ok(thrown > 0, 'at least some prefixes must be rejected');
});

test('specific truncation points map to safe error codes', () => {
  const full = encodeMessage(addressBookSchema, sampleBook());
  for (let len = 0; len < full.length; len += 1) {
    try {
      decodeMessage(addressBookSchema, full.subarray(0, len));
    } catch (e) {
      const code = (e as ProtobufDecodeError).code;
      assert.ok(
        ['TRUNCATED', 'OUT_OF_BOUNDS', 'MISSING_REQUIRED', 'VARINT_OVERFLOW', 'INVALID_UTF8'].includes(code),
        `unexpected code ${code} at prefix ${len}`,
      );
    }
  }
});

test('corrupted length prefix pointing past the buffer reports OUT_OF_BOUNDS', () => {
  const full = encodeMessage(addressBookSchema, sampleBook());
  // first byte is tag(1, LEN) = 0x0A, second byte is the nested length
  assert.equal(full[0], 0x0a);
  const corrupted = full.slice();
  corrupted[1] = 0x7f; // claim 127 bytes for the nested Person
  assert.throws(
    () => decodeMessage(addressBookSchema, corrupted),
    (e: unknown) => {
      assert.ok(e instanceof ProtobufDecodeError);
      assert.ok(['OUT_OF_BOUNDS', 'TRUNCATED'].includes((e as ProtobufDecodeError).code));
      return true;
    },
  );
});

test('truncated varint in tag position reports TRUNCATED', () => {
  assert.throws(
    () => decodeMessage(addressBookSchema, new Uint8Array([0x80])),
    (e: unknown) => (e as ProtobufDecodeError).code === 'TRUNCATED',
  );
});

test('fuzz: random byte mutations never throw non-ProtobufDecodeError', () => {
  const full = encodeMessage(addressBookSchema, sampleBook());
  let seed = 0xabcdef01;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed;
  };
  for (let i = 0; i < 20000; i += 1) {
    const mutated = full.slice();
    const mutations = 1 + (rand() % 4);
    for (let m = 0; m < mutations; m += 1) {
      mutated[rand() % mutated.length] = rand() & 0xff;
    }
    try {
      decodeMessage(addressBookSchema, mutated);
    } catch (e) {
      assert.ok(e instanceof ProtobufDecodeError, `mutation ${i}: got ${String(e)}`);
    }
  }
});

test('fuzz: random garbage buffers never throw non-ProtobufDecodeError', () => {
  let seed = 0x00c0ffee;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed;
  };
  for (let i = 0; i < 20000; i += 1) {
    const len = rand() % 64;
    const buf = new Uint8Array(len);
    for (let j = 0; j < len; j += 1) buf[j] = rand() & 0xff;
    try {
      decodeMessage(addressBookSchema, buf);
    } catch (e) {
      assert.ok(e instanceof ProtobufDecodeError, `garbage ${i}: got ${String(e)}`);
    }
  }
});
