import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encodeVarint, decodeVarint,
  zigzagEncode32, zigzagDecode32, zigzagEncode64, zigzagDecode64,
  MAX_VARINT_BYTES,
} from '../src/varint.ts';
import { ProtobufDecodeError } from '../src/errors.ts';

test('varint: 7 位压缩边界值往返', () => {
  const cases: Array<[bigint, number[]]> = [
    [0n, [0x00]],
    [1n, [0x01]],
    [127n, [0x7f]],                 // 1 字节上限
    [128n, [0x80, 0x01]],           // 进入 2 字节
    [300n, [0xac, 0x02]],           // protobuf 官方示例
    [16383n, [0xff, 0x7f]],
    [16384n, [0x80, 0x80, 0x01]],
    [0xffffffffn, [0xff, 0xff, 0xff, 0xff, 0x0f]],
    [0xffffffffffffffffn, [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]],
  ];
  for (const [value, bytes] of cases) {
    assert.deepEqual([...encodeVarint(value)], bytes, `encode ${value}`);
    const { value: decoded, bytesRead } = decodeVarint(new Uint8Array(bytes));
    assert.equal(decoded, value, `decode ${value}`);
    assert.equal(bytesRead, bytes.length);
  }
});

test('varint: 负数以 64 位补码编码为 10 字节', () => {
  const enc = encodeVarint(-1);
  assert.equal(enc.length, MAX_VARINT_BYTES);
  assert.equal(decodeVarint(enc).value, 0xffffffffffffffffn);
});

test('varint: 截断时抛出带偏移的 ProtobufDecodeError', () => {
  assert.throws(() => decodeVarint(new Uint8Array([0x80])), (e: unknown) => {
    assert.ok(e instanceof ProtobufDecodeError);
    assert.equal((e as ProtobufDecodeError).offset, 1);
    assert.match((e as Error).message, /Truncated varint/);
    return true;
  });
  assert.throws(() => decodeVarint(new Uint8Array([])), ProtobufDecodeError);
});

test('varint: 超过 10 字节视为溢出畸形', () => {
  const bad = new Uint8Array(11).fill(0x80);
  assert.throws(() => decodeVarint(bad), /exceeds 10 bytes/);
});

test('varint: 随机大整数往返', () => {
  for (let i = 0; i < 2000; i++) {
    const v = BigInt('0x' + [...crypto.getRandomValues(new Uint8Array(8))].map(b => b.toString(16).padStart(2, '0')).join(''));
    assert.equal(decodeVarint(encodeVarint(v)).value, v);
  }
});

test('zigzag32: 符号映射与极值', () => {
  assert.equal(zigzagEncode32(0), 0);
  assert.equal(zigzagEncode32(-1), 1);
  assert.equal(zigzagEncode32(1), 2);
  assert.equal(zigzagEncode32(-2), 3);
  assert.equal(zigzagEncode32(2147483647), 0xfffffffe);
  assert.equal(zigzagEncode32(-2147483648), 0xffffffff);
  for (const v of [0, 1, -1, 2, -2, 123456, -123456, 2147483647, -2147483648]) {
    assert.equal(zigzagDecode32(zigzagEncode32(v)), v);
  }
});

test('zigzag64: 符号映射与 64 位极值', () => {
  assert.equal(zigzagEncode64(0n), 0n);
  assert.equal(zigzagEncode64(-1n), 1n);
  assert.equal(zigzagEncode64(1n), 2n);
  assert.equal(zigzagEncode64(9223372036854775807n), 0xfffffffffffffffen);
  assert.equal(zigzagEncode64(-9223372036854775808n), 0xffffffffffffffffn);
  for (const v of [0n, 1n, -1n, 2n, -2n, 9223372036854775807n, -9223372036854775808n]) {
    assert.equal(zigzagDecode64(zigzagEncode64(v)), v);
  }
});
