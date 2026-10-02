import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BitReader, BitWriter } from '../src/bitstream.ts';
import { buildCodebook } from '../src/huffman.ts';
import {
  decodePlain,
  decodeResilient,
  encodePlain,
  encodeResilient,
  SEGMENT_SIZE_BYTES,
} from '../src/resilient.ts';
import { deleteBits, flipBit } from '../src/channel.ts';
import { buildSampleText } from '../src/sample-text.ts';
import { longestCommonSuffixLength } from '../src/metrics.ts';

function toSymbols(text: string): number[] {
  return [...text].map((ch) => ch.charCodeAt(0));
}

/** 在（未受损的）字节流中定位所有同步字的字节偏移。 */
function findSyncBytes(data: Uint8Array): number[] {
  const positions: number[] = [];
  for (let i = 0; i + 4 <= data.length; i += 1) {
    if (data[i] === 0x1a && data[i + 1] === 0xcf && data[i + 2] === 0xfc && data[i + 3] === 0x1d) {
      positions.push(i);
    }
  }
  return positions;
}

test('BitWriter/BitReader 位级往返一致', () => {
  const writer = new BitWriter();
  writer.writeBits(0x1acffc1d, 32);
  writer.writeBits(5, 3);
  writer.writeBits(255, 8);
  writer.writeBits(1, 1);
  const reader = new BitReader(writer.toUint8Array());
  assert.equal(reader.readBits(32), 0x1acffc1d);
  assert.equal(reader.readBits(3), 5);
  assert.equal(reader.readBits(8), 255);
  assert.equal(reader.readBits(1), 1);
});

test('Huffman 普通流往返一致且有压缩效果', () => {
  const symbols = toSymbols(buildSampleText(1000));
  const codebook = buildCodebook(symbols);
  const plain = encodePlain(symbols, codebook);
  assert.deepEqual(decodePlain(plain.data), symbols);
  assert.ok(plain.data.length < symbols.length * 0.7);
});

test('干净信道：含文件头的容错流完整往返', () => {
  const symbols = toSymbols(buildSampleText(1500));
  const codebook = buildCodebook(symbols);
  const encoded = encodeResilient(symbols, codebook);
  const result = decodeResilient(encoded.data); // 不传码表，从文件头自描述恢复
  assert.deepEqual(result.symbols, symbols);
  assert.equal(result.segmentsLost, 0);
  assert.equal(result.resyncs.length, 0);
});

test('单比特反转：仅损失所在分段，错误不扩散', () => {
  const symbols = toSymbols(buildSampleText(2000));
  const codebook = buildCodebook(symbols);
  const encoded = encodeResilient(symbols, codebook);
  const syncs = findSyncBytes(encoded.data);
  assert.ok(syncs.length >= 3);
  // 在第 2 个分段的载荷中间反转 1 个比特
  const damaged = flipBit(encoded.data, (syncs[1] + 50) * 8);
  const result = decodeResilient(damaged);

  assert.equal(result.segmentsLost, 1);
  assert.equal(result.segmentsOk, encoded.segmentCount - 1);
  assert.equal(result.resyncs.length, 1);
  const ratio = result.symbols.length / symbols.length;
  assert.ok(ratio >= 0.9, `恢复比例 ${ratio} 应 >= 0.9`);
  // 解码结果 = 原文挖去一个连续分段（前缀、后缀均与原位对齐）
  let prefix = 0;
  while (prefix < result.symbols.length && result.symbols[prefix] === symbols[prefix]) prefix += 1;
  let suffix = 0;
  while (
    suffix < result.symbols.length - prefix &&
    result.symbols[result.symbols.length - 1 - suffix] === symbols[symbols.length - 1 - suffix]
  ) {
    suffix += 1;
  }
  assert.equal(prefix + suffix, result.symbols.length);
});

test('验收：中间抹去3bit，容错解码器在第100字节的同步字重锁并挽救>=90%内容', (t) => {
  const symbols = toSymbols(buildSampleText(4000));
  const codebook = buildCodebook(symbols);

  // 通信双方预先约定码表（无文件头）：同步字恰好落在第 0、100、200… 字节
  const encoded = encodeResilient(symbols, codebook, { withHeader: false });
  assert.deepEqual(findSyncBytes(encoded.data).slice(0, 3), [0, 100, 200]);

  // 信道事故：第 50 字节处（第 0 段载荷中部）被抹去 3 个比特
  const damaged = deleteBits(encoded.data, 50 * 8, 3);

  // 对照组：普通解码器，从损坏点开始全部乱码
  const plain = encodePlain(symbols, codebook, { withHeader: false });
  const naive = decodePlain(deleteBits(plain.data, 50 * 8, 3), codebook);
  const naiveRatio = longestCommonSuffixLength(symbols, naive) / symbols.length;
  t.diagnostic(`普通解码器损坏点后可恢复比例: ${(naiveRatio * 100).toFixed(2)}%`);
  assert.ok(naiveRatio < 0.5, '普通解码器应当无法恢复后续内容');

  // 实验组：容错解码器
  const result = decodeResilient(damaged, codebook);
  assert.equal(result.segmentsLost, 1, '仅第 0 段被隔离');
  // 重新锁定：原第 100 字节处的同步字，因抹去 3bit 前移到比特 797
  assert.deepEqual(
    result.resyncs.map((r) => r.bitPos),
    [100 * 8 - 3],
    '应在原第 100 字节处的同步字重新锁定相位',
  );
  const ratio = result.symbols.length / symbols.length;
  t.diagnostic(`容错解码器挽救内容比例: ${(ratio * 100).toFixed(2)}%`);
  assert.ok(ratio >= 0.9, `挽救比例 ${ratio} 应 >= 0.9`);
  // 跳过损坏段后，后续符号按序完整恢复（解码结果 = 原文删去第 0 段）
  assert.deepEqual(result.symbols, symbols.slice(encoded.segmentSymbolCounts[0]));
});
