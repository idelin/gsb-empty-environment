import test from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { addressBookSchema, sampleBook } from './helpers/fixture.ts';

const gc = (globalThis as { gc?: () => void }).gc;

test('repeated decode keeps heap stable (no leak)', { skip: !gc }, () => {
  const data = encodeMessage(addressBookSchema, sampleBook());
  const snapshot = Buffer.from(data); // independent copy for integrity check
  gc!();
  const before = process.memoryUsage().heapUsed;
  let sink = 0;
  for (let i = 0; i < 50000; i += 1) {
    const decoded = decodeMessage(addressBookSchema, data);
    sink += (decoded.people as unknown[]).length;
  }
  gc!();
  const after = process.memoryUsage().heapUsed;
  assert.equal(sink, 100000);
  const growth = after - before;
  assert.ok(
    growth < 8 * 1024 * 1024,
    `heap grew by ${growth} bytes after 50k decodes — possible leak`,
  );
  assert.deepEqual(Buffer.from(data), snapshot, 'input buffer mutated during decode');
});

test('repeated encode keeps heap stable (no leak)', { skip: !gc }, () => {
  const book = sampleBook();
  gc!();
  const before = process.memoryUsage().heapUsed;
  let sink = 0;
  for (let i = 0; i < 50000; i += 1) {
    sink += encodeMessage(addressBookSchema, book).length;
  }
  gc!();
  const after = process.memoryUsage().heapUsed;
  assert.ok(sink > 0);
  const growth = after - before;
  assert.ok(
    growth < 8 * 1024 * 1024,
    `heap grew by ${growth} bytes after 50k encodes — possible leak`,
  );
});

test('zero-copy views do not extend beyond the source buffer lifetime semantics', () => {
  const data = encodeMessage(addressBookSchema, sampleBook());
  const decoded = decodeMessage(addressBookSchema, data);
  const avatar = (decoded.people as Array<Record<string, unknown>>)[0].avatar as Uint8Array;
  assert.strictEqual(avatar.buffer, data.buffer);
  assert.equal(avatar.byteLength, 6);
  // the view is bounded: it cannot see past its own window
  assert.equal(avatar.byteOffset >= data.byteOffset, true);
  assert.equal(avatar.byteOffset + avatar.byteLength <= data.byteOffset + data.byteLength, true);
});
