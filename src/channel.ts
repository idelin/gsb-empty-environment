import { BitReader, BitWriter } from './bitstream.ts';

/** 不可靠信道模拟：删除从 bitPos 开始的 count 个比特（丢位 / 突发抹除）。 */
export function deleteBits(data: Uint8Array, bitPos: number, count: number): Uint8Array {
  const reader = new BitReader(data);
  const writer = new BitWriter();
  const total = data.length * 8;
  for (let i = 0; i < total; i += 1) {
    const bit = reader.readBit();
    if (i >= bitPos && i < bitPos + count) continue;
    writer.writeBit(bit);
  }
  return writer.toUint8Array();
}

/** 不可靠信道模拟：反转 bitPos 处的一个比特。 */
export function flipBit(data: Uint8Array, bitPos: number): Uint8Array {
  const copy = Uint8Array.from(data);
  copy[bitPos >> 3] ^= 1 << (7 - (bitPos & 7));
  return copy;
}
