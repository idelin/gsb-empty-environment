import { BlockReader } from './block.ts';
import type { SstableReader } from './sstable.ts';

/**
 * Minimal ordered-iterator contract shared by SSTable iterators, the
 * memtable iterator and the merging iterator.
 *
 * `key()` / `value()` return zero-copy `Buffer` views; callers that retain
 * them beyond the next iterator movement must copy.
 */
export interface RawIterator {
  seek(target: Buffer): void;
  valid(): boolean;
  key(): Buffer;
  value(): Buffer;
  next(): void;
}

/**
 * Two-level iterator over one SSTable: positions itself via the in-memory
 * index block, then walks data blocks sequentially. Only the blocks that
 * actually overlap the scanned range are ever read from disk.
 */
export class SstableIterator implements RawIterator {
  private indexPos = 0;
  private block: BlockReader | null = null;
  private entryPos = 0;
  private readonly reader: SstableReader;

  constructor(reader: SstableReader) {
    this.reader = reader;
  }

  seek(target: Buffer): void {
    this.indexPos = this.reader.findBlock(target);
    if (this.indexPos >= this.reader.numBlocks) {
      this.block = null;
      return;
    }
    this.loadBlock();
    this.entryPos = this.block!.seek(target);
    if (this.entryPos >= this.block!.size) this.advanceBlock();
  }

  seekToFirst(): void {
    this.indexPos = 0;
    this.block = null;
    if (this.reader.numBlocks > 0) {
      this.loadBlock();
      this.entryPos = 0;
    }
  }

  private loadBlock(): void {
    this.block = new BlockReader(this.reader.readBlock(this.reader.blockHandleAt(this.indexPos)));
  }

  private advanceBlock(): void {
    this.indexPos++;
    if (this.indexPos >= this.reader.numBlocks) {
      this.block = null;
      return;
    }
    this.loadBlock();
    this.entryPos = 0;
  }

  next(): void {
    if (this.block === null) return;
    this.entryPos++;
    if (this.entryPos >= this.block.size) this.advanceBlock();
  }

  valid(): boolean {
    return this.block !== null;
  }

  key(): Buffer {
    return this.block!.keyAt(this.entryPos);
  }

  value(): Buffer {
    return this.block!.valueAt(this.entryPos);
  }
}

function startsWith(key: Buffer, prefix: Buffer): boolean {
  if (key.length < prefix.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (key[i] !== prefix[i]) return false;
  }
  return true;
}

/**
 * Prefix range-scan locator. Seeks the underlying iterator to the first key
 * >= prefix (landing directly on the first matching data block via the
 * index) and applies boundary truncation: the moment the sequential scan
 * produces a key outside the prefix (e.g. the next tenant), `valid()` turns
 * false and the scan ends -- no further block is touched.
 */
export class PrefixIterator implements RawIterator {
  private readonly inner: RawIterator;
  private readonly prefix: Buffer;

  constructor(inner: RawIterator, prefix: Buffer) {
    this.inner = inner;
    this.prefix = prefix;
    this.inner.seek(prefix);
  }

  valid(): boolean {
    return this.inner.valid() && startsWith(this.inner.key(), this.prefix);
  }

  next(): void {
    this.inner.next();
  }

  key(): Buffer {
    return this.inner.key();
  }

  value(): Buffer {
    return this.inner.value();
  }
}

/** In-memory sorted iterator used for the LSM memtable. */
export class MemTableIterator implements RawIterator {
  private readonly keys: Buffer[];
  private pos = 0;
  private readonly mem: ReadonlyMap<string, Buffer>;

  constructor(mem: ReadonlyMap<string, Buffer>) {
    this.mem = mem;
    this.keys = [...mem.keys()].sort().map((k) => Buffer.from(k));
  }

  seek(target: Buffer): void {
    let lo = 0;
    let hi = this.keys.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (Buffer.compare(this.keys[mid], target) < 0) lo = mid + 1;
      else hi = mid;
    }
    this.pos = lo;
  }

  valid(): boolean {
    return this.pos < this.keys.length;
  }

  key(): Buffer {
    return this.keys[this.pos];
  }

  value(): Buffer {
    return this.mem.get(this.keys[this.pos].toString())!;
  }

  next(): void {
    this.pos++;
  }
}

/**
 * K-way merge over ordered sources. Sources are listed from newest to
 * oldest; on duplicate keys the newest source wins and the stale versions
 * are suppressed.
 */
export class MergeIterator implements RawIterator {
  private current = -1;
  private readonly sources: RawIterator[];

  constructor(sources: RawIterator[]) {
    this.sources = sources;
  }

  seek(target: Buffer): void {
    for (const source of this.sources) source.seek(target);
    this.recompute();
  }

  private recompute(): void {
    this.current = -1;
    for (let i = 0; i < this.sources.length; i++) {
      const source = this.sources[i];
      if (!source.valid()) continue;
      if (this.current === -1 || Buffer.compare(source.key(), this.sources[this.current].key()) < 0) {
        this.current = i;
      }
    }
  }

  valid(): boolean {
    return this.current !== -1;
  }

  key(): Buffer {
    return this.sources[this.current].key();
  }

  value(): Buffer {
    return this.sources[this.current].value();
  }

  next(): void {
    const key = Buffer.from(this.key());
    for (const source of this.sources) {
      if (source.valid() && source.key().equals(key)) source.next();
    }
    this.recompute();
  }
}
