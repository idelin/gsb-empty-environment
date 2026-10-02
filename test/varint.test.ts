import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  MalformedVarintError,
  TruncatedFieldError,
  VarintOverflowError,
} from "../src/errors.ts";
import {
  decodeVarint,
  decodeVarint64,
  encodeVarintInto,
  MAX_VARINT_BYTES,
  varintLength,
  zigzagDecode32,
  zigzagDecode64,
  zigzagEncode32,
  zigzagEncode64,
} from "../src/varint.ts";

function encode(value: number | bigint): Uint8Array {
  const buf = new Uint8Array(16);
  const end = encodeVarintInto(value, buf, 0);
  return buf.subarray(0, end);
}

describe("varint 7-bit compression", () => {
  it("roundtrips boundary values", () => {
    const cases = [
      0, 1, 127, 128, 300, 16383, 16384,
      2 ** 21 - 1, 2 ** 21, 2 ** 28 - 1, 2 ** 28,
      2 ** 31 - 1, 2 ** 31, 2 ** 32 - 1, 2 ** 32,
      2 ** 53 - 1,
    ];
    for (const v of cases) {
      const buf = encode(v);
      const [decoded, pos] = decodeVarint(buf, 0, buf.length);
      assert.equal(decoded, v, `value ${v}`);
      assert.equal(pos, buf.length);
      assert.equal(buf.length, varintLength(v));
    }
  });

  it("uses exactly 1 byte for values < 128 and 2 for < 16384", () => {
    assert.equal(encode(0).length, 1);
    assert.equal(encode(127).length, 1);
    assert.equal(encode(128).length, 2);
    assert.equal(encode(16383).length, 2);
    assert.equal(encode(16384).length, 3);
  });

  it("encodes 7 bits per byte, LSB first, continuation bit set", () => {
    // 300 = 0b100101100 -> 0xAC 0x02 (canonical protobuf example)
    assert.deepEqual(Array.from(encode(300)), [0xac, 0x02]);
  });

  it("roundtrips full 64-bit range as bigint", () => {
    const cases = [
      0n, 1n, 127n, 300n,
      2n ** 53n - 1n, 2n ** 53n, 2n ** 63n - 1n,
      2n ** 64n - 1n, // max uint64
    ];
    for (const v of cases) {
      const buf = encode(v);
      const [decoded, pos] = decodeVarint64(buf, 0, buf.length);
      assert.equal(decoded, v);
      assert.equal(pos, buf.length);
    }
    assert.equal(encode(2n ** 64n - 1n).length, MAX_VARINT_BYTES);
  });

  it("throws VarintOverflowError for number decode beyond 2^53-1", () => {
    const buf = encode(2n ** 53n);
    assert.throws(() => decodeVarint(buf, 0, buf.length), VarintOverflowError);
  });

  it("throws MalformedVarintError on >10 continuation bytes", () => {
    const buf = new Uint8Array(11).fill(0x80); // never terminates
    assert.throws(() => decodeVarint(buf, 0, buf.length), MalformedVarintError);
    assert.throws(() => decodeVarint64(buf, 0, buf.length), MalformedVarintError);
  });

  it("throws TruncatedFieldError when buffer ends mid-varint", () => {
    const buf = new Uint8Array([0x80]); // continuation bit set, no next byte
    assert.throws(() => decodeVarint(buf, 0, buf.length), TruncatedFieldError);
  });

  it("respects the end window, not just buffer length", () => {
    const buf = new Uint8Array([0xac, 0x02, 0xff]);
    const [v, pos] = decodeVarint(buf, 0, 2);
    assert.equal(v, 300);
    assert.equal(pos, 2);
    assert.throws(() => decodeVarint(buf, 0, 1), TruncatedFieldError);
  });
});

describe("zigzag signed conversion", () => {
  it("maps signed to unsigned per spec", () => {
    assert.equal(zigzagEncode32(0), 0);
    assert.equal(zigzagEncode32(-1), 1);
    assert.equal(zigzagEncode32(1), 2);
    assert.equal(zigzagEncode32(-2), 3);
    assert.equal(zigzagEncode32(2147483647), 0xfffffffe | 0);
    assert.equal(zigzagEncode32(-2147483648), 0xffffffff | 0);
  });

  it("roundtrips int32 extremes", () => {
    for (const v of [0, 1, -1, 2, -2, 2147483647, -2147483648, 123456789, -987654321]) {
      assert.equal(zigzagDecode32(zigzagEncode32(v)), v);
    }
  });

  it("roundtrips int64 extremes as bigint", () => {
    const cases = [
      0n, 1n, -1n, 2n ** 63n - 1n, -(2n ** 63n),
      123456789012345678n, -123456789012345678n,
    ];
    for (const v of cases) {
      assert.equal(zigzagDecode64(zigzagEncode64(v)), v);
    }
  });
});
