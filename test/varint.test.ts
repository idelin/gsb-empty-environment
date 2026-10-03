import { test } from "node:test";
import assert from "node:assert/strict";
import {
  decodeFixed32,
  decodeFixed64,
  decodeVarint,
  encodeFixed32,
  encodeFixed64,
  encodeVarint,
  MalformedVarintError,
  TruncatedFieldError,
  varintLength,
  zigzagDecode,
  zigzagEncode,
} from "../src/varint.ts";

const VARINT_VECTORS: Array<[bigint, number[]]> = [
  [0n, [0x00]],
  [1n, [0x01]],
  [127n, [0x7f]],
  [128n, [0x80, 0x01]],
  [300n, [0xac, 0x02]],
  [16383n, [0xff, 0x7f]],
  [16384n, [0x80, 0x80, 0x01]],
  [4294967295n, [0xff, 0xff, 0xff, 0xff, 0x0f]],
  [4294967296n, [0x80, 0x80, 0x80, 0x80, 0x10]],
  [
    18446744073709551615n, // 2^64 - 1
    [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01],
  ],
];

test("varint: canonical vectors round-trip", () => {
  for (const [value, bytes] of VARINT_VECTORS) {
    const encoded = encodeVarint(value);
    assert.deepEqual([...encoded], bytes, `encode ${value}`);
    const decoded = decodeVarint(encoded);
    assert.equal(decoded.value, value, `decode ${value}`);
    assert.equal(decoded.bytesRead, bytes.length);
    assert.equal(varintLength(value), bytes.length);
  }
});

test("varint: 7-bit group boundaries", () => {
  for (let bits = 0n; bits <= 64n; bits++) {
    const value = bits === 0n ? 0n : (1n << bits) - 1n;
    const { value: decoded, bytesRead } = decodeVarint(encodeVarint(value));
    assert.equal(decoded, value);
    assert.equal(bytesRead, Math.max(1, Math.ceil(Number(bits) / 7)));
  }
});

test("varint: decode respects offset and reports bytesRead", () => {
  const buf = new Uint8Array([0x99, 0xac, 0x02, 0x07]);
  const r = decodeVarint(buf, 1);
  assert.equal(r.value, 300n);
  assert.equal(r.bytesRead, 2);
});

test("varint: truncated buffer raises TruncatedFieldError", () => {
  assert.throws(() => decodeVarint(new Uint8Array([0x80])), TruncatedFieldError);
  assert.throws(
    () => decodeVarint(new Uint8Array([0xff, 0xff, 0xff])),
    (e: unknown) => e instanceof TruncatedFieldError && (e as TruncatedFieldError).available === 3,
  );
  assert.throws(() => decodeVarint(new Uint8Array(0)), TruncatedFieldError);
});

test("varint: overlong varint (>10 bytes) is malformed", () => {
  const tooLong = new Uint8Array(11).fill(0x80);
  tooLong[10] = 0x00;
  assert.throws(() => decodeVarint(tooLong), MalformedVarintError);
});

test("varint: 10th byte may not overflow 64 bits", () => {
  const overflow = new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02]);
  assert.throws(() => decodeVarint(overflow), MalformedVarintError);
});

test("zigzag: signed mapping vectors", () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n],
    [-1n, 1n],
    [1n, 2n],
    [-2n, 3n],
    [2147483647n, 4294967294n],
    [-2147483648n, 4294967295n],
    [9223372036854775807n, 18446744073709551614n],
    [-9223372036854775808n, 18446744073709551615n],
  ];
  for (const [signed, unsigned] of cases) {
    assert.equal(zigzagEncode(signed), unsigned, `encode ${signed}`);
    assert.equal(zigzagDecode(unsigned), signed, `decode ${unsigned}`);
  }
});

test("fixed32: little-endian round-trip and truncation", () => {
  assert.deepEqual([...encodeFixed32(0xdeadbeef)], [0xef, 0xbe, 0xad, 0xde]);
  assert.equal(decodeFixed32(encodeFixed32(0)), 0);
  assert.equal(decodeFixed32(encodeFixed32(0xffffffff)), 0xffffffff);
  assert.throws(() => decodeFixed32(new Uint8Array(3)), TruncatedFieldError);
});

test("fixed64: little-endian round-trip and truncation", () => {
  const max = 0xffffffffffffffffn;
  assert.equal(decodeFixed64(encodeFixed64(max)), max);
  assert.deepEqual([...encodeFixed64(1n)], [1, 0, 0, 0, 0, 0, 0, 0]);
  assert.throws(() => decodeFixed64(new Uint8Array(7)), TruncatedFieldError);
});
