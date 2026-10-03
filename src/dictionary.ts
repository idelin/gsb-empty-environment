/**
 * Prefix-compressed (front-coded) sorted term dictionary.
 *
 * Terms are stored in UTF-8 byte order, grouped into fixed-size blocks.
 * The first term of each block is stored in full; every following term is
 * stored as (sharedPrefixLength, suffix). A block-offset table at the head
 * of the dictionary allows binary search over block "floor" terms, then a
 * short linear scan inside one block — the classic prefix-dictionary
 * alternative to a full FST.
 */
import {
  ByteReader,
  ByteWriter,
  compareBytes,
  sharedPrefixLength,
} from './bytes.ts';

export const DICT_BLOCK_SIZE = 8;

/** Pointer to one term's posting list inside the postings region of a segment. */
export interface TermPointer {
  /** Byte offset of the posting list, relative to the start of the postings region. */
  offset: number;
  /** Byte length of the encoded posting list. */
  length: number;
  /** Number of postings (documents) in the list. */
  docCount: number;
}

export interface TermEntry {
  term: string;
  pointer: TermPointer;
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/**
 * Serializes term entries (must already be sorted by UTF-8 byte order) into
 * the dictionary block. Posting-list offsets are delta-encoded because they
 * are monotonically increasing.
 */
export function encodeDictionary(entries: TermEntry[]): Uint8Array {
  const blockData = new ByteWriter();
  const blockOffsets: number[] = [];

  let previousOffset = 0;
  let previousTerm = new Uint8Array(0);

  for (let i = 0; i < entries.length; i++) {
    const termBytes = encoder.encode(entries[i].term);
    const { offset, length, docCount } = entries[i].pointer;

    if (i % DICT_BLOCK_SIZE === 0) {
      blockOffsets.push(blockData.length);
      blockData.writeVarint(termBytes.length);
      blockData.writeBytes(termBytes);
      // Each block is decoded independently, so the first entry of a block
      // stores its absolute postings offset; later entries store deltas.
      previousOffset = 0;
    } else {
      const prefix = sharedPrefixLength(previousTerm, termBytes);
      blockData.writeVarint(prefix);
      blockData.writeVarint(termBytes.length - prefix);
      blockData.writeBytes(termBytes.subarray(prefix));
    }
    blockData.writeVarint(offset - previousOffset);
    blockData.writeVarint(length);
    blockData.writeVarint(docCount);

    previousOffset = offset;
    previousTerm = termBytes;
  }

  const out = new ByteWriter();
  out.writeVarint(entries.length);
  out.writeVarint(blockOffsets.length);
  let previousBlockOffset = 0;
  for (const blockOffset of blockOffsets) {
    out.writeVarint(blockOffset - previousBlockOffset);
    previousBlockOffset = blockOffset;
  }
  out.writeBytes(blockData.toUint8Array());
  return out.toUint8Array();
}

/** Read-only view over an encoded dictionary block inside a segment buffer. */
export class DictionaryReader {
  readonly termCount: number;
  private readonly data: Uint8Array;
  private readonly blockOffsets: number[] = [];
  private readonly dataStart: number;

  constructor(data: Uint8Array, offset: number) {
    this.data = data;
    const reader = new ByteReader(data, offset);
    this.termCount = reader.readVarint();
    const blockCount = reader.readVarint();
    let blockOffset = 0;
    for (let i = 0; i < blockCount; i++) {
      blockOffset += reader.readVarint();
      this.blockOffsets.push(blockOffset);
    }
    this.dataStart = reader.offset;
  }

  /** Returns the posting-list pointer for `term`, or null when absent. */
  lookup(term: string): TermPointer | null {
    if (this.blockOffsets.length === 0) return null;
    const target = encoder.encode(term);

    // Binary search: last block whose first term is <= target.
    let lo = 0;
    let hi = this.blockOffsets.length - 1;
    let block = 0;
    while (lo <= hi) {
      const mid = (lo + hi) >>> 1;
      const firstTerm = this.readBlockFirstTerm(mid);
      if (compareBytes(firstTerm, target) <= 0) {
        block = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }

    // Linear scan inside the candidate block.
    const reader = new ByteReader(this.data, this.dataStart + this.blockOffsets[block]);
    let previousTerm = new Uint8Array(0);
    let previousOffset = 0;
    const termsInBlock = Math.min(
      DICT_BLOCK_SIZE,
      this.termCount - block * DICT_BLOCK_SIZE,
    );
    for (let i = 0; i < termsInBlock; i++) {
      let termBytes: Uint8Array;
      if (i === 0) {
        termBytes = reader.readBytes(reader.readVarint());
      } else {
        const prefix = reader.readVarint();
        const suffix = reader.readBytes(reader.readVarint());
        termBytes = new Uint8Array(prefix + suffix.length);
        termBytes.set(previousTerm.subarray(0, prefix), 0);
        termBytes.set(suffix, prefix);
      }
      const offset = previousOffset + reader.readVarint();
      const length = reader.readVarint();
      const docCount = reader.readVarint();

      const cmp = compareBytes(termBytes, target);
      if (cmp === 0) return { offset, length, docCount };
      if (cmp > 0) return null; // sorted: no match possible beyond this point
      previousTerm = termBytes;
      previousOffset = offset;
    }
    return null;
  }

  /** Iterates every term in byte order (used by tests and tooling). */
  *terms(): Generator<string> {
    const reader = new ByteReader(this.data, this.dataStart);
    let previousTerm = new Uint8Array(0);
    for (let i = 0; i < this.termCount; i++) {
      let termBytes: Uint8Array;
      if (i % DICT_BLOCK_SIZE === 0) {
        termBytes = reader.readBytes(reader.readVarint());
      } else {
        const prefix = reader.readVarint();
        const suffix = reader.readBytes(reader.readVarint());
        termBytes = new Uint8Array(prefix + suffix.length);
        termBytes.set(previousTerm.subarray(0, prefix), 0);
        termBytes.set(suffix, prefix);
      }
      reader.readVarint(); // offset delta
      reader.readVarint(); // length
      reader.readVarint(); // docCount
      previousTerm = termBytes;
      yield decoder.decode(termBytes);
    }
  }

  private readBlockFirstTerm(block: number): Uint8Array {
    const reader = new ByteReader(this.data, this.dataStart + this.blockOffsets[block]);
    return reader.readBytes(reader.readVarint());
  }
}
