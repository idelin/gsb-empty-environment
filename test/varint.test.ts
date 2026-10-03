import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { DecodeError } from "../src/errors.ts";
import {
  decodeVarint,
  encodeVarint,
  MAX_VARINT_BYTES,
  toSigned64,
  zigzagDecode,
  zigzagEncode,
} from "../src/varint.ts";

describe("varint encoding (7-bit compression)", () => {
  it("encodes canonical boundary values to known bytes", () => {
    assert.deepEqual([...encodeVarint(0)], [0x00]);
    assert.deepEqual([...encodeVarint(1)], [0x01]);
    assert.deepEqual([...encodeVarint(127)], [0x7f]);
    assert.deepEqual([...encodeVarint(128)], [0x80, 0x01]);
    assert.deepEqual([...encodeVarint(300)], [0xac, 0x02]);
    assert.deepEqual([...encodeVarint(0xffff_ffffn)], [0xff, 0xff, 0xff, 0xff, 0x0f]);
  });

  it("encodes -1 (int64) as ten 0xff..01 bytes per protobuf semantics", () => {
    const bytes = encodeVarint(-1n);
    assert.equal(bytes.length, MAX_VARINT_BYTES);
    assert.deepEqual([...bytes], [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]);
  });

  it("round-trips 64-bit extremes", () => {
    const values = [0n, 1n, 127n, 128n, 300n, 2n ** 32n, 2n ** 53n - 1n, 2n ** 63n - 1n, 2n ** 64n - 1n];
    for (const v of values) {
      const { value, bytesRead } = decodeVarint(encodeVarint(v), 0);
      assert.equal(value, v);
      assert.equal(bytesRead, encodeVarint(v).length);
    }
  });

  it("round-trips pseudorandom values deterministically", () => {
    let s = 0x12345678;
    const rnd = () => {
      s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
      return s;
    };
    for (let i = 0; i < 10_000; i++) {
      const v = (BigInt(rnd()) << 32n) | BigInt(rnd());
      assert.equal(decodeVarint(encodeVarint(v), 0).value, v);
    }
  });

  it("rejects truncated sequences at every prefix length", () => {
    const full = encodeVarint(2n ** 63n - 1n);
    for (let cut = 0; cut < full.length; cut++) {
      assert.throws(() => decodeVarint(full.subarray(0, cut), 0), DecodeError);
    }
  });

  it("rejects >64-bit overflow", () => {
    const overflow = new Uint8Array([...Array(9).fill(0xff), 0x02]);
    assert.throws(() => decodeVarint(overflow, 0), /overflow/);
    const eleven = new Uint8Array([...Array(10).fill(0x80), 0x01]);
    assert.throws(() => decodeVarint(eleven, 0), /overflow/);
  });

  it("honours the limit parameter (sub-message bound)", () => {
    const buf = encodeVarint(300);
    assert.throws(() => decodeVarint(buf, 0, 1), /truncated/);
    assert.equal(decodeVarint(buf, 0, 2).value, 300n);
  });
});

describe("zigzag signed conversion", () => {
  it("maps signed values per the protobuf spec", () => {
    const table: Array<[bigint, bigint]> = [
      [0n, 0n],
      [-1n, 1n],
      [1n, 2n],
      [-2n, 3n],
      [2n, 4n],
      [2147483647n, 4294967294n],
      [-2147483648n, 4294967295n],
      [9223372036854775807n, 18446744073709551614n],
      [-9223372036854775808n, 18446744073709551615n],
    ];
    for (const [signed, unsigned] of table) {
      assert.equal(zigzagEncode(signed), unsigned);
      assert.equal(zigzagDecode(unsigned), signed);
    }
  });

  it("round-trips across the full int64 range", () => {
    const values = [0n, 1n, -1n, 2n ** 31n, -(2n ** 31n), 2n ** 63n - 1n, -(2n ** 63n)];
    for (const v of values) assert.equal(zigzagDecode(zigzagEncode(v)), v);
  });
});

describe("two's-complement reinterpretation", () => {
  it("converts unsigned 64-bit to signed", () => {
    assert.equal(toSigned64(0n), 0n);
    assert.equal(toSigned64(2n ** 64n - 1n), -1n);
    assert.equal(toSigned64(2n ** 63n), -(2n ** 63n));
    assert.equal(toSigned64(2n ** 63n - 1n), 2n ** 63n - 1n);
  });
});
