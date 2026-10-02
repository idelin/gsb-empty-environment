import { createHash } from 'node:crypto';
import { Buzhash } from './buzhash.ts';

export interface ChunkerOptions {
  /** Minimum chunk size in bytes (no boundary check before this). */
  minSize?: number;
  /** Target average chunk size in bytes (must be a power of two). */
  avgSize?: number;
  /** Maximum chunk size in bytes (forced cut). */
  maxSize?: number;
  /** Buzhash sliding window size in bytes. */
  windowSize?: number;
}

export interface Chunk {
  /** Sequential chunk index within the stream. */
  index: number;
  /** Byte offset of the chunk start in the source data. */
  offset: number;
  /** Chunk length in bytes. */
  length: number;
  /** SHA-256 hex digest of the chunk content (Merkle leaf). */
  hash: string;
  /** Raw chunk bytes. */
  data: Buffer;
}

export const DEFAULTS = {
  minSize: 2048,
  avgSize: 8192,
  maxSize: 65536,
  windowSize: 64,
} as const;

export function sha256Hex(data: Uint8Array): string {
  return createHash('sha256').update(data).digest('hex');
}

/**
 * Content-defined chunker: cut points depend only on local byte content
 * (via the Buzhash rolling hash), never on absolute position. A boundary is
 * declared when the low `log2(avgSize)` bits of the hash are all set, which
 * yields an average chunk size of `avgSize` bytes.
 */
export function chunk(data: Buffer, options: ChunkerOptions = {}): Chunk[] {
  const minSize = options.minSize ?? DEFAULTS.minSize;
  const avgSize = options.avgSize ?? DEFAULTS.avgSize;
  const maxSize = options.maxSize ?? DEFAULTS.maxSize;
  const windowSize = options.windowSize ?? DEFAULTS.windowSize;
  if ((avgSize & (avgSize - 1)) !== 0) {
    throw new Error('avgSize must be a power of two');
  }
  const mask = avgSize - 1;

  const chunks: Chunk[] = [];
  const hasher = new Buzhash(windowSize);
  let start = 0;
  let index = 0;

  const emit = (end: number): void => {
    const slice = data.subarray(start, end);
    chunks.push({
      index: index++,
      offset: start,
      length: end - start,
      hash: sha256Hex(slice),
      data: Buffer.from(slice),
    });
    start = end;
    hasher.reset();
  };

  for (let i = 0; i < data.length; i++) {
    const h = hasher.update(data[i]);
    const len = i + 1 - start;
    if (len < minSize) continue;
    if (len >= maxSize || (h & mask) === mask) {
      emit(i + 1);
    }
  }
  if (start < data.length) emit(data.length);
  return chunks;
}
