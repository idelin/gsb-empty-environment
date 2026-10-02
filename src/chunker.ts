/**
 * 基于 Buzhash 滚动哈希的内容定义分块器（CDC）。
 *
 * 当滑动窗口哈希值的低 maskBits 位全为 1 时判定分块边界，
 * 期望分块大小 = 2^maskBits 字节。边界只取决于局部内容，
 * 因此在文件中间插入/删除字节只会影响附近的少数分块。
 */
import { createHash } from "node:crypto";
import { Buzhash } from "./buzhash.ts";

export interface ChunkerOptions {
  minSize?: number;   // 最小分块，避免过碎
  maxSize?: number;   // 最大分块，强制切分
  maskBits?: number;  // 目标大小 = 2^maskBits
  windowSize?: number;
}

export interface Chunk {
  index: number;
  offset: number;  // 在原始数据中的起始偏移
  length: number;
  hash: string;    // 内容 SHA-256（hex）
  data: Buffer;
}

export const DEFAULTS = {
  minSize: 2048,
  maxSize: 65536,
  maskBits: 13, // 目标平均 8192 字节
  windowSize: 48,
} as const;

export function hashChunk(data: Buffer | Uint8Array): string {
  return createHash("sha256").update(data).digest("hex");
}

/** 流式分块：输入完整 buffer，输出分块数组（内部按字节滚动，可改为增量喂数据）。 */
export function chunk(data: Buffer, opts: ChunkerOptions = {}): Chunk[] {
  const minSize = opts.minSize ?? DEFAULTS.minSize;
  const maxSize = opts.maxSize ?? DEFAULTS.maxSize;
  const maskBits = opts.maskBits ?? DEFAULTS.maskBits;
  const windowSize = opts.windowSize ?? DEFAULTS.windowSize;
  const mask = (1 << maskBits) - 1;

  const bh = new Buzhash(windowSize);
  const chunks: Chunk[] = [];
  let start = 0;

  for (let i = 0; i < data.length; i++) {
    const h = bh.update(data[i]);
    const len = i - start + 1;
    if (len < minSize) continue;
    const boundary = (h & mask) === mask;
    if (boundary || len >= maxSize) {
      const slice = data.subarray(start, i + 1);
      chunks.push({
        index: chunks.length,
        offset: start,
        length: slice.length,
        hash: hashChunk(slice),
        data: Buffer.from(slice),
      });
      start = i + 1;
      bh.reset();
    }
  }
  if (start < data.length || chunks.length === 0) {
    const slice = data.subarray(start);
    chunks.push({
      index: chunks.length,
      offset: start,
      length: slice.length,
      hash: hashChunk(slice),
      data: Buffer.from(slice),
    });
  }
  return chunks;
}
