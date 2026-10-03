import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage } from '../src/protobuf.ts';
import { ProtobufDecodeError, TruncatedMessageError } from '../src/errors.ts';
import { WireReader, WireWriter, WireType } from '../src/wire.ts';
import { ENVELOPE_SCHEMA, HEADER_SCHEMA, POINT_SCHEMA, SAMPLE_VALUE } from './fixtures.ts';

test('round-trip: nested message + packed repeated + fixed + zigzag', () => {
  const bytes = encodeMessage(ENVELOPE_SCHEMA, SAMPLE_VALUE);
  const decoded = decodeMessage(ENVELOPE_SCHEMA, bytes);
  assert.deepEqual(decoded, SAMPLE_VALUE);
});

test('hand-crafted wire bytes decode to expected struct', () => {
  // Point { x: -1, y: 1 } -> field1 zigzag(-1)=1, field2 zigzag(1)=2
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeVarint(1n);
  w.writeTag(2, WireType.Varint).writeVarint(2n);
  assert.deepEqual(decodeMessage(POINT_SCHEMA, w.finish()), { x: -1, y: 1 });
});

test('packed repeated: canonical protobuf doc example decodes correctly', () => {
  // field 3 (sint32 packed) with payload zigzag: 1->2, -2->3, 300->600
  const schema = {
    name: 'M',
    fields: [{ name: 'v', number: 3, type: 'sint32', repeated: true, packed: true }],
  } as const;
  const w = new WireWriter();
  w.writeTag(3, WireType.LengthDelimited);
  w.writeBytes(new Uint8Array([0x02, 0x03, 0xd8, 0x04]));
  assert.deepEqual(decodeMessage(schema as never, w.finish()), { v: [1, -2, 300] });
});

test('unpacked encoding of packed field is also accepted', () => {
  const w = new WireWriter();
  w.writeTag(3, WireType.Varint).writeVarint(2n);
  w.writeTag(3, WireType.Varint).writeVarint(3n);
  const decoded = decodeMessage(ENVELOPE_SCHEMA, w.finish());
  assert.deepEqual(decoded.samples, [1, -2]);
});

test('multi-level nesting: envelope -> header -> point', () => {
  const bytes = encodeMessage(ENVELOPE_SCHEMA, SAMPLE_VALUE);
  const decoded = decodeMessage(ENVELOPE_SCHEMA, bytes) as {
    header: { origin: { x: number; y: number } };
  };
  assert.deepEqual(decoded.header.origin, { x: -123, y: 456 });
});

test('unknown fields are skipped by wire type without disturbing known fields', () => {
  const w = new WireWriter();
  w.writeTag(99, WireType.Varint).writeVarint(12345n); // unknown varint
  w.writeTag(100, WireType.Fixed64).writeFixed64(0xaabbccddeeff0011n); // unknown fixed64
  w.writeTag(101, WireType.LengthDelimited).writeString('ghost'); // unknown LEN
  w.writeTag(102, WireType.Fixed32).writeFixed32(0x01020304); // unknown fixed32
  w.writeTag(2, WireType.LengthDelimited).writeString('real');
  const decoded = decodeMessage(ENVELOPE_SCHEMA, w.finish());
  assert.deepEqual(decoded, { label: 'real' });
});

test('truncation guard: every cut point of a valid message fails safely', () => {
  const bytes = encodeMessage(ENVELOPE_SCHEMA, SAMPLE_VALUE);
  let truncated = 0;
  for (let cut = 0; cut < bytes.length; cut += 1) {
    try {
      // Prefixes ending exactly on a field boundary decode legally.
      decodeMessage(ENVELOPE_SCHEMA, bytes.subarray(0, cut));
    } catch (err) {
      // The only legal failure is the safe bounds guard: never a raw
      // RangeError/TypeError from an out-of-bounds buffer access.
      assert.ok(
        err instanceof TruncatedMessageError,
        `cut=${cut}: expected TruncatedMessageError, got ${err}`,
      );
      truncated += 1;
    }
  }
  assert.ok(truncated > 0, 'at least some prefixes must be caught mid-field');
  // Cutting inside the final byte (a bool field's varint) must be caught.
  assert.throws(
    () => decodeMessage(ENVELOPE_SCHEMA, bytes.subarray(0, bytes.length - 1)),
    TruncatedMessageError,
  );
});

test('truncated nested length-delimited payload reports bounds precisely', () => {
  // field 1 (header, LEN): tag 0x0a, declared length 100, only 3 payload bytes.
  const bytes = new Uint8Array([0x0a, 100, 1, 2, 3]);
  let err: TruncatedMessageError | undefined;
  try {
    decodeMessage(ENVELOPE_SCHEMA, bytes);
  } catch (caught) {
    assert.ok(caught instanceof TruncatedMessageError);
    err = caught;
  }
  assert.ok(err, 'expected a TruncatedMessageError');
  assert.equal(err.needed, 100);
  assert.equal(err.available, 3);
});

test('declared length exceeding buffer is rejected before reading', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.LengthDelimited).writeVarint(1_000_000n);
  assert.throws(() => decodeMessage(ENVELOPE_SCHEMA, w.finish()), TruncatedMessageError);
});

test('wire-type mismatch on a known field is a decode error', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Varint).writeVarint(1n); // label is string (LEN), not varint
  assert.throws(() => decodeMessage(ENVELOPE_SCHEMA, w.finish()), ProtobufDecodeError);
});

test('illegal field number 0 and invalid wire types are rejected', () => {
  const w = new WireWriter();
  w.writeVarint(0n); // tag = 0 -> field number 0
  assert.throws(() => decodeMessage(ENVELOPE_SCHEMA, w.finish()), ProtobufDecodeError);

  const w2 = new WireWriter();
  w2.writeVarint(BigInt((7 << 3) | 3)); // wire type 3 (deprecated group) on unknown field
  assert.throws(() => decodeMessage(ENVELOPE_SCHEMA, w2.finish()), ProtobufDecodeError);
});

test('fixed32/fixed64 little-endian layout is exact', () => {
  const w = new WireWriter();
  w.writeTag(2, WireType.Fixed32).writeFixed32(0xdeadbeef);
  const reader = new WireReader(w.finish());
  reader.readVarint(); // tag
  assert.equal(reader.readFixed32(), 0xdeadbeef);

  const bytes = encodeMessage(HEADER_SCHEMA, { id: 1n, flags: 0xdeadbeef });
  const decoded = decodeMessage(HEADER_SCHEMA, bytes);
  assert.equal(decoded.flags, 0xdeadbeef);
});

test('empty buffer decodes to empty struct', () => {
  assert.deepEqual(decodeMessage(ENVELOPE_SCHEMA, new Uint8Array(0)), {});
});
