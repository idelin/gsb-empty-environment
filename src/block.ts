// LZH1 块编解码。格式与位序见 FORMAT.md。
import { BitWriter } from './bitstream.ts';
import { FormatError } from './errors.ts';

export { FormatError };
import { Crc32, crc32 } from './crc32.ts';
import {
  CanonicalDecoder,
  NUM_SYMBOLS,
  buildCodeLengths,
  canonicalCodes,
} from './huffman.ts';
import {
  MAX_MATCH,
  MAX_WINDOW,
  MIN_MATCH,
  tokenize,
} from './lz77.ts';
import type { Token } from './lz77.ts';


export const MAGIC = [0x4c, 0x5a, 0x48, 0x31]; // "LZH1"
export const FIXED_HEADER_SIZE = 22;
export const SYM_EOB = 256;
export const NUM_LEN_CODES = 29; // 符号 257..285

// 长度码表：基数与额外位（长度 3..258）。
export const LEN_BASE = [
  3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67,
  83, 99, 115, 131, 163, 195, 227, 258,
];
export const LEN_EXTRA = [
  0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5,
  5, 5, 5, 0,
];

// 长度(3..258) -> 长度码索引(0..28)。258 固定映射到最后一个码（符号 285）。
const LEN_TO_CODE = (() => {
  const table = new Uint8Array(MAX_MATCH + 1);
  for (let c = 0; c < NUM_LEN_CODES; c++) {
    const lo = LEN_BASE[c];
    const hi = Math.min(MAX_MATCH, lo + ((1 << LEN_EXTRA[c]) - 1));
    for (let l = lo; l <= hi; l++) table[l] = c;
  }
  return table;
})();

export interface BlockInfo {
  originalLength: number;
  windowLimit: number;
  payloadBytes: number;
  crc32: number;
  numSymbols: number;
  codeLengths: Uint8Array;
  headerBytes: number;
  totalBytes: number;
}

// ---------------- 编码 ----------------

// 把 token 序列编码为完整块。original 用于计算声明长度与 CRC。
export function encodeTokensAsBlock(
  tokens: Token[],
  windowLimit: number,
  original: Uint8Array,
): Uint8Array {
  if (!Number.isInteger(windowLimit) || windowLimit < 1 || windowLimit > MAX_WINDOW) {
    throw new RangeError(`windowLimit 必须在 1..${MAX_WINDOW} 之间`);
  }
  const freqs = new Array<number>(NUM_SYMBOLS).fill(0);
  freqs[SYM_EOB] = 1;
  for (const t of tokens) {
    if (t.type === 'lit') {
      freqs[t.value]++;
    } else {
      if (
        !Number.isInteger(t.length) ||
        t.length < MIN_MATCH ||
        t.length > MAX_MATCH ||
        !Number.isInteger(t.distance) ||
        t.distance < 1 ||
        t.distance > windowLimit
      ) {
        throw new RangeError(`非法匹配令牌: len=${t.length} dist=${t.distance}`);
      }
      freqs[257 + LEN_TO_CODE[t.length]]++;
    }
  }

  const lengths = buildCodeLengths(freqs);
  let numSymbols = NUM_SYMBOLS;
  while (numSymbols > 1 && lengths[numSymbols - 1] === 0) numSymbols--;
  const codes = canonicalCodes(lengths);

  const writer = new BitWriter();
  for (const t of tokens) {
    if (t.type === 'lit') {
      writer.writeCode(codes[t.value], lengths[t.value]);
    } else {
      const c = LEN_TO_CODE[t.length];
      const sym = 257 + c;
      writer.writeCode(codes[sym], lengths[sym]);
      writer.writeBits(t.length - LEN_BASE[c], LEN_EXTRA[c]);
      writer.writeBits(t.distance, 16);
    }
  }
  writer.writeCode(codes[SYM_EOB], lengths[SYM_EOB]);
  const payload = writer.finish();

  const headerBytes = FIXED_HEADER_SIZE + numSymbols;
  const block = new Uint8Array(headerBytes + payload.length);
  const view = new DataView(block.buffer);
  block.set(MAGIC, 0);
  view.setUint32(4, original.length, true);
  view.setUint32(8, windowLimit, true);
  view.setUint32(12, payload.length, true);
  view.setUint32(16, crc32(original), true);
  view.setUint16(20, numSymbols, true);
  block.set(lengths.subarray(0, numSymbols), FIXED_HEADER_SIZE);
  block.set(payload, headerBytes);
  return block;
}

export function encodeBlock(data: Uint8Array, windowLimit: number = MAX_WINDOW): Uint8Array {
  return encodeTokensAsBlock(tokenize(data, windowLimit), windowLimit, data);
}

// ---------------- 解码 ----------------

// 解析并校验块头。data 至少需要包含完整头部，否则抛 FormatError。
export function parseHeader(data: Uint8Array): BlockInfo {
  if (data.length < FIXED_HEADER_SIZE) {
    throw new FormatError('头部不完整');
  }
  for (let i = 0; i < 4; i++) {
    if (data[i] !== MAGIC[i]) throw new FormatError('magic 不符');
  }
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  const originalLength = view.getUint32(4, true);
  const windowLimit = view.getUint32(8, true);
  const payloadBytes = view.getUint32(12, true);
  const crc = view.getUint32(16, true);
  const numSymbols = view.getUint16(20, true);
  if (windowLimit < 1 || windowLimit > MAX_WINDOW) {
    throw new FormatError(`非法窗口上限: ${windowLimit}`);
  }
  if (payloadBytes < 1) {
    throw new FormatError('载荷长度必须 ≥ 1');
  }
  if (numSymbols < 1 || numSymbols > NUM_SYMBOLS) {
    throw new FormatError(`非法码长表大小: ${numSymbols}`);
  }
  const headerBytes = FIXED_HEADER_SIZE + numSymbols;
  if (data.length < headerBytes) {
    throw new FormatError('码长表不完整');
  }
  const codeLengths = data.slice(FIXED_HEADER_SIZE, headerBytes);
  if (numSymbols <= SYM_EOB || codeLengths[SYM_EOB] === 0) {
    throw new FormatError('码长表缺少块结束符号');
  }
  return {
    originalLength,
    windowLimit,
    payloadBytes,
    crc32: crc,
    numSymbols,
    codeLengths,
    headerBytes,
    totalBytes: headerBytes + payloadBytes,
  };
}

export type DecodeResult = {
  output: Uint8Array;
  done: boolean;
};

// 流式块解码器：任意字节边界分块输入，输出受调用方额度限制。
export class BlockDecoder {
  private buf = new Uint8Array(4096);
  private len = 0;

  private header: BlockInfo | null = null;
  private table: CanonicalDecoder | null = null;
  private window = new Uint8Array(0);
  private bitPos = 0; // 载荷内已消费的位数
  private produced = 0;
  private crc = new Crc32();
  private matchDist = 0;
  private matchLeft = 0;
  private finished = false;

  push(chunk: Uint8Array): void {
    if (this.finished) {
      throw new FormatError('块已结束，拒绝继续输入');
    }
    if (this.len + chunk.length > this.buf.length) {
      let cap = this.buf.length;
      while (cap < this.len + chunk.length) cap *= 2;
      const next = new Uint8Array(cap);
      next.set(this.buf.subarray(0, this.len));
      this.buf = next;
    }
    this.buf.set(chunk, this.len);
    this.len += chunk.length;
  }

  // 单次最多输出 maxOutput 字节。返回 done=true 表示本块完整解码且校验通过。
  decode(maxOutput: number): DecodeResult {
    const out: number[] = [];
    while (out.length < maxOutput && !this.finished) {
      if (this.header === null) {
        if (!this.tryParseHeader()) break;
      }
      if (this.matchLeft > 0) {
        this.copyOne(out);
        continue;
      }
      if (!this.decodeToken(out)) break; // 输入不足，等待更多数据
    }
    return { output: Uint8Array.from(out), done: this.finished };
  }

  // 宣告输入结束。块未完结则抛 FormatError（截断）。
  finish(): void {
    if (!this.finished) {
      throw new FormatError('输入截断: 块未完整解码');
    }
  }

  private tryParseHeader(): boolean {
    if (this.len < FIXED_HEADER_SIZE) return false;
    const view = new DataView(this.buf.buffer);
    const numSymbols = view.getUint16(20, true);
    if (numSymbols < 1 || numSymbols > NUM_SYMBOLS) {
      parseHeader(this.buf.subarray(0, this.len)); // 抛出具体错误
    }
    if (this.len < FIXED_HEADER_SIZE + numSymbols) return false;
    // parseHeader 负责全部合法性校验（含过度订阅码表，经 CanonicalDecoder）。
    const header = parseHeader(this.buf.subarray(0, this.len));
    this.table = new CanonicalDecoder(header.codeLengths);
    this.window = new Uint8Array(header.windowLimit);
    this.header = header;
    return true;
  }

  private readBit(): number | null {
    const header = this.header!;
    if (this.bitPos >= header.payloadBytes * 8) {
      throw new FormatError('载荷耗尽但未遇到块结束符号');
    }
    const byteIdx = header.headerBytes + (this.bitPos >> 3);
    if (byteIdx >= this.len) return null; // 输入不足
    const bit = (this.buf[byteIdx] >> (this.bitPos & 7)) & 1;
    this.bitPos++;
    return bit;
  }

  private readBits(count: number): number | null {
    let value = 0;
    for (let i = 0; i < count; i++) {
      const bit = this.readBit();
      if (bit === null) return null;
      value |= bit << i;
    }
    return value >>> 0;
  }

  // 原子地解码一个令牌；输入不足时回滚位位置并返回 false。
  private decodeToken(out: number[]): boolean {
    const header = this.header!;
    const savedBitPos = this.bitPos;
    const sym = this.table!.decode(() => this.readBit());
    if (sym === null) {
      this.bitPos = savedBitPos;
      return false;
    }
    if (sym < 256) {
      this.emitByte(sym, out);
      return true;
    }
    if (sym === SYM_EOB) {
      this.finalizeBlock();
      return true;
    }
    const c = sym - 257;
    const extra = this.readBits(LEN_EXTRA[c]);
    const dist = this.readBits(16);
    if (extra === null || dist === null) {
      this.bitPos = savedBitPos;
      return false;
    }
    const length = LEN_BASE[c] + extra;
    if (dist < 1) {
      throw new FormatError('非法距离: 0');
    }
    if (dist > header.windowLimit) {
      throw new FormatError(`非法距离: ${dist} 超过窗口上限 ${header.windowLimit}`);
    }
    if (dist > this.produced) {
      throw new FormatError(`非法距离: ${dist} 超过已产生历史 ${this.produced}`);
    }
    this.matchDist = dist;
    this.matchLeft = length;
    return true;
  }

  private emitByte(b: number, out: number[]): void {
    const header = this.header!;
    if (this.produced >= header.originalLength) {
      throw new FormatError('展开超限: 输出超过头部声明的原始长度');
    }
    this.window[this.produced % header.windowLimit] = b;
    this.produced++;
    this.crc.updateByte(b);
    out.push(b);
  }

  private copyOne(out: number[]): void {
    const header = this.header!;
    // 重叠复制语义：逐字节从 distance 之前取值，源随输出同步前进。
    const b = this.window[(this.produced - this.matchDist) % header.windowLimit];
    this.emitByte(b, out);
    this.matchLeft--;
  }

  private finalizeBlock(): void {
    const header = this.header!;
    // 填充位必须为 0。
    let paddingBits = 0;
    while (this.bitPos & 7) {
      const bit = this.readBit();
      if (bit === null) throw new FormatError('输入截断: 缺少填充字节');
      if (bit !== 0) throw new FormatError('块结束后的填充位非零');
      paddingBits++;
    }
    void paddingBits;
    const consumed = this.bitPos >> 3;
    if (consumed !== header.payloadBytes) {
      throw new FormatError(
        `载荷长度不符: 消费 ${consumed} 字节，声明 ${header.payloadBytes} 字节`,
      );
    }
    if (this.produced !== header.originalLength) {
      throw new FormatError(
        `长度不符: 输出 ${this.produced} 字节，声明 ${header.originalLength} 字节`,
      );
    }
    if (this.crc.digest() !== header.crc32) {
      throw new FormatError('CRC-32 校验失败');
    }
    this.finished = true;
  }
}

// 一次性解码完整块（输入必须恰好是完整块，否则抛 FormatError）。
export function decodeBlock(data: Uint8Array): Uint8Array {
  const decoder = new BlockDecoder();
  decoder.push(data);
  const parts: Uint8Array[] = [];
  for (;;) {
    const { output, done } = decoder.decode(1 << 20);
    if (output.length > 0) parts.push(output);
    if (done) break;
    if (output.length === 0) break; // 无进展：输入不足
  }
  decoder.finish();
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}
