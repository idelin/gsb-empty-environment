/**
 * Read-only term dictionary using front coding (prefix compression).
 *
 * Terms are sorted, split into fixed-size blocks. Inside a block the first
 * term is stored in full and every following term as (sharedPrefixLen, suffix).
 * A sparse block index (first term of each block) allows binary search to
 * locate the candidate block, which is then scanned linearly.
 *
 * Layout:
 *   termCount   varint
 *   blockSize   varint
 *   blockCount  varint
 *   -- block index (blockCount entries) --
 *     firstTermLen varint, firstTerm bytes, blockOffset u32 (rel. to blocks region)
 *   -- blocks region --
 *     per block:
 *       entryCount varint
 *       entry 0: termLen varint, term bytes, postingsOffset u32, postingsLength u32, docFreq varint
 *       entry n: prefixLen varint, suffixLen varint, suffix bytes,
 *                postingsOffset u32, postingsLength u32, docFreq varint
 */

import { ByteReader, ByteWriter, decodeUtf8, encodeUtf8 } from "./bytes.ts";

export interface DictEntry {
  term: string;
  postingsOffset: number;
  postingsLength: number;
  docFreq: number;
}

export const DEFAULT_BLOCK_SIZE = 8;

function sharedPrefixLen(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  let i = 0;
  while (i < n && a[i] === b[i]) i++;
  return i;
}

/** Entries must be sorted by term (lexicographic, UTF-8 byte order). */
export function buildDictionary(
  entries: DictEntry[],
  blockSize = DEFAULT_BLOCK_SIZE,
): Uint8Array {
  const writer = new ByteWriter();
  const blockCount = Math.ceil(entries.length / blockSize);
  writer.writeVarint(entries.length);
  writer.writeVarint(blockSize);
  writer.writeVarint(blockCount);

  // Encode blocks first so we know their offsets, then emit index + blocks.
  const blocks = new ByteWriter();
  const index: { firstTerm: Uint8Array; offset: number }[] = [];

  for (let b = 0; b < blockCount; b++) {
    const start = b * blockSize;
    const end = Math.min(start + blockSize, entries.length);
    index.push({ firstTerm: encodeUtf8(entries[start].term), offset: blocks.length });
    blocks.writeVarint(end - start);
    let prev = new Uint8Array(0);
    for (let i = start; i < end; i++) {
      const termBytes = encodeUtf8(entries[i].term);
      if (i === start) {
        blocks.writeVarint(termBytes.length);
        blocks.writeBytes(termBytes);
      } else {
        const prefix = sharedPrefixLen(prev, termBytes);
        blocks.writeVarint(prefix);
        blocks.writeVarint(termBytes.length - prefix);
        blocks.writeBytes(termBytes.subarray(prefix));
      }
      blocks.writeU32(entries[i].postingsOffset);
      blocks.writeU32(entries[i].postingsLength);
      blocks.writeVarint(entries[i].docFreq);
      prev = termBytes;
    }
  }

  for (const { firstTerm, offset } of index) {
    writer.writeVarint(firstTerm.length);
    writer.writeBytes(firstTerm);
    writer.writeU32(offset);
  }
  writer.writeBytes(blocks.toUint8Array());
  return writer.toUint8Array();
}

export class DictionaryReader {
  readonly termCount: number;
  private readonly blockSize: number;
  private readonly indexTerms: string[] = [];
  private readonly indexOffsets: number[] = [];
  private readonly buf: Uint8Array;
  private readonly blocksBase: number;

  constructor(buf: Uint8Array, offset = 0) {
    this.buf = buf;
    const reader = new ByteReader(buf, offset);
    this.termCount = reader.readVarint();
    this.blockSize = reader.readVarint();
    const blockCount = reader.readVarint();
    for (let i = 0; i < blockCount; i++) {
      const len = reader.readVarint();
      this.indexTerms.push(decodeUtf8(reader.readBytes(len)));
      this.indexOffsets.push(reader.readU32());
    }
    this.blocksBase = reader.offset;
  }

  lookup(term: string): DictEntry | null {
    // Binary search: last index entry whose firstTerm <= term.
    let lo = 0;
    let hi = this.indexTerms.length - 1;
    let block = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >>> 1;
      if (this.indexTerms[mid] <= term) {
        block = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    if (block < 0) return null;

    const reader = new ByteReader(this.buf, this.blocksBase + this.indexOffsets[block]);
    const count = reader.readVarint();
    let currentBytes: Uint8Array = new Uint8Array(0);
    for (let i = 0; i < count; i++) {
      if (i === 0) {
        currentBytes = reader.readBytes(reader.readVarint());
      } else {
        const prefix = reader.readVarint();
        const suffixLen = reader.readVarint();
        const suffix = reader.readBytes(suffixLen);
        const joined = new Uint8Array(prefix + suffixLen);
        joined.set(currentBytes.subarray(0, prefix));
        joined.set(suffix, prefix);
        currentBytes = joined;
      }
      const current = decodeUtf8(currentBytes);
      const postingsOffset = reader.readU32();
      const postingsLength = reader.readU32();
      const docFreq = reader.readVarint();
      if (current === term) return { term, postingsOffset, postingsLength, docFreq };
      if (current > term) return null; // sorted: no match possible beyond this point
    }
    return null;
  }
}
