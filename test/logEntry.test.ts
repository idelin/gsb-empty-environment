import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CodecError } from '../src/codec/errors.ts';
import { WIRE_LEN, WIRE_VARINT, writeFieldHeader } from '../src/codec/fields.ts';
import {
  decodeLogEntryCopy,
  decodeLogEntryView,
  encodeLogEntryPayload,
  ENTRY_NOOP,
  ENTRY_VALUE,
  makeEntry,
} from '../src/codec/logEntry.ts';
import { ByteWriter } from '../src/codec/writer.ts';

const sample = () => makeEntry(7, 3, 2, ENTRY_VALUE, Buffer.from('x=42'), 5, 1_700_000_000_000);

test('log entry roundtrip v1 and v2', () => {
  for (const version of [1, 2] as const) {
    const payload = encodeLogEntryPayload(sample(), version);
    const e = decodeLogEntryView(payload);
    assert.equal(e.index, 7);
    assert.equal(e.term, 3);
    assert.equal(e.nodeId, 2);
    assert.equal(e.type, ENTRY_VALUE);
    assert.equal(e.value.toString(), 'x=42');
    if (version === 2) {
      assert.equal(e.flags, 5);
      assert.equal(e.timestampMs, 1_700_000_000_000);
    }
  }
});

test('backward compat: v2 decoder reads v1 payload with defaults', () => {
  const v1 = encodeLogEntryPayload(sample(), 1);
  const e = decodeLogEntryView(v1);
  assert.equal(e.flags, 0); // absent fields default
  assert.equal(e.timestampMs, 0);
  assert.equal(e.value.toString(), 'x=42');
});

test('forward compat: v1 decoder skips unknown v2 fields', () => {
  const v2 = encodeLogEntryPayload(sample(), 2);
  // A v1-only decoder knows tags 1..5; tags 6,7 must be skipped by wire type.
  const e = decodeLogEntryView(v2); // decoder skips unknown tags generically
  assert.equal(e.index, 7);
  assert.equal(e.value.toString(), 'x=42');
});

test('forward compat: unknown future fields (v3 simulation) are skipped', () => {
  const w = new ByteWriter();
  writeFieldHeader(w, 1, WIRE_VARINT);
  w.varint(9); // index
  writeFieldHeader(w, 4, WIRE_VARINT);
  w.varint(ENTRY_NOOP);
  writeFieldHeader(w, 5, WIRE_LEN);
  w.varint(0); // empty value
  writeFieldHeader(w, 99, WIRE_VARINT); // future varint field
  w.varint(123456);
  writeFieldHeader(w, 100, WIRE_LEN); // future length-delimited field
  w.varint(4);
  w.bytes(Buffer.from('futr'));
  const e = decodeLogEntryView(w.finish());
  assert.equal(e.index, 9);
  assert.equal(e.type, ENTRY_NOOP);
});

test('zero-copy restore: decoded value is a view into the source buffer', () => {
  const payload = encodeLogEntryPayload(sample(), 2);
  const e = decodeLogEntryView(payload);
  assert.equal(e.value.buffer, payload.buffer); // no copy: shared backing store
  // mutate the source: the decoded entry observes it (proves zero-copy)
  const off = e.value.byteOffset - payload.byteOffset;
  payload[off] = 'y'.charCodeAt(0);
  assert.equal(e.value.toString(), 'y=42');
});

test('detached copy is immune to source mutation', () => {
  const payload = encodeLogEntryPayload(sample(), 2);
  const e = decodeLogEntryCopy(payload);
  payload.fill(0x5a); // corrupt the whole source
  assert.equal(e.value.toString(), 'x=42');
});

test('decoder rejects truncated value field', () => {
  const payload = encodeLogEntryPayload(sample(), 2);
  const cut = payload.length - 2; // drop the tail mid-value
  assert.throws(() => decodeLogEntryView(payload.subarray(0, cut)), CodecError);
});

test('decoder rejects malicious length prefix', () => {
  const w = new ByteWriter();
  writeFieldHeader(w, 5, WIRE_LEN);
  w.varint(2 ** 40); // absurd length, way past the buffer
  assert.throws(() => decodeLogEntryView(w.finish()), /TRUNCATED|need/);
});

test('decoder rejects bad wire type for known field', () => {
  const w = new ByteWriter();
  writeFieldHeader(w, 1, WIRE_LEN); // index must be varint
  w.varint(1);
  w.bytes(Buffer.from([0]));
  assert.throws(() => decodeLogEntryView(w.finish()), /must be varint/);
});

test('empty and large values roundtrip', () => {
  const empty = makeEntry(1, 1, 1, ENTRY_NOOP, Buffer.alloc(0));
  assert.equal(decodeLogEntryView(encodeLogEntryPayload(empty)).value.length, 0);

  const big = makeEntry(2, 1, 1, ENTRY_VALUE, Buffer.alloc(1024 * 1024, 0xab));
  const out = decodeLogEntryView(encodeLogEntryPayload(big));
  assert.equal(out.value.length, 1024 * 1024);
  assert.equal(out.value[12345], 0xab);
});

test('boundary indexes and terms roundtrip', () => {
  const e = makeEntry(2 ** 40, 2 ** 32, 255, ENTRY_VALUE, Buffer.from('v'));
  const out = decodeLogEntryView(encodeLogEntryPayload(e));
  assert.equal(out.index, 2 ** 40);
  assert.equal(out.term, 2 ** 32);
  assert.equal(out.nodeId, 255);
});
