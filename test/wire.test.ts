import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { MalformedError, TruncationError } from "../src/errors.ts";
import { Reader, WireType, Writer } from "../src/wire.ts";

function capture(fn: () => unknown): Error {
  try {
    fn();
  } catch (err) {
    return err as Error;
  }
  throw new Error("expected function to throw");
}

describe("Writer/Reader primitives", () => {
  it("round-trips fixed32/fixed64/float/double little-endian", () => {
    const w = new Writer();
    w.fixed32(0xdeadbeef).fixed64(0x0102_0304_0506_0708n).float(1.5).double(-Math.PI);
    const r = new Reader(w.finish());
    assert.equal(r.fixed32(), 0xdeadbeef);
    assert.equal(r.fixed64(), 0x0102_0304_0506_0708n);
    assert.equal(r.float(), 1.5);
    assert.equal(r.double(), -Math.PI);
    assert.ok(r.done);
  });

  it("writes fixed32 little-endian byte order", () => {
    const w = new Writer();
    w.fixed32(0x01020304);
    assert.deepEqual([...w.finish()], [0x04, 0x03, 0x02, 0x01]);
  });

  it("round-trips signed fixed types", () => {
    const w = new Writer();
    w.sfixed32(-123456).sfixed64(-9_000_000_000_000_000_000n);
    const r = new Reader(w.finish());
    assert.equal(r.sfixed32(), -123456);
    assert.equal(r.sfixed64(), -9_000_000_000_000_000_000n);
  });

  it("round-trips strings incl. multi-byte UTF-8", () => {
    const w = new Writer();
    w.string("héllo 世界 🌍");
    const r = new Reader(w.finish());
    assert.equal(r.string(), "héllo 世界 🌍");
    assert.ok(r.done);
  });

  it("bytes() returns a detached copy (no aliasing of source buffer)", () => {
    const w = new Writer();
    w.bytes(new Uint8Array([1, 2, 3]));
    const buf = w.finish();
    const r = new Reader(buf);
    const out = r.bytes();
    out[0] = 0xff;
    assert.equal(buf[1], 1, "mutating the copy must not touch the source");
  });

  it("tag encodes field number and wire type", () => {
    const w = new Writer();
    w.tag(1, WireType.Varint);
    w.tag(15, WireType.Fixed32);
    w.tag(2047, WireType.LengthDelimited);
    const r = new Reader(w.finish());
    assert.deepEqual(r.tag(), { fieldNo: 1, wireType: WireType.Varint });
    assert.deepEqual(r.tag(), { fieldNo: 15, wireType: WireType.Fixed32 });
    assert.deepEqual(r.tag(), { fieldNo: 2047, wireType: WireType.LengthDelimited });
  });

  it("rejects illegal tags", () => {
    assert.throws(() => new Writer().tag(0, WireType.Varint), MalformedError);
    assert.throws(() => new Writer().tag(0x2000_0000, WireType.Varint), MalformedError);
    const r = new Reader(new Uint8Array([0x00]));
    assert.throws(() => r.tag(), MalformedError); // field number 0
    const g = new Reader(new Uint8Array([0x0b])); // field 1, start-group
    assert.throws(() => g.tag(), MalformedError);
  });

  it("subMessage scopes the child reader to the declared length", () => {
    const w = new Writer();
    const inner = new Writer();
    inner.varint(42).varint(43);
    w.bytes(inner.finish());
    w.varint(7);
    const r = new Reader(w.finish());
    const child = r.subMessage("child");
    assert.equal(child.varint(), 42n);
    assert.equal(child.varint(), 43n);
    assert.ok(child.done);
    assert.equal(r.varint(), 7n);
  });

  it("skip() handles every wire type and stays in bounds", () => {
    const w = new Writer();
    w.varint(150).fixed64(1n).bytes(new Uint8Array([9, 9])).fixed32(2);
    const r = new Reader(w.finish());
    r.skip(WireType.Varint);
    r.skip(WireType.Fixed64);
    r.skip(WireType.LengthDelimited);
    r.skip(WireType.Fixed32);
    assert.ok(r.done);
    assert.throws(() => r.skip(WireType.Varint), TruncationError);
  });
});

describe("safe out-of-bounds protection", () => {
  it("fixed32/fixed64/float/double report truncation", () => {
    for (const [read, size] of [
      [(r: Reader) => r.fixed32(), 4],
      [(r: Reader) => r.fixed64(), 8],
      [(r: Reader) => r.float(), 4],
      [(r: Reader) => r.double(), 8],
    ] as Array<[(r: Reader) => unknown, number]>) {
      const r = new Reader(new Uint8Array(size - 1));
      assert.throws(() => read(r), TruncationError);
    }
  });

  it("length-delimited payload longer than remaining reports truncation", () => {
    const buf = new Uint8Array([0x05, 1, 2]); // declares 5 bytes, has 2
    const r = new Reader(buf);
    const err = capture(() => r.bytes());
    assert.ok(err instanceof TruncationError);
    assert.equal(err.needed, 5);
    assert.equal(err.available, 2);
  });

  it("subMessage with inflated length reports truncation", () => {
    const buf = new Uint8Array([0x7f, 1, 2, 3]);
    const r = new Reader(buf);
    assert.throws(() => r.subMessage("x"), TruncationError);
  });

  it("truncation error carries the field path", () => {
    const r = new Reader(new Uint8Array([0x0a, 0x05, 1])); // tag + len 5, 1 byte
    r.tag();
    r.fieldPath = "Root.payload";
    const err = capture(() => r.bytes());
    assert.ok(err instanceof TruncationError);
    assert.match(err.message, /Root\.payload/);
    assert.equal(err.fieldPath, "Root.payload");
  });

  it("Reader constructor rejects impossible windows", () => {
    const buf = new Uint8Array(4);
    assert.throws(() => new Reader(buf, 3, 2), TruncationError);
    assert.throws(() => new Reader(buf, -1), TruncationError);
  });

  it("invalid UTF-8 raises MalformedError, not a raw exception", () => {
    const w = new Writer();
    w.bytes(new Uint8Array([0xff, 0xfe]));
    const r = new Reader(w.finish());
    assert.throws(() => r.string(), MalformedError);
  });
});
