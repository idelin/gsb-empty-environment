import type { BlockIterator } from './block.ts';
import type { SSTableReader } from './sstable.ts';

export interface PrefixEntry {
  /** View into an internal key buffer; copy if you need to retain it. */
  key: Buffer;
  /** Zero-copy view into the SSTable file image. */
  value: Buffer;
}

/**
 * Smallest key strictly greater than every key carrying `prefix`
 * (prefix successor). null means unbounded (prefix is all 0xff).
 */
export function prefixUpperBound(prefix: Buffer): Buffer | null {
  const out = Buffer.from(prefix);
  for (let i = out.length - 1; i >= 0; i--) {
    if (out[i] !== 0xff) {
      out[i] += 1;
      return out.subarray(0, i + 1);
    }
  }
  return null;
}

/**
 * Iterator over all entries whose key starts with `prefix`.
 *
 * Positioning: one binary search over the in-memory index block jumps
 * directly to the first data block that may contain the prefix — all
 * preceding blocks are skipped without being read.
 *
 * Termination (boundary truncation): stops at the first key that no
 * longer carries the prefix. Before reading the next data block it
 * consults the current block's index separator, so a scan typically
 * never touches a block belonging to the next tenant.
 */
export class PrefixIterator implements IterableIterator<PrefixEntry> {
  private blockIndex = -2; // -2 = not yet positioned, -1 = exhausted
  private blockIter: BlockIterator | null = null;
  private readonly upperBound: Buffer | null;
  private blocksScannedCount = 0;
  private pendingStep = false; // true when the last returned entry must be stepped over
  private readonly reader: SSTableReader;
  private readonly prefix: Buffer;

  constructor(reader: SSTableReader, prefix: Buffer) {
    this.reader = reader;
    this.prefix = prefix;
    this.upperBound = prefixUpperBound(prefix);
  }

  /** Number of data blocks actually read during this scan. */
  get blocksScanned(): number {
    return this.blocksScannedCount;
  }

  private position(): void {
    const idx = this.reader.locateBlock(this.prefix);
    if (idx < 0) {
      this.blockIndex = -1;
      return;
    }
    this.blockIndex = idx;
    this.loadBlock();
    this.blockIter!.seek(this.prefix);
  }

  private loadBlock(): void {
    this.blockIter = this.reader.readBlockAt(this.blockIndex).iterator();
    this.blocksScannedCount++;
  }

  private outOfRange(key: Buffer): boolean {
    return !key.subarray(0, this.prefix.length).equals(this.prefix);
  }

  /** Entry at the current position, or null when the range is exhausted. */
  private current(): PrefixEntry | null {
    for (;;) {
      if (this.blockIter!.valid()) {
        const key = this.blockIter!.key();
        if (this.outOfRange(key)) return null; // boundary truncation
        return { key, value: this.blockIter!.value() };
      }
      // Current block exhausted. The index separator of the block we just
      // finished is >= every key in it; if it is already >= the prefix
      // successor, no later block can contain the prefix — stop without
      // reading another block.
      const separator = this.reader.indexEntries[this.blockIndex].separator;
      if (this.upperBound !== null && Buffer.compare(separator, this.upperBound) >= 0) {
        return null;
      }
      if (this.blockIndex + 1 >= this.reader.indexEntries.length) return null;
      this.blockIndex++;
      this.loadBlock();
      this.blockIter!.seekToFirst();
    }
  }

  next(): IteratorResult<PrefixEntry> {
    if (this.blockIndex === -2) this.position();
    if (this.blockIndex === -1) return { done: true, value: undefined };
    // Step over the entry returned by the previous next() call. Done here
    // (not before returning) so the returned key/value views stay valid
    // until the consumer calls next() again.
    if (this.pendingStep) {
      this.blockIter!.next();
      this.pendingStep = false;
    }
    const entry = this.current();
    if (entry === null) {
      this.blockIndex = -1;
      return { done: true, value: undefined };
    }
    this.pendingStep = true;
    return { done: false, value: entry };
  }

  [Symbol.iterator](): IterableIterator<PrefixEntry> {
    return this;
  }
}
