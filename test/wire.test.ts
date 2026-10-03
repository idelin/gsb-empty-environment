import { test } from "node:test";
import assert from "node:assert/strict";
import { defineMessage } from "../src/schema.ts";
import type { MessageRecord } from "../src/schema.ts";
import { SchemaValidationError } from "../src/schema.ts";
import {
  InvalidWireTypeError,
  ProtobufDecodeError,
  TruncatedFieldError,
} from "../src/varint.ts";
import { WireReader, WireType, WireWriter } from "../src/wire.ts";

/* A three-level nested schema with packed repeated fields. */
const Inner = defineMessage("Inner", [
  { name: "id", fieldNumber: 1, type: "uint32" },
  { name: "label", fieldNumber: 2, type: "string" },
]);

const Middle = defineMessage("Middle", [
  { name: "ratio", fieldNumber: 1, type: "double" },
  { name: "inner", fieldNumber: 2, type: "message", schema: Inner },
  { name: "tags", fieldNumber: 3, type: "sint32", repeated: true, packed: true },
]);

const Outer = defineMessage("Outer", [
  { name: "name", fieldNumber: 1, type: "string" },
  { name: "flag", fieldNumber: 2, type: "bool" },
  { name: "big", fieldNumber: 3, type: "sint64" },
  { name: "blob", fieldNumber: 4, type: "bytes" },
  { name: "middle", fieldNumber: 5, type: "message", schema: Middle },
  { name: "scores", fieldNumber: 6, type: "fixed32", repeated: true, packed: true },
  { name: "history", fieldNumber: 7, type: "message", repeated: true, schema: Inner },
  { name: "checksum", fieldNumber: 8, type: "fixed64" },
]);

function sampleRecord(): MessageRecord {
  return {
    name: "checkpoint-α",
    flag: true,
    big: -9223372036854775808n,
    blob: new Uint8Array([0, 1, 2, 250, 255]),
    middle: {
      ratio: 3.141592653589793,
      inner: { id: 42, label: "nested" },
      tags: [-5, 0, 7, 123456],
    },
    scores: [0xdeadbeef, 1, 0xffffffff],
    history: [
      { id: 1, label: "a" },
      { id: 2, label: "b" },
    ],
    checksum: 0xffffffffffffffffn,
  };
}

test("schema: multi-level nested + packed repeated round-trip", () => {
  const record = sampleRecord();
  const bytes = Outer.encode(record);
  const decoded = Outer.decode(bytes);
  assert.deepEqual(decoded, record);
});

test("schema: empty record encodes to zero bytes", () => {
  const bytes = Outer.encode({});
  assert.equal(bytes.length, 0);
  assert.deepEqual(Outer.decode(bytes), {});
});

test("schema: packed and unpacked repeated encodings both decode", () => {
  // Hand-craft field 3 of Middle as unpacked varints (legal on the wire).
  const writer = new WireWriter();
  for (const v of [1n, 2n, 3n]) {
    writer.writeTag(3, WireType.VARINT);
    writer.writeSint32(Number(v));
  }
  // And again as one packed blob.
  const packed = new WireWriter();
  packed.writeSint32(-1);
  packed.writeSint32(100);
  writer.writeTag(3, WireType.LENGTH_DELIMITED);
  writer.writeBytes(packed.toBytes());

  const decoded = Middle.decode(writer.toBytes());
  assert.deepEqual(decoded.tags, [1, 2, 3, -1, 100]);
});

test("schema: unknown fields are skipped safely", () => {
  const writer = new WireWriter();
  writer.writeTag(2, WireType.LENGTH_DELIMITED);
  writer.writeString("hello");
  writer.writeTag(99, WireType.VARINT); // unknown varint
  writer.writeVarint(123456n);
  writer.writeTag(100, WireType.FIXED32); // unknown fixed32
  writer.writeFixed32(7);
  writer.writeTag(101, WireType.FIXED64); // unknown fixed64
  writer.writeFixed64(9n);
  writer.writeTag(102, WireType.LENGTH_DELIMITED); // unknown blob
  writer.writeBytes(new Uint8Array([1, 2, 3]));

  const decoded = Inner.decode(writer.toBytes());
  assert.deepEqual(decoded, { label: "hello" });
});

test("schema: truncation never escapes bounds; failures are structured", () => {
  const full = Outer.encode(sampleRecord());
  let truncationsCaught = 0;
  for (let cut = 0; cut < full.length; cut++) {
    const truncated = full.subarray(0, cut);
    try {
      // A cut landing exactly on a field boundary is a valid shorter
      // message; anything else must fail with a structured error and
      // never with a raw RangeError / out-of-bounds access.
      Outer.decode(truncated);
    } catch (e) {
      assert.ok(
        e instanceof ProtobufDecodeError,
        `cut=${cut}: expected ProtobufDecodeError, got ${String(e)}`,
      );
      truncationsCaught++;
    }
  }
  // Dropping the final byte always truncates the last field's payload.
  assert.throws(() => Outer.decode(full.subarray(0, full.length - 1)), TruncatedFieldError);
  assert.ok(truncationsCaught > 0, "expected at least one truncation to be caught");
  // Sanity: the full buffer itself decodes fine.
  assert.deepEqual(Outer.decode(full), sampleRecord());
});

test("schema: truncated nested length-delimited message reports bounds", () => {
  const writer = new WireWriter();
  writer.writeTag(2, WireType.LENGTH_DELIMITED);
  writer.writeVarint(100n); // claims 100 bytes
  writer.writeRawBytes(new Uint8Array([0x08, 0x2a])); // only 2 follow
  try {
    Inner.decode(writer.toBytes());
    assert.unreachable("should have thrown");
  } catch (e) {
    assert.ok(e instanceof TruncatedFieldError);
    assert.equal((e as TruncatedFieldError).needed, 100);
    assert.equal((e as TruncatedFieldError).available, 2);
  }
});

test("schema: wrong wire type for a known field is rejected", () => {
  const writer = new WireWriter();
  writer.writeTag(1, WireType.FIXED32); // Inner.id is varint
  writer.writeFixed32(1);
  assert.throws(() => Inner.decode(writer.toBytes()), ProtobufDecodeError);
});

test("schema: invalid wire types (SGROUP/EGROUP/6/7) are rejected", () => {
  for (const wt of [3, 4, 6, 7]) {
    const writer = new WireWriter();
    writer.writeTag(1, wt as never);
    writer.writeVarint(0n);
    assert.throws(() => Inner.decode(writer.toBytes()), InvalidWireTypeError);
  }
});

test("schema: tag with field number 0 is rejected", () => {
  assert.throws(() => Inner.decode(new Uint8Array([0x00])), ProtobufDecodeError);
});

test("schema: validation catches type mismatches before encoding", () => {
  assert.throws(
    () => Inner.encode({ id: "not-a-number" } as unknown as MessageRecord),
    SchemaValidationError,
  );
  assert.throws(
    () => Outer.encode({ middle: { tags: "nope" } } as unknown as MessageRecord),
    SchemaValidationError,
  );
  assert.throws(
    () => Outer.encode({ blob: [1, 2, 3] } as unknown as MessageRecord),
    SchemaValidationError,
  );
});

test("schema: duplicate field numbers/names rejected at definition", () => {
  assert.throws(
    () =>
      defineMessage("Bad", [
        { name: "a", fieldNumber: 1, type: "uint32" },
        { name: "b", fieldNumber: 1, type: "uint32" },
      ]),
    SchemaValidationError,
  );
  assert.throws(
    () => defineMessage("Bad2", [{ name: "s", fieldNumber: 1, type: "string", packed: true }]),
    SchemaValidationError,
  );
});

test("reader: float/double and negative int32 varint encoding", () => {
  const S = defineMessage("Nums", [
    { name: "f", fieldNumber: 1, type: "float" },
    { name: "d", fieldNumber: 2, type: "double" },
    { name: "i", fieldNumber: 3, type: "int32" },
    { name: "sf", fieldNumber: 4, type: "sfixed64" },
  ]);
  const rec = { f: 1.5, d: -2.25, i: -1, sf: -42n };
  const bytes = S.encode(rec);
  // negative int32 must be a 10-byte varint on the wire
  const reader = new WireReader(bytes);
  let sawTenByteVarint = false;
  while (!reader.eof) {
    const tag = reader.readTag()!;
    if (tag.fieldNumber === 3) {
      const before = reader.offset;
      reader.readVarint();
      sawTenByteVarint = reader.offset - before === 10;
    } else {
      reader.skip(tag.wireType);
    }
  }
  assert.ok(sawTenByteVarint);
  assert.deepEqual(S.decode(bytes), rec);
});
