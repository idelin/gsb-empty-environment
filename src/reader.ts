// Segment querier. A segment is only opened after full validation;
// seek() jumps via the in-memory skip table and decodes at most one
// posting block per block boundary crossed.

import { readFile } from 'node:fs/promises';
import {
  decodeBlockHeader,
  decodeHeader,
  decodePayloadIds,
  parseDictionary,
  type BlockMeta,
  type TermEntry,
} from './format.ts';
import { validateSegmentBuffer } from './validator.ts';

export interface ReaderStats {
  decodedBlocks: number;
}

export class SegmentReader {
  readonly path: string;
  readonly stats: ReaderStats = { decodedBlocks: 0 };
  private readonly buf: Buffer;
  private readonly dict: Map<string, TermEntry>;

  private constructor(path: string, buf: Buffer) {
    this.path = path;
    this.buf = buf;
    const header = decodeHeader(buf);
    const entries = parseDictionary(buf, header.dictOffset, header.dictLength, header.termCount);
    this.dict = new Map(entries.map((e) => [e.term, e]));
  }

  // Opens a segment only if it passes validation; throws otherwise.
  static async open(path: string): Promise<SegmentReader> {
    const buf = await readFile(path);
    const report = validateSegmentBuffer(buf);
    if (!report.ok) {
      const brief = report.issues
        .slice(0, 3)
        .map((i) => `${i.code}@[${i.byteStart},${i.byteEnd})${i.term !== undefined ? ` term=${JSON.stringify(i.term)}` : ''}`)
        .join('; ');
      throw new Error(`refusing to query invalid segment: ${report.issues.length} issue(s): ${brief}`);
    }
    return new SegmentReader(path, buf);
  }

  terms(): string[] {
    return [...this.dict.keys()];
  }

  has(term: string): boolean {
    return this.dict.has(term);
  }

  resetStats(): void {
    this.stats.decodedBlocks = 0;
  }

  // Decodes one posting block; counted in stats. Used by Cursor.
  decodeBlockInternal(meta: BlockMeta): number[] {
    this.stats.decodedBlocks++;
    const end = meta.offset + meta.length;
    const bh = decodeBlockHeader(this.buf, meta.offset, end);
    return decodePayloadIds(this.buf, bh.payloadOffset, end, bh.count, bh.firstDocId, bh.lastDocId);
  }

  // Full posting list of a term (decodes every block of the term).
  get(term: string): number[] {
    const entry = this.dict.get(term);
    if (!entry) return [];
    const out: number[] = [];
    for (const meta of entry.blocks) out.push(...this.decodeBlockInternal(meta));
    return out;
  }

  cursor(term: string): Cursor {
    const entry = this.dict.get(term);
    if (!entry) throw new Error(`unknown term ${JSON.stringify(term)}`);
    return new Cursor(this, entry);
  }

  // AND intersection of posting lists via merge + skip-table seek.
  intersect(terms: string[]): number[] {
    if (terms.length === 0) return [];
    const cursors: Cursor[] = [];
    for (const term of terms) {
      const entry = this.dict.get(term);
      if (!entry || entry.blocks.length === 0) return [];
      cursors.push(new Cursor(this, entry));
    }
    for (const c of cursors) c.next();
    const out: number[] = [];
    for (;;) {
      if (cursors.some((c) => c.done)) break;
      let max = cursors[0].value;
      for (const c of cursors) if (c.value > max) max = c.value;
      for (const c of cursors) c.seek(max);
      if (cursors.some((c) => c.done)) break;
      let allEqual = true;
      for (const c of cursors) {
        if (c.value !== max) {
          allEqual = false;
          break;
        }
      }
      if (allEqual) {
        out.push(max);
        for (const c of cursors) c.next();
      }
    }
    return out;
  }
}

// First index in [from, arr.length) whose key >= target.
function lowerBound<T>(arr: T[], target: number, from: number, key: (x: T) => number): number {
  let lo = from;
  let hi = arr.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (key(arr[mid]) < target) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export class Cursor {
  value = 0;
  done = false;
  private blockIndex = -1;
  private ids: number[] = [];
  private pos = 0;
  private readonly reader: SegmentReader;
  private readonly entry: TermEntry;

  constructor(reader: SegmentReader, entry: TermEntry) {
    this.reader = reader;
    this.entry = entry;
  }

  private loadBlock(index: number): boolean {
    if (index >= this.entry.blocks.length) {
      this.done = true;
      return false;
    }
    if (index !== this.blockIndex) {
      this.ids = this.reader.decodeBlockInternal(this.entry.blocks[index]);
      this.blockIndex = index;
      this.pos = 0;
    }
    return true;
  }

  next(): void {
    if (this.done) return;
    if (this.blockIndex === -1) {
      if (!this.loadBlock(0)) return;
    } else {
      this.pos++;
      if (this.pos >= this.ids.length && !this.loadBlock(this.blockIndex + 1)) return;
    }
    this.value = this.ids[this.pos];
  }

  // Advances to the first id >= target. Never moves backwards and never
  // decodes blocks whose skip-table lastDocId is below target.
  seek(target: number): void {
    if (this.done) return;
    if (this.entry.blocks.length === 0) {
      this.done = true;
      return;
    }
    if (this.blockIndex !== -1 && this.value >= target) return;
    let index = this.blockIndex === -1 ? 0 : this.blockIndex;
    if (this.entry.blocks[index].lastDocId < target) {
      index = lowerBound(this.entry.blocks, target, index + 1, (m: BlockMeta) => m.lastDocId);
      if (index >= this.entry.blocks.length) {
        this.done = true;
        return;
      }
    }
    if (!this.loadBlock(index)) return;
    const pos = lowerBound(this.ids, target, this.pos, (x: number) => x);
    if (pos >= this.ids.length) {
      // target lies beyond this block; move to the next block start.
      if (!this.loadBlock(index + 1)) return;
      this.value = this.ids[0];
      return;
    }
    this.pos = pos;
    this.value = this.ids[pos];
  }
}
