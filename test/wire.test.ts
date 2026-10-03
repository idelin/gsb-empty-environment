import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { DecodeError } from "../src/errors.ts";
import { WireReader, WIRE_VARINT } from "../src/reader.ts";
import { WireWriter } from "../src/writer.ts";
import { captureError } from "../testkit/capture.ts";

describe("fixed32 / fixed64", () => {
  it("writes little-endian fixed32", () => {
    const w = new WireWriter();
    w.fixed32Field(1, 0xdeadbeef);
    assert.deepEqual([...w.finish()], [0x0d, 0xef, 0xbe, 0xad, 0xde]);
  });

  it("writes little-endian fixed64", () => {
    const w = new WireWriter();
    w.fixed64Field(1, 0x0102030405060708n);
    assert.deepEqual([...w.finish()], [0x09, 0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01]);
  });

  it("round-trips fixed values including extremes", () => {
    const w = new WireWriter();
    w.fixed32Field(1, 0).fixed32Field(2, 0xffffffff).fixed64Field(3, 0n).fixed64Field(4, 2n ** 64n - 1n);
    const r = new WireReader(w.finish());
    assert.deepEqual(r.readTag(), { field: 1, wireType: 5 });
    assert.equal(r.readFixed32(), 0);
    assert.deepEqual(r.readTag(), { field: 2, wireType: 5 });
    assert.equal(r.readFixed32(), 0xffffffff);
    assert.deepEqual(r.readTag(), { field: 3, wireType: 1 });
    assert.equal(r.readFixed64(), 0n);
    assert.deepEqual(r.readTag(), { field: 4, wireType: 1 });
    assert.equal(r.readFixed64(), 2n ** 64n - 1n);
    assert.ok(r.eof);
  });

  it("reads signed fixed values", () => {
    const w = new WireWriter();
    w.fixed32Field(1, -1 >>> 0).fixed64Field(2, -1n);
    const r = new WireReader(w.finish());
    r.readTag();
    assert.equal(r.readSFixed32(), -1);
    r.readTag();
    assert.equal(r.readSFixed64(), -1n);
  });
});

describe("length-delimited and nesting", () => {
  it("round-trips strings and bytes", () => {
    const w = new WireWriter();
    w.stringField(1, "héllo wörld ✨").bytesField(2, new Uint8Array([0, 1, 2, 255]));
    const r = new WireReader(w.finish());
    r.readTag();
    assert.equal(r.readString(), "héllo wörld ✨");
    r.readTag();
    assert.deepEqual([...r.readBytes()], [0, 1, 2, 255]);
  });

  it("bounds sub-readers to their declared length", () => {
    const w = new WireWriter();
    w.messageField(1, (inner) => inner.varintField(1, 42));
    w.varintField(2, 7);
    const r = new WireReader(w.finish());
    r.readTag();
    const sub = r.subReader();
    sub.readTag();
    assert.equal(sub.readVarint(), 42n);
    assert.ok(sub.eof, "sub-reader must stop at the nested message boundary");
    r.readTag();
    assert.equal(r.readVarint(), 7n);
    assert.ok(r.eof);
  });

  it("rejects a nested message whose declared length overruns the parent", () => {
    // field 1, length-delimited, declares 10 bytes, buffer holds 1
    const buf = new Uint8Array([0x0a, 0x0a, 0x00]);
    const r = new WireReader(buf);
    r.readTag();
    assert.throws(() => r.subReader(), /truncated nested message/);
  });

  it("rejects nesting deeper than the depth cap", () => {
    // Recursively: field 1 message wrapping itself, 200 levels deep.
    let payload = new Uint8Array(0);
    for (let i = 0; i < 200; i++) {
      const w = new WireWriter();
      w.bytesField(1, payload);
      payload = w.finish();
    }
    let r: WireReader = new WireReader(payload);
    let depth = 0;
    assert.throws(
      () => {
        for (;;) {
          r.readTag();
          r = r.subReader();
          depth++;
        }
      },
      /nesting depth/,
    );
    assert.ok(depth > 50, "should have descended a reasonable number of levels first");
  });
});

describe("tags, skipping and malformed input", () => {
  it("skips unknown fields of every wire type", () => {
    const w = new WireWriter();
    w.varintField(10, 123).fixed64Field(11, 5n).bytesField(12, new Uint8Array([9])).fixed32Field(13, 7);
    w.varintField(1, 1);
    const r = new WireReader(w.finish());
    for (let i = 0; i < 4; i++) {
      const tag = r.readTag();
      assert.ok(tag);
      r.skip(tag.wireType);
    }
    const tag = r.readTag();
    assert.deepEqual(tag, { field: 1, wireType: WIRE_VARINT });
    assert.equal(r.readVarint(), 1n);
  });

  it("rejects field number 0", () => {
    assert.throws(() => new WireReader(new Uint8Array([0x00])).readTag(), /field number 0/);
  });

  it("rejects deprecated group wire types", () => {
    assert.throws(() => new WireReader(new Uint8Array([0x0b])).readTag(), /groups/);
    assert.throws(() => new WireReader(new Uint8Array([0x0c])).readTag(), /groups/);
  });

  it("rejects invalid wire types 6 and 7", () => {
    assert.throws(() => new WireReader(new Uint8Array([0x0e])).readTag(), /invalid wire type/);
    assert.throws(() => new WireReader(new Uint8Array([0x0f])).readTag(), /invalid wire type/);
  });

  it("throws DecodeError with offset when a field is truncated mid-value", () => {
    const w = new WireWriter();
    w.fixed64Field(3, 1n);
    const buf = w.finish().subarray(0, 4); // tag + 3 of 8 bytes
    const r = new WireReader(buf);
    r.readTag();
    const err = captureError<DecodeError>(() => r.readFixed64());
    assert.ok(err instanceof DecodeError);
    assert.match(err.message, /truncated fixed64/);
    assert.equal(err.offset, 1);
  });

  it("release() drops the buffer and further reads fail cleanly", () => {
    const w = new WireWriter();
    w.varintField(1, 1);
    const r = new WireReader(w.finish());
    r.release();
    assert.throws(() => r.readVarint(), /released/);
  });

  it("constructor validates offset and length", () => {
    const buf = new Uint8Array(4);
    assert.throws(() => new WireReader(buf, 5), /offset out of bounds/);
    assert.throws(() => new WireReader(buf, 0, 5), /length exceeds buffer/);
  });
});
