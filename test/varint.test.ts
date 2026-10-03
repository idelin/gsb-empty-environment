import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeVarint,
  encodeVarint,
  toSigned64,
  toUnsigned64,
  varintLength,
  zigzagDecode,
  zigzagEncode,
} from '../src/varint.ts';
import { isProtoError } from '../src/errors.ts';

const ROUNDTRIP_VALUES = [
  0n, 1n, 127n, 128n, 300n, 16383n, 16384n,
  (1n << 31n) - 1n, 1n << 31n, (1n << 32n) - 1n, 1n << 32n,
  (1n << 63n) - 1n, 1n << 63n, (1n << 64n) - 1n,
];

test('varint 边界值往返一致', () => {
  for (const v of ROUNDTRIP_VALUES) {
    const enc = encodeVarint(v);
    const dec = decodeVarint(enc, 0);
    assert.equal(dec.value, v, `值 ${v} 往返失败`);
    assert.equal(dec.bytesRead, enc.length);
    assert.equal(enc.length, varintLength(v));
  }
});

test('varint 已知编码与 protobuf 规范一致', () => {
  assert.deepEqual(encodeVarint(1), Uint8Array.from([0x01]));
  assert.deepEqual(encodeVarint(300), Uint8Array.from([0xac, 0x02]));
  assert.deepEqual(encodeVarint(0), Uint8Array.from([0x00]));
});

test('varint 支持 number 输入并截断到 64 位', () => {
  const dec = decodeVarint(encodeVarint(2 ** 53), 0);
  assert.equal(dec.value, BigInt(2 ** 53));
});

test('varint 截断：缓冲区耗尽抛 TRUNCATED 且不越界', () => {
  const full = encodeVarint((1n << 64n) - 1n); // 10 字节
  for (let len = 1; len < full.length; len++) {
    assert.throws(
      () => decodeVarint(full.subarray(0, len), 0),
      (err: unknown) => isProtoError(err, 'TRUNCATED'),
      `长度 ${len} 应报截断`,
    );
  }
});

test('varint 溢出：11 字节或第 10 字节超位抛 OVERFLOW', () => {
  const eleven = Uint8Array.from([...Array(10).fill(0x80), 0x01]);
  assert.throws(() => decodeVarint(eleven, 0), (e: unknown) => isProtoError(e, 'OVERFLOW'));
  const badTenth = Uint8Array.from([...Array(9).fill(0x80), 0x7f]);
  assert.throws(() => decodeVarint(badTenth, 0), (e: unknown) => isProtoError(e, 'OVERFLOW'));
});

test('varint 带偏移解码', () => {
  const buf = Uint8Array.from([0xff, 0xac, 0x02, 0xff]);
  const dec = decodeVarint(buf, 1, 3);
  assert.equal(dec.value, 300n);
  assert.equal(dec.bytesRead, 2);
});

test('zigzag 边界值往返', () => {
  const values = [0n, -1n, 1n, -2n, 2n, 2147483647n, -2147483648n,
    (1n << 63n) - 1n, -(1n << 63n)];
  for (const v of values) {
    assert.equal(zigzagDecode(zigzagEncode(v)), v, `zigzag(${v}) 往返失败`);
  }
  assert.equal(zigzagEncode(0), 0n);
  assert.equal(zigzagEncode(-1), 1n);
  assert.equal(zigzagEncode(1), 2n);
  assert.equal(zigzagEncode(-2), 3n);
});

test('int64 补码转换', () => {
  assert.equal(toUnsigned64(-1), (1n << 64n) - 1n);
  assert.equal(toSigned64((1n << 64n) - 1n), -1n);
  assert.equal(toSigned64(toUnsigned64(-(1n << 63n))), -(1n << 63n));
});
