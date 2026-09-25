import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rleEncode, rleDecode } from '../src/rle.ts';

function lcg(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s & 0xff;
  };
}

test('RLE 往返：随机数据', () => {
  const rand = lcg(42);
  const src = Uint8Array.from({ length: 4096 }, () => rand());
  const decoded = rleDecode(rleEncode(src), 1, src.length);
  assert.deepEqual([...decoded], [...src]);
});

test('RLE 往返：纯色数据且显著压缩', () => {
  const src = new Uint8Array(10000).fill(200);
  const packed = rleEncode(src);
  assert.ok(packed.length < src.length / 10, `packed=${packed.length}`);
  assert.deepEqual([...rleDecode(packed, 1, src.length)], [...src]);
});

test('RLE 往返：混合游程与字面量、空输入', () => {
  const src = Uint8Array.from([1, 1, 1, 1, 2, 3, 4, 5, 5, 5, 6, 7, 8, 9, 9, 9, 9, 9, 10]);
  assert.deepEqual([...rleDecode(rleEncode(src), 1, src.length)], [...src]);
  assert.equal(rleEncode(new Uint8Array(0)).length, 0);
  assert.equal(rleDecode(new Uint8Array(0), 1, 0).length, 0);
});

test('RLE 长度校验', () => {
  const packed = rleEncode(new Uint8Array([7, 7, 7]));
  assert.throws(() => rleDecode(packed, 1, 99), /length mismatch/);
});

test('RLE 像素单元模式：纯色 RGBA 流高效压缩且往返一致', () => {
  // 64x64 纯红像素：字节级无游程，像素级全是游程
  const src = new Uint8Array(64 * 64 * 4);
  for (let i = 0; i < 64 * 64; i++) src.set([255, 0, 0, 255], i * 4);
  const packed = rleEncode(src, 4);
  assert.ok(packed.length < src.length / 100, `packed=${packed.length}`);
  assert.deepEqual([...rleDecode(packed, 4, src.length)], [...src]);

  // 混合：部分重复像素 + 部分随机像素
  const rand = lcg(9);
  const mixed = new Uint8Array(1024 * 4);
  for (let i = 0; i < 1024; i++) {
    if (i % 3 === 0) mixed.set([1, 2, 3, 4], i * 4);
    else mixed.set([rand(), rand(), rand(), rand()], i * 4);
  }
  assert.deepEqual([...rleDecode(rleEncode(mixed, 4), 4, mixed.length)], [...mixed]);
});
