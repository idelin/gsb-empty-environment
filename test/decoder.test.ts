import { test } from "node:test";
import assert from "node:assert/strict";
import { decodeMessage, type MessageSchema } from "../src/wire/schema.ts";
import { encodeMessage } from "../src/wire/encoder.ts";
import { WireWriter } from "../src/wire/writer.ts";
import { WireType } from "../src/wire/reader.ts";
import { WireError } from "../src/wire/errors.ts";

// A 3-level nested schema:
// Envelope { id: sint32, tags: packed sint32[], payload: Message, note?: string }
// Message  { kind: string, metrics: Metric[], flag: bool }
// Metric   { name: string, value: double, samples: packed fixed32[] }
const Metric: MessageSchema = {
  name: "Metric",
  fields: {
    1: { name: "name", type: "string" },
    2: { name: "value", type: "double" },
    3: { name: "samples", type: "fixed32", repeated: true, packed: true },
  },
};
const Message: MessageSchema = {
  name: "Message",
  fields: {
    1: { name: "kind", type: "string" },
    2: { name: "metrics", type: "message", repeated: true, message: Metric },
    3: { name: "flag", type: "bool" },
  },
};
const Envelope: MessageSchema = {
  name: "Envelope",
  fields: {
    1: { name: "id", type: "zigzag32" },
    2: { name: "tags", type: "zigzag32", repeated: true, packed: true },
    3: { name: "payload", type: "message", message: Message },
    4: { name: "note", type: "string" },
  },
};

const sample = {
  id: -42,
  tags: [1, -2, 300, -4000],
  payload: {
    kind: "telemetry",
    metrics: [
      { name: "cpu", value: 0.875, samples: [7, 8, 9] },
      { name: "mem", value: 1024.5, samples: [] },
    ],
    flag: true,
  },
  note: "nested ✓",
};

test("reflection decode: multi-level nesting + packed repeated roundtrip", () => {
  const buf = encodeMessage(sample, Envelope);
  const decoded = decodeMessage(buf, Envelope);
  assert.deepEqual(decoded, sample);
});

test("reflection decode: hand-built buffer matches protoc layout byte-for-byte", () => {
  // Envelope { id: -42 } -> field 1 (zigzag varint), value 83 -> 0x08 0x53
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint);
  w.writeZigZag32(-42);
  const decoded = decodeMessage(w.finish(), Envelope);
  assert.equal(decoded.id, -42);
  assert.deepEqual([...w.finish()], [0x08, 0x53]);
});

test("packed and unpacked repeated scalars are both accepted", () => {
  const w = new WireWriter();
  // packed run: field 2, LEN, [zigzag(1), zigzag(-2)]
  const packed = new WireWriter();
  packed.writeZigZag32(1).writeZigZag32(-2);
  w.writeTag(2, WireType.LengthDelimited);
  w.writeBytes(packed.finish());
  // unpacked singleton: field 2, varint zigzag(300)
  w.writeTag(2, WireType.Varint);
  w.writeZigZag32(300);
  const decoded = decodeMessage(w.finish(), Envelope);
  assert.deepEqual(decoded.tags, [1, -2, 300]);
});

test("unknown fields are skipped without disturbing known ones", () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeZigZag32(7); // known: id
  w.writeTag(99, WireType.LengthDelimited).writeString("unknown junk"); // unknown LEN
  w.writeTag(100, WireType.Fixed64).writeFixed64(123n); // unknown fixed64
  w.writeTag(101, WireType.Fixed32).writeFixed32(5); // unknown fixed32
  w.writeTag(102, WireType.Varint).writeVarint(9); // unknown varint
  w.writeTag(4, WireType.LengthDelimited).writeString("tail"); // known: note
  const decoded = decodeMessage(w.finish(), Envelope);
  assert.equal(decoded.id, 7);
  assert.equal(decoded.note, "tail");
});

test("truncation inside a nested message reports a guarded error", () => {
  const buf = encodeMessage(sample, Envelope);
  // Cut the buffer in the middle of the nested payload.
  const cut = buf.subarray(0, buf.length - 3);
  assert.throws(
    () => decodeMessage(cut, Envelope),
    (e: unknown) => e instanceof WireError && e.kind === "truncation",
  );
});

test("truncation at every single byte boundary is contained (no OOB reads)", () => {
  const buf = encodeMessage(sample, Envelope);
  for (let len = 0; len < buf.length; len++) {
    const cut = buf.subarray(0, len);
    try {
      decodeMessage(cut, Envelope); // a prefix may still decode cleanly
    } catch (e) {
      assert.ok(e instanceof WireError, `len=${len}: expected WireError, got ${e}`);
      assert.ok(
        e.kind === "truncation" || e.kind === "invalid-wire-type" || e.kind === "invalid-tag",
        `len=${len}: unexpected kind ${e.kind}`,
      );
    }
  }
});

test("declared length of nested message exceeding buffer is rejected", () => {
  const w = new WireWriter();
  w.writeTag(3, WireType.LengthDelimited);
  w.writeVarint(1000); // claims 1000 bytes of nested message
  w.writeRawBytes(Uint8Array.from([0x01, 0x02]));
  assert.throws(
    () => decodeMessage(w.finish(), Envelope),
    (e: unknown) => e instanceof WireError && e.kind === "truncation",
  );
});

test("wire-type mismatch for a declared field is reported", () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Fixed32).writeFixed32(1); // id expects varint
  assert.throws(
    () => decodeMessage(w.finish(), Envelope),
    (e: unknown) => e instanceof WireError && e.kind === "invalid-wire-type",
  );
});

test("empty buffer decodes to defaults (empty repeated fields)", () => {
  const decoded = decodeMessage(new Uint8Array(0), Envelope);
  assert.deepEqual(decoded, { tags: [] });
});

test("packed fixed32 inside nested metric decodes exactly", () => {
  const metric = { name: "io", value: 1.25, samples: [4294967295, 0, 3735928559] };
  const buf = encodeMessage(metric, Metric);
  assert.deepEqual(decodeMessage(buf, Metric), metric);
});
