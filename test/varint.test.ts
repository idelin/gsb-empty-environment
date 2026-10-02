import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { MalformedError, TruncationError } from "../src/errors.ts";
import {
  MAX_VARINT_BYTES,
  decodeVarint,
  encodeVarint,
  toSigned64,
  toUnsigned64,
  varintLength,
  zigzagDecode,
  zigzagEncode,
} from "../src/varint.ts";

describe("varint 7-bit compression", () => {
  const cases: Array<[bigint, number[]]> = [
    [0n, [0x00]],
    [1n, [0x01]],
    [127n, [0x7f]],
    [128n, [0x80, 0x01]],
    [300n, [0xac, 0x02]],
    [0xffff_ffffn, [0xff, 0xff, 0xff, 0xff, 0x0f]],
    [(1n << 64n) - 1n, [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]],
  ];

  for (const [value, bytes] of cases) {
    it(`round-trips ${value}`, () => {
      const enc = encodeVarint(value);
      assert.deepEqual([...enc], bytes);
      assert.equal(enc.length, varintLength(value));
      const dec = decodeVarint(enc);
      assert.equal(dec.value, value);
      assert.equal(dec.bytesRead, bytes.length);
    });
  }

  it("never exceeds 10 bytes for 64-bit values", () => {
    assert.equal(encodeVarint((1n << 64n) - 1n).length, MAX_VARINT_BYTES);
    assert.equal(encodeVarint(0n).length, 1);
  });

  it("masks values to 64 bits on encode", () => {
    assert.deepEqual([...encodeVarint(1n << 64n)], [0x00]);
  });

  it("decodes with offset and limit", () => {
    const buf = new Uint8Array([0x99, 0xac, 0x02, 0x77]);
    const dec = decodeVarint(buf, 1, 3);
    assert.equal(dec.value, 300n);
    assert.equal(dec.bytesRead, 2);
  });

  it("throws TruncationError on unterminated varint", () => {
    assert.throws(() => decodeVarint(new Uint8Array([0x80])), TruncationError);
    assert.throws(() => decodeVarint(new Uint8Array([])), TruncationError);
    assert.throws(
      () => decodeVarint(new Uint8Array([0xff, 0xff, 0xff]), 0, 3),
      TruncationError,
    );
  });

  it("throws MalformedError when the 10th byte overflows 64 bits", () => {
    const bad = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02]);
    assert.throws(() => decodeVarint(bad), MalformedError);
    const badCont = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x81, 0x01]);
    assert.throws(() => decodeVarint(badCont), MalformedError);
  });
});

describe("zigzag signed conversion", () => {
  const table: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [2n, 4n],
    [2147483647n, 4294967294n],
    [-2147483648n, 4294967295n],
    [(1n << 63n) - 1n, (1n << 64n) - 2n],
    [-(1n << 63n), (1n << 64n) - 1n],
  ];

  for (const [signed, unsigned] of table) {
    it(`maps ${signed} <-> ${unsigned}`, () => {
      assert.equal(zigzagEncode(signed), unsigned);
      assert.equal(zigzagDecode(unsigned), signed);
    });
  }

  it("round-trips random int64 values", () => {
    let seed = 0x12345678n;
    for (let i = 0; i < 1000; i++) {
      seed = (seed * 6364136223846793005n + 1442695040888963407n) & ((1n << 64n) - 1n);
      const signed = toSigned64(seed);
      assert.equal(zigzagDecode(zigzagEncode(signed)), signed);
    }
  });

  it("two's-complement helpers round-trip", () => {
    assert.equal(toUnsigned64(-1n), (1n << 64n) - 1n);
    assert.equal(toSigned64((1n << 64n) - 1n), -1n);
    assert.equal(toSigned64(toUnsigned64(-(1n << 63n))), -(1n << 63n));
  });
});
