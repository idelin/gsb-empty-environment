import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { TruncatedFieldError } from "../src/errors.ts";
import {
  WIRE_FIXED32,
  WIRE_LENGTH_DELIMITED,
  WIRE_VARINT,
  WireReader,
} from "../src/reader.ts";
import { WireWriter } from "../src/writer.ts";

describe("WireWriter/WireReader fixed-width", () => {
  it("roundtrips fixed32 little-endian", () => {
    const w = new WireWriter();
    w.writeFixed32(0xdeadbeef);
    assert.deepEqual(Array.from(w.view()), [0xef, 0xbe, 0xad, 0xde]);
    const r = new WireReader(w.view());
    assert.equal(r.readFixed32(), 0xdeadbeef);
    assert.ok(r.done);
  });

  it("roundtrips fixed64 little-endian", () => {
    const w = new WireWriter();
    w.writeFixed64(0x0102030405060708n);
    assert.deepEqual(Array.from(w.view()), [8, 7, 6, 5, 4, 3, 2, 1]);
    const r = new WireReader(w.view());
    assert.equal(r.readFixed64(), 0x0102030405060708n);
  });

  it("fixed32 truncation is guarded at every prefix length", () => {
    const w = new WireWriter();
    w.writeFixed32(123);
    const full = w.finish();
    for (let len = 0; len < 4; len++) {
      assert.throws(
        () => new WireReader(full.subarray(0, len)).readFixed32(),
        TruncatedFieldError,
        `len=${len}`,
      );
    }
  });

  it("fixed64 truncation is guarded at every prefix length", () => {
    const w = new WireWriter();
    w.writeFixed64(0xffffffffffffffffn);
    const full = w.finish();
    for (let len = 0; len < 8; len++) {
      assert.throws(
        () => new WireReader(full.subarray(0, len)).readFixed64(),
        TruncatedFieldError,
        `len=${len}`,
      );
    }
  });
});

describe("WireReader tags, strings, sub-messages, skip", () => {
  it("reads tag as field number + wire type", () => {
    const w = new WireWriter();
    w.writeTag(15, WIRE_LENGTH_DELIMITED);
    const r = new WireReader(w.view());
    assert.deepEqual(r.readTag(), [15, WIRE_LENGTH_DELIMITED]);
  });

  it("roundtrips strings including multibyte UTF-8", () => {
    const w = new WireWriter();
    w.fieldString(1, "héllo 世界 🚀");
    const r = new WireReader(w.view());
    assert.deepEqual(r.readTag(), [1, WIRE_LENGTH_DELIMITED]);
    assert.equal(r.readString(), "héllo 世界 🚀");
    assert.ok(r.done);
  });

  it("readBytes returns a zero-copy view", () => {
    const backing = new Uint8Array([3, 10, 20, 30]);
    const r = new WireReader(backing);
    const view = r.readBytes();
    assert.equal(view.buffer, backing.buffer); // same memory, no copy
    assert.deepEqual(Array.from(view), [10, 20, 30]);
  });

  it("enterSub/leaveSub confines reads to the sub-window", () => {
    const w = new WireWriter();
    const inner = new WireWriter();
    inner.fieldVarint(1, 42);
    w.fieldBytes(1, inner.view());
    w.fieldVarint(2, 7);
    const r = new WireReader(w.view());
    assert.deepEqual(r.readTag(), [1, WIRE_LENGTH_DELIMITED]);
    const saved = r.enterSub();
    assert.deepEqual(r.readTag(), [1, WIRE_VARINT]);
    assert.equal(r.readVarint(), 42);
    assert.ok(r.done); // done within sub-window
    r.leaveSub(saved);
    assert.ok(!r.done); // outer window continues
    assert.deepEqual(r.readTag(), [2, WIRE_VARINT]);
    assert.equal(r.readVarint(), 7);
  });

  it("enterSub rejects a length that overruns the buffer", () => {
    const w = new WireWriter();
    w.fieldBytes(1, new Uint8Array([1, 2, 3]));
    const full = w.finish();
    const truncated = full.subarray(0, full.length - 1); // length says 3, only 2 left
    const r = new WireReader(truncated);
    r.readTag();
    assert.throws(() => r.enterSub(), TruncatedFieldError);
  });

  it("skip handles every wire type and guards truncation", () => {
    const w = new WireWriter();
    w.fieldVarint(1, 123456);
    w.fieldFixed64(2, 0xaabbccddeeff0011n);
    w.fieldBytes(3, new Uint8Array([9, 9, 9]));
    w.fieldFixed32(4, 77);
    w.fieldVarint(5, 1);
    const r = new WireReader(w.view());
    for (let i = 0; i < 4; i++) {
      const [, wt] = r.readTag();
      r.skip(wt);
    }
    assert.deepEqual(r.readTag(), [5, WIRE_VARINT]);
    assert.equal(r.readVarint(), 1);
    assert.ok(r.done);
  });

  it("skip on truncated payload throws instead of overrunning", () => {
    const w = new WireWriter();
    w.fieldBytes(1, new Uint8Array(100));
    const full = w.finish();
    const r = new WireReader(full.subarray(0, 3)); // tag + len, no payload
    const [, wt] = r.readTag();
    assert.throws(() => r.skip(wt), TruncatedFieldError);
  });

  it("rejects unknown wire types", () => {
    const r = new WireReader(new Uint8Array([1]));
    assert.throws(() => r.skip(7), TruncatedFieldError);
  });
});

describe("WireWriter reuse", () => {
  it("reset() reuses the backing buffer without growth", () => {
    const w = new WireWriter(16);
    w.writeVarint(1);
    w.reset();
    assert.equal(w.length, 0);
    w.writeVarint(300);
    assert.equal(w.length, 2);
  });

  it("grows transparently for large payloads", () => {
    const w = new WireWriter(4);
    const big = new Uint8Array(10_000).fill(0xab);
    w.writeBytes(big); // length prefix (varint 10000 = 2 bytes) + payload
    const out = w.finish();
    assert.equal(out.length, 2 + big.length);
    assert.deepEqual(out.subarray(2), big);
  });
});
