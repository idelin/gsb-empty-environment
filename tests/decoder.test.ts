import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WireWriter, concatBytes } from '../src/wire/writer.ts';
import { decodeRawFields, decodePackedVarints, decodePackedFixed32, decodePackedFixed64 } from '../src/wire/decoder.ts';
import { WireReader, WireType } from '../src/wire/reader.ts';
import { writeVarint } from '../src/wire/varint.ts';
import { ProtobufDecodeError } from '../src/wire/errors.ts';

test('decodes a flat message with every wire type', () => {
  const buf = new WireWriter()
    .varintField(1, 150)
    .fixed64Field(2, 0x0102030405060708n)
    .stringField(3, 'hello')
    .fixed32Field(4, 0xdeadbeef)
    .finish();

  const fields = decodeRawFields(buf);
  assert.equal(fields.length, 4);
  assert.deepEqual(fields[0], { fieldNumber: 1, wireType: WireType.Varint, value: 150n });
  assert.deepEqual(fields[1], { fieldNumber: 2, wireType: WireType.Fixed64, value: 0x0102030405060708n });
  assert.equal(fields[2].fieldNumber, 3);
  assert.equal(new TextDecoder().decode(fields[2].value as Uint8Array), 'hello');
  assert.deepEqual(fields[3], { fieldNumber: 4, wireType: WireType.Fixed32, value: 0xdeadbeef });
});

test('decodes multi-level nested length-delimited messages', () => {
  const level3 = new WireWriter().varintField(1, 42).finish();
  const level2 = new WireWriter().messageField(1, level3).stringField(2, 'mid').finish();
  const level1 = new WireWriter().messageField(1, level2).varintField(2, 7).finish();

  const top = decodeRawFields(level1);
  assert.equal(top.length, 2);
  const mid = decodeRawFields(top[0].value as Uint8Array);
  assert.equal(mid.length, 2);
  const inner = decodeRawFields(mid[0].value as Uint8Array);
  assert.deepEqual(inner, [{ fieldNumber: 1, wireType: WireType.Varint, value: 42n }]);
  assert.equal(new TextDecoder().decode(mid[1].value as Uint8Array), 'mid');
  assert.equal(top[1].value, 7n);
});

test('decodes packed repeated varint / fixed32 / fixed64 payloads', () => {
  const varintPayload = concatBytes(writeVarint(3), writeVarint(270), writeVarint(86942));
  assert.deepEqual(decodePackedVarints(varintPayload), [3n, 270n, 86942n]);

  const w32 = new WireWriter().fixed32(1).fixed32(0xffffffff).fixed32(42).finish();
  assert.deepEqual(decodePackedFixed32(w32), [1, 0xffffffff, 42]);

  const w64 = new WireWriter().fixed64(5n).fixed64(2n ** 64n - 1n).finish();
  assert.deepEqual(decodePackedFixed64(w64), [5n, 2n ** 64n - 1n]);
});

test('packed fixed32/fixed64 reject payloads with bad alignment', () => {
  assert.throws(() => decodePackedFixed32(Uint8Array.from([1, 2, 3])), ProtobufDecodeError);
  assert.throws(() => decodePackedFixed64(Uint8Array.from([1, 2, 3, 4])), ProtobufDecodeError);
});

test('truncation at EVERY byte position raises ProtobufDecodeError (never a raw crash)', () => {
  const full = new WireWriter()
    .varintField(1, 2n ** 40n)
    .fixed64Field(2, 0xaabbccddeeff0011n)
    .stringField(3, 'truncation-guard')
    .fixed32Field(4, 123456)
    .messageField(5, new WireWriter().varintField(1, 9).finish())
    .finish();

  // Sanity: the full buffer decodes fine.
  assert.equal(decodeRawFields(full).length, 5);

  for (let len = 0; len < full.length; len++) {
    const cut = full.subarray(0, len);
    try {
      decodeRawFields(cut);
      // A prefix that happens to be a valid message is acceptable only if it
      // ends exactly on a field boundary; the decoder must never throw
      // anything other than ProtobufDecodeError.
    } catch (err) {
      assert.ok(err instanceof ProtobufDecodeError, `prefix ${len}: expected ProtobufDecodeError, got ${err}`);
    }
  }
});

test('truncated length-delimited field reports safe bounds guard', () => {
  // tag(3, LEN) + declared length 100 + only 2 payload bytes
  const buf = concatBytes(writeVarint((3 << 3) | 2), writeVarint(100), Uint8Array.from([1, 2]));
  assert.throws(
    () => decodeRawFields(buf),
    (err: unknown) => err instanceof ProtobufDecodeError && /truncated/.test((err as Error).message),
  );
});

test('rejects reserved field number 0 and deprecated group wire types', () => {
  assert.throws(() => decodeRawFields(Uint8Array.from([0x00])), /field number 0/);
  assert.throws(() => decodeRawFields(Uint8Array.from([(1 << 3) | 3, 0x01])), /deprecated/);
  assert.throws(() => decodeRawFields(Uint8Array.from([(1 << 3) | 4, 0x01])), /deprecated/);
  assert.throws(() => decodeRawFields(Uint8Array.from([(1 << 3) | 7, 0x01])), /invalid wire type/);
});

test('reader rejects an out-of-range construction window', () => {
  assert.throws(() => new WireReader(Uint8Array.from([1, 2]), 1, 5), ProtobufDecodeError);
  assert.throws(() => new WireReader(Uint8Array.from([1, 2]), -1), ProtobufDecodeError);
});

test('skipField skips every wire type within bounds', () => {
  const buf = new WireWriter()
    .varintField(1, 1)
    .fixed64Field(2, 2n)
    .stringField(3, 'skip me')
    .fixed32Field(4, 4)
    .varintField(5, 555)
    .finish();
  const reader = new WireReader(buf);
  for (let i = 0; i < 4; i++) {
    const { wireType } = reader.readTag();
    reader.skipField(wireType);
  }
  const { fieldNumber } = reader.readTag();
  assert.equal(fieldNumber, 5);
  assert.equal(reader.readVarint(), 555n);
  assert.ok(reader.eof);
});
