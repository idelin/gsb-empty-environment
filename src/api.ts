// Whole-buffer convenience API built on the block codec and StreamDecoder.

import { concatBytes } from './format.ts';
import { encodeBlock } from './block.ts';
import { StreamDecoder } from './decoder.ts';

export interface CompressOptions {
  windowBits?: number; // log2 of the LZ77 window, 4..20 (default 16)
  blockSize?: number; // uncompressed bytes per block (default 1 MiB)
}

export interface DecompressOptions {
  maxBlockSize?: number; // declared-length limit per block (default 64 MiB)
}

export function compress(data: Uint8Array, opts: CompressOptions = {}): Uint8Array {
  const windowBits = opts.windowBits ?? 16;
  const blockSize = opts.blockSize ?? 1 << 20;
  if (blockSize <= 0) throw new RangeError('blockSize must be positive');
  if (data.length === 0) {
    // Emit one explicit empty block so the stream round-trips the edge case.
    return encodeBlock(data, windowBits);
  }
  const blocks: Uint8Array[] = [];
  for (let off = 0; off < data.length; off += blockSize) {
    blocks.push(encodeBlock(data.subarray(off, Math.min(off + blockSize, data.length)), windowBits));
  }
  return concatBytes(blocks);
}

export function decompress(data: Uint8Array, opts: DecompressOptions = {}): Uint8Array {
  const dec = new StreamDecoder(opts);
  dec.push(data);
  dec.finish();
  const parts: Uint8Array[] = [];
  for (;;) {
    const chunk = dec.read(1 << 20);
    if (chunk.length === 0) break;
    parts.push(chunk);
  }
  return concatBytes(parts);
}
