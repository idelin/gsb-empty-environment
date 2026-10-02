import { BitReader, BitWriter } from './bitstream.ts';
import {
  buildDecodeTree,
  decodeSymbol,
  deserializeCodebook,
  serializeCodebook,
  type HuffmanCode,
} from './huffman.ts';

/**
 * 容错帧格式（面向不可靠信道的霍夫曼分段流）：
 *
 *   [文件头]（可选）
 *     magic       u32  'HFR1'
 *     totalSyms   u32  符号总数（仅供参考）
 *     codebook    变长 码表序列化
 *   [分段] × N，每段固定 100 字节：
 *     sync        u32  0x1ACFFC1D 同步字（自同步标记）
 *     symCount    u16  本段符号数
 *     payloadBits u32  本段载荷精确比特数
 *     checksum    u16  符号字节和（mod 2^16）
 *     payload     变长 霍夫曼编码比特，末尾 0 填充到字节边界
 *
 * 解码器逐段验证（非法码字 / payloadBits / checksum 三重校验）；
 * 任何一段验证失败即被整体隔离，随后逐比特扫描下一个同步字重新锁定相位，
 * 错误不会扩散到后续分段。
 */
export const SYNC_WORD = 0x1acffc1d;
export const FILE_MAGIC = 0x48465231; // 'HFR1'
export const PLAIN_MAGIC = 0x48465031; // 'HFP1'
export const SEGMENT_SIZE_BYTES = 100;
export const SEGMENT_HEADER_BYTES = 4 + 2 + 4 + 2; // 12
export const SEGMENT_PAYLOAD_CAPACITY_BITS = (SEGMENT_SIZE_BYTES - SEGMENT_HEADER_BYTES) * 8; // 704

function checksum(symbols: number[]): number {
  let sum = 0;
  for (const s of symbols) sum = (sum + s) & 0xffff;
  return sum;
}

export interface EncodeOptions {
  /** 是否写入文件头（码表）。false 表示通信双方预先约定码表（如 DEFLATE 固定码表）。 */
  withHeader?: boolean;
}

export interface ResilientEncodeResult {
  data: Uint8Array;
  segmentCount: number;
  segmentSymbolCounts: number[];
}

export function encodeResilient(
  symbols: number[],
  codebook: Map<number, HuffmanCode>,
  options: EncodeOptions = {},
): ResilientEncodeResult {
  const { withHeader = true } = options;
  const writer = new BitWriter();
  if (withHeader) {
    writer.writeBits(FILE_MAGIC, 32);
    writer.writeBits(symbols.length, 32);
    serializeCodebook(writer, codebook);
    writer.alignToByte();
  }

  const segmentSymbolCounts: number[] = [];
  let segmentSymbols: number[] = [];
  let segmentBits = 0;

  const flush = (): void => {
    if (segmentSymbols.length === 0) return;
    writer.writeBits(SYNC_WORD, 32);
    writer.writeBits(segmentSymbols.length, 16);
    writer.writeBits(segmentBits, 32);
    writer.writeBits(checksum(segmentSymbols), 16);
    for (const s of segmentSymbols) {
      const c = codebook.get(s) as HuffmanCode;
      writer.writeBits(c.code, c.length);
    }
    writer.alignToByte();
    segmentSymbolCounts.push(segmentSymbols.length);
    segmentSymbols = [];
    segmentBits = 0;
  };

  for (const s of symbols) {
    const c = codebook.get(s);
    if (!c) throw new Error(`symbol ${s} missing from codebook`);
    if (c.length > SEGMENT_PAYLOAD_CAPACITY_BITS) {
      throw new Error('huffman code too long for segment');
    }
    if (segmentBits + c.length > SEGMENT_PAYLOAD_CAPACITY_BITS) flush();
    segmentSymbols.push(s);
    segmentBits += c.length;
  }
  flush();

  return {
    data: writer.toUint8Array(),
    segmentCount: segmentSymbolCounts.length,
    segmentSymbolCounts,
  };
}

export interface ResyncEvent {
  /** 重新锁定同步字时，同步字在受损流中的比特位置。 */
  bitPos: number;
}

export interface ResilientDecodeResult {
  symbols: number[];
  segmentsOk: number;
  segmentsLost: number;
  resyncs: ResyncEvent[];
}

/** 在剩余比特流中逐比特扫描同步字，返回其比特位置；找不到返回 -1。 */
function scanForSync(reader: BitReader): number {
  while (reader.remaining >= 32) {
    if (reader.peekBits(32) === SYNC_WORD) return reader.bitPos;
    reader.seek(reader.bitPos + 1);
  }
  return -1;
}

export function decodeResilient(
  data: Uint8Array,
  knownCodebook?: Map<number, HuffmanCode>,
): ResilientDecodeResult {
  const reader = new BitReader(data);
  let codebook = knownCodebook;
  if (reader.remaining >= 32 && reader.peekBits(32) === FILE_MAGIC) {
    reader.readBits(32);
    reader.readBits(32); // totalSyms，仅供参考
    codebook = deserializeCodebook(reader);
    reader.seek(Math.ceil(reader.bitPos / 8) * 8);
  }
  if (!codebook) {
    throw new Error('missing codebook: stream has no header and no codebook was provided');
  }
  const tree = buildDecodeTree(codebook);

  const symbols: number[] = [];
  const resyncs: ResyncEvent[] = [];
  let segmentsOk = 0;
  let segmentsLost = 0;

  while (reader.remaining >= 32) {
    if (reader.peekBits(32) !== SYNC_WORD) {
      // 相位丢失：逐比特扫描下一个同步字
      const found = scanForSync(reader);
      if (found < 0) break;
      resyncs.push({ bitPos: found });
    }
    const syncPos = reader.bitPos;
    reader.readBits(32); // 消费同步字
    if (reader.remaining < 64) {
      segmentsLost += 1;
      break;
    }
    const symCount = reader.readBits(16);
    const payloadBits = reader.readBits(32);
    const expectedChecksum = reader.readBits(16);
    const headerSane =
      symCount > 0 &&
      payloadBits > 0 &&
      payloadBits <= SEGMENT_PAYLOAD_CAPACITY_BITS &&
      symCount <= payloadBits;
    if (!headerSane || reader.remaining < payloadBits) {
      segmentsLost += 1;
      reader.seek(syncPos + 1); // 从假同步字后继续扫描
      continue;
    }
    const payloadStart = reader.bitPos;
    const segmentSymbols: number[] = [];
    let ok = true;
    try {
      for (let i = 0; i < symCount; i += 1) {
        segmentSymbols.push(decodeSymbol(reader, tree));
      }
    } catch {
      ok = false; // 非法码字：流已损坏
    }
    if (ok && reader.bitPos - payloadStart !== payloadBits) ok = false;
    if (ok && checksum(segmentSymbols) !== expectedChecksum) ok = false;
    if (ok) {
      symbols.push(...segmentSymbols);
      segmentsOk += 1;
      // 跳过段尾填充比特，前进到下一段起点（相对段首字节对齐）
      reader.seek(syncPos + Math.ceil((reader.bitPos - syncPos) / 8) * 8);
    } else {
      // 损坏段整体隔离，从下一比特重新扫描同步字
      segmentsLost += 1;
      reader.seek(syncPos + 1);
    }
  }

  return { symbols, segmentsOk, segmentsLost, resyncs };
}

/** 普通（无容错）编码：文件头 + 连续霍夫曼比特流，无任何同步机制。 */
export function encodePlain(
  symbols: number[],
  codebook: Map<number, HuffmanCode>,
  options: EncodeOptions = {},
): { data: Uint8Array } {
  const { withHeader = true } = options;
  const writer = new BitWriter();
  if (withHeader) {
    writer.writeBits(PLAIN_MAGIC, 32);
    writer.writeBits(symbols.length, 32);
    serializeCodebook(writer, codebook);
    writer.alignToByte();
  }
  for (const s of symbols) {
    const c = codebook.get(s);
    if (!c) throw new Error(`symbol ${s} missing from codebook`);
    writer.writeBits(c.code, c.length);
  }
  return { data: writer.toUint8Array() };
}

/** 普通（无容错）解码：顺序解码，损坏后只能产出乱码直至中断。 */
export function decodePlain(data: Uint8Array, knownCodebook?: Map<number, HuffmanCode>): number[] {
  const reader = new BitReader(data);
  let codebook = knownCodebook;
  let total = Number.POSITIVE_INFINITY;
  if (reader.remaining >= 32 && reader.peekBits(32) === PLAIN_MAGIC) {
    reader.readBits(32);
    total = reader.readBits(32);
    codebook = deserializeCodebook(reader);
    reader.seek(Math.ceil(reader.bitPos / 8) * 8);
  }
  if (!codebook) {
    throw new Error('missing codebook: stream has no header and no codebook was provided');
  }
  const tree = buildDecodeTree(codebook);
  const out: number[] = [];
  try {
    while (out.length < total && reader.remaining > 0) {
      out.push(decodeSymbol(reader, tree));
    }
  } catch {
    // 信道损坏：普通解码器在此彻底失效
  }
  return out;
}
