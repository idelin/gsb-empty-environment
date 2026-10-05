import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BitWriter } from '../src/bitstream.ts';
import { crc32 } from '../src/crc32.ts';
import { canonicalCodes } from '../src/huffman.ts';
import {
  BlockDecoder,
  FIXED_HEADER_SIZE,
  FormatError,
  SYM_EOB,
  decodeBlock,
  encodeBlock,
  parseHeader,
} from '../src/block.ts';
import { tokenize } from '../src/lz77.ts';
import { expandTokens } from './reference-expander.ts';
import { assertBytesEqual, decodeStreaming, randomBytes, rng } from './helpers.ts';

const WINDOW = 65535;

// 手工构造一个块：给定码长表与载荷位写入过程。
function craftBlock(
  codeLengths: number[],
  writePayload: (w: BitWriter, codes: Uint32Array) => void,
  meta: { originalLength: number; windowLimit?: number; crc?: number; payloadBytesDelta?: number },
): Uint8Array {
  const codes = canonicalCodes(codeLengths);
  const writer = new BitWriter();
  writePayload(writer, codes);
  const payload = writer.finish();
  const windowLimit = meta.windowLimit ?? WINDOW;
  const header = new Uint8Array(FIXED_HEADER_SIZE + codeLengths.length);
  const view = new DataView(header.buffer);
  header.set([0x4c, 0x5a, 0x48, 0x31], 0);
  view.setUint32(4, meta.originalLength, true);
  view.setUint32(8, windowLimit, true);
  view.setUint32(12, payload.length + (meta.payloadBytesDelta ?? 0), true);
  view.setUint32(16, meta.crc ?? 0, true);
  view.setUint16(20, codeLengths.length, true);
  header.set(codeLengths, FIXED_HEADER_SIZE);
  const block = new Uint8Array(header.length + payload.length);
  block.set(header, 0);
  block.set(payload, header.length);
  return block;
}

function expectFormatError(fn: () => void, pattern?: RegExp): void {
  assert.throws(fn, (err: unknown) => {
    if (!(err instanceof FormatError)) {
      throw new Error(`期望 FormatError，实际: ${String(err)}`);
    }
    if (pattern && !pattern.test(err.message)) {
      throw new Error(`错误信息不匹配 ${pattern}: ${err.message}`);
    }
    return true;
  });
}

// 解码到出错的便捷封装：吞掉结果，只关心是否抛错。
function runDecoder(block: Uint8Array): Uint8Array {
  return decodeStreaming(block, [3, 1, 7], [5, 1, 11]);
}

test('距离 1 的长重复按重叠语义展开', () => {
  const data = new Uint8Array(200000).fill(0x78);
  const tokens = tokenize(data, WINDOW);
  const long = tokens.filter((t) => t.type === 'match' && t.distance === 1 && t.length === 258);
  if (long.length === 0) throw new Error('应产生 distance=1 的长匹配');
  assertBytesEqual(expandTokens(tokens), data);
  const block = encodeBlock(data, WINDOW);
  assertBytesEqual(decodeBlock(block), data);
  // 逐字节额度下也不得重复或丢失输出
  assertBytesEqual(decodeStreaming(block, [5], [1]), data);
});

test('空块往返', () => {
  const empty = new Uint8Array(0);
  const block = encodeBlock(empty, WINDOW);
  assertBytesEqual(decodeBlock(block), empty);
  assertBytesEqual(decodeStreaming(block, [1], [1]), empty);
});

test('空块的码长表只含块结束符号（单符号欠订阅码）', () => {
  const info = parseHeader(encodeBlock(new Uint8Array(0), WINDOW));
  const used = [...info.codeLengths].filter((l) => l > 0);
  assert.equal(used.length, 1);
  assert.equal(info.codeLengths[SYM_EOB], 1);
});

test('手工构造的单符号码表（仅 EOB）解码为空输出', () => {
  const lengths = new Array<number>(257).fill(0);
  lengths[SYM_EOB] = 1;
  const block = craftBlock(
    lengths,
    (w, codes) => w.writeCode(codes[SYM_EOB], 1),
    { originalLength: 0, crc: crc32(new Uint8Array(0)) },
  );
  assertBytesEqual(decodeBlock(block), new Uint8Array(0));
});

test('只有一种符号的数据: 多种长度往返', () => {
  for (const len of [1, 2, 3, 258, 259, 1000, 100000]) {
    const data = new Uint8Array(len).fill(0x61);
    assertBytesEqual(decodeBlock(encodeBlock(data, WINDOW)), data);
  }
});

test('每个字节截断都必须检出', () => {
  const rand = rng(999);
  const data = randomBytes(rand, 400, 5);
  const block = encodeBlock(data, WINDOW);
  for (let k = 0; k < block.length; k++) {
    const prefix = block.subarray(0, k);
    expectFormatError(() => runDecoder(prefix), undefined);
  }
});

test('声明长度溢出: 改小、改大、置零、置最大都被拒绝', () => {
  const data = randomBytes(rng(7), 100, 3);
  const block = encodeBlock(data, WINDOW);
  const patch = (value: number) => {
    const b = block.slice();
    new DataView(b.buffer).setUint32(4, value >>> 0, true);
    return b;
  };
  expectFormatError(() => decodeBlock(patch(data.length - 1)), /展开超限|长度不符/);
  expectFormatError(() => decodeBlock(patch(data.length + 1)), /长度不符/);
  expectFormatError(() => decodeBlock(patch(0)), /展开超限|长度不符/);
  expectFormatError(() => decodeBlock(patch(0xffffffff)), /长度不符/);
});

test('声明长度巨大不会导致内存放大或误收', () => {
  // 载荷只产出 3 字节，头部却声明 4GB：必须在 EOB 处拒绝，且全程无大额分配。
  const lengths = new Array<number>(257).fill(0);
  lengths[65] = 2;
  lengths[66] = 2;
  lengths[SYM_EOB] = 2;
  const data = new Uint8Array([65, 65, 65]);
  const block = craftBlock(
    lengths,
    (w, codes) => {
      w.writeCode(codes[65], 2);
      w.writeCode(codes[65], 2);
      w.writeCode(codes[65], 2);
      w.writeCode(codes[SYM_EOB], 2);
    },
    { originalLength: 0xffffffff, crc: crc32(data) },
  );
  expectFormatError(() => decodeBlock(block), /长度不符/);
});

test('实际展开超过声明长度立即报错', () => {
  const lengths = new Array<number>(257).fill(0);
  lengths[65] = 1;
  lengths[SYM_EOB] = 1; // 两个码长 1：Kraft = 1，合法
  const block = craftBlock(
    lengths,
    (w, codes) => {
      w.writeCode(codes[65], 1);
      w.writeCode(codes[65], 1); // 第二个字面量即超限
      w.writeCode(codes[SYM_EOB], 1);
    },
    { originalLength: 1, crc: crc32(new Uint8Array([65, 65])) },
  );
  expectFormatError(() => decodeBlock(block), /展开超限/);
});

test('过度订阅的码表被拒绝', () => {
  const lengths = new Array<number>(257).fill(0);
  lengths[0] = 1;
  lengths[1] = 1;
  lengths[2] = 1; // Kraft = 3/2 > 1
  lengths[SYM_EOB] = 2;
  const block = craftBlock(lengths, (w, codes) => w.writeCode(codes[SYM_EOB], 2), {
    originalLength: 0,
  });
  expectFormatError(() => decodeBlock(block), /过度订阅/);
});

test('非法距离: 0、超过历史、超过窗口上限', () => {
  const mkLengths = () => {
    const l = new Array<number>(258).fill(0);
    l[65] = 2;
    l[SYM_EOB] = 2;
    l[257] = 2; // 长度码 257 = 长度 3，无额外位
    return l;
  };
  // 距离 0
  expectFormatError(
    () =>
      decodeBlock(
        craftBlock(
          mkLengths(),
          (w, codes) => {
            w.writeCode(codes[65], 2);
            w.writeCode(codes[257], 2);
            w.writeBits(0, 16);
            w.writeCode(codes[SYM_EOB], 2);
          },
          { originalLength: 4 },
        ),
      ),
    /非法距离/,
  );
  // 距离超过已产生历史（历史为 0，距离 1）
  expectFormatError(
    () =>
      decodeBlock(
        craftBlock(
          mkLengths(),
          (w, codes) => {
            w.writeCode(codes[257], 2);
            w.writeBits(1, 16);
            w.writeCode(codes[SYM_EOB], 2);
          },
          { originalLength: 3 },
        ),
      ),
    /非法距离.*历史/,
  );
  // 距离超过窗口上限（窗口 1，距离 2；历史 1）
  expectFormatError(
    () =>
      decodeBlock(
        craftBlock(
          mkLengths(),
          (w, codes) => {
            w.writeCode(codes[65], 2);
            w.writeCode(codes[257], 2);
            w.writeBits(2, 16);
            w.writeCode(codes[SYM_EOB], 2);
          },
          { originalLength: 4, windowLimit: 1 },
        ),
      ),
    /非法距离.*窗口上限/,
  );
});

test('CRC 字段任何翻转都被检出', () => {
  const data = randomBytes(rng(31337), 300, 7);
  const block = encodeBlock(data, WINDOW);
  for (let i = 16; i < 20; i++) {
    const bad = block.slice();
    bad[i] ^= 0x01;
    expectFormatError(() => decodeBlock(bad), /CRC/);
  }
});

test('载荷与码长表损坏: 要么报错，要么结果仍正确（绝不错误静默）', () => {
  const rand = rng(555);
  const data = randomBytes(rand, 500, 9);
  const block = encodeBlock(data, WINDOW);
  const info = parseHeader(block);
  for (let i = FIXED_HEADER_SIZE; i < block.length; i++) {
    const bad = block.slice();
    bad[i] ^= 0x40;
    try {
      const out = decodeBlock(bad);
      assertBytesEqual(out, data); // 未报错则必须输出完全正确
    } catch (err) {
      if (!(err instanceof FormatError)) throw err;
    }
  }
  void info;
});

test('块结束后填充位非零被拒绝', () => {
  // 单符号表（仅 EOB，码长 1），载荷字节 0b00000010：bit0=EOB(0)，bit1 填充=1。
  const lengths = new Array<number>(257).fill(0);
  lengths[SYM_EOB] = 1;
  const header = new Uint8Array(FIXED_HEADER_SIZE + 257);
  const view = new DataView(header.buffer);
  header.set([0x4c, 0x5a, 0x48, 0x31], 0);
  view.setUint32(4, 0, true);
  view.setUint32(8, WINDOW, true);
  view.setUint32(12, 1, true);
  view.setUint32(16, crc32(new Uint8Array(0)), true);
  view.setUint16(20, 257, true);
  header.set(lengths, FIXED_HEADER_SIZE);
  const block = new Uint8Array(header.length + 1);
  block.set(header, 0);
  block[header.length] = 0x02;
  expectFormatError(() => decodeBlock(block), /填充位/);
});

test('载荷长度声明多于实际消费被拒绝', () => {
  const block = encodeBlock(new Uint8Array(0), WINDOW).slice();
  const view = new DataView(block.buffer);
  view.setUint32(12, view.getUint32(12, true) + 1, true);
  const padded = new Uint8Array(block.length + 1);
  padded.set(block, 0);
  expectFormatError(() => decodeBlock(padded), /载荷长度不符/);
});

test('损坏的块不污染下一个独立块', () => {
  const a = randomBytes(rng(1), 300, 4);
  const b = randomBytes(rng(2), 300, 4);
  const blockA = encodeBlock(a, WINDOW);
  const blockB = encodeBlock(b, WINDOW);
  const badA = blockA.slice();
  badA[16] ^= 0xff; // 破坏第一块的 CRC
  expectFormatError(() => decodeBlock(badA), /CRC/);
  // 第二块用全新的解码器独立解码，不受前块影响
  assertBytesEqual(decodeBlock(blockB), b);
  assertBytesEqual(decodeStreaming(blockB, [1], [1]), b);
});

test('输出额度: 暂停后继续，绝不重复输出', () => {
  const rand = rng(2024);
  const data = randomBytes(rand, 5000, 6);
  const block = encodeBlock(data, WINDOW);
  const decoder = new BlockDecoder();
  decoder.push(block);
  const out: number[] = [];
  const quotas = [1, 2, 3, 5, 8, 13, 21, 100];
  let qi = 0;
  for (;;) {
    const quota = quotas[qi++ % quotas.length];
    const { output, done } = decoder.decode(quota);
    if (output.length > quota) throw new Error('输出超过额度');
    for (const byte of output) out.push(byte);
    if (done) break;
  }
  if (out.length !== data.length) throw new Error(`总输出 ${out.length} != ${data.length}`);
  assertBytesEqual(Uint8Array.from(out), data);
  // 完成后再调用：空输出且 done
  const after = decoder.decode(10);
  assert.equal(after.output.length, 0);
  assert.equal(after.done, true);
});
