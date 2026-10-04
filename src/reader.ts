// Query reader. A segment is only served after the standalone validator
// accepts it; corrupt or truncated files are rejected at open time.

import { readFileSync } from "node:fs";
import { BLOCK_HEADER_SIZE, decodeVarint } from "./format.ts";
import { parseDictionary, type TermMeta } from "./parse.ts";
import { validateSegment, type ValidationError } from "./validator.ts";

export class SegmentValidationError extends Error {
  readonly errors: ValidationError[];

  constructor(errors: ValidationError[]) {
    super(`segment validation failed with ${errors.length} error(s): ` +
      errors.slice(0, 3).map((e) => `[${e.code}] ${e.message}`).join("; "));
    this.errors = errors;
  }
}

export interface ReaderStats {
  // Number of posting blocks actually varint-decoded since reader open.
  decodedBlocks: number;
}

export class PostingCursor {
  // Current docID, or undefined when the list is exhausted.
  value: number | undefined;

  private blockIndex = -1;
  private blockIds: number[] = [];
  private posInBlock = 0;
  private readonly data: Buffer;
  private readonly meta: TermMeta;
  private readonly stats: ReaderStats;

  constructor(data: Buffer, meta: TermMeta, stats: ReaderStats) {
    this.data = data;
    this.meta = meta;
    this.stats = stats;
    this.advance();
  }

  get totalCount(): number {
    return this.meta.totalCount;
  }

  private loadBlock(index: number): void {
    const j = this.meta.jump[index];
    const payloadStart = j.offset + BLOCK_HEADER_SIZE;
    const payloadLength = this.data.readUInt32LE(j.offset + 20);
    const payloadEnd = payloadStart + payloadLength;
    const ids = new Array<number>(j.count);
    let p = payloadStart;
    let prev = 0;
    for (let k = 0; k < j.count; k++) {
      const r = decodeVarint(this.data, p, payloadEnd);
      prev = k === 0 ? r.value : prev + r.value;
      p = r.next;
      ids[k] = prev;
    }
    this.blockIndex = index;
    this.blockIds = ids;
    this.posInBlock = 0;
    this.stats.decodedBlocks++;
  }

  // Moves to the next docID and returns it (undefined when exhausted).
  advance(): number | undefined {
    if (this.posInBlock < this.blockIds.length) {
      this.value = this.blockIds[this.posInBlock++];
      return this.value;
    }
    if (this.blockIndex + 1 >= this.meta.jump.length) {
      this.value = undefined;
      return undefined;
    }
    this.loadBlock(this.blockIndex + 1);
    return this.advance();
  }

  // Repositions so that value is the first docID >= target, without
  // decoding the posting blocks that the jump table rules out.
  seek(target: number): void {
    if (this.value !== undefined && this.value >= target) return;
    if (this.value === undefined && this.blockIndex >= 0) return; // exhausted
    const jump = this.meta.jump;
    // Binary search: first block whose maxDocId >= target.
    let lo = 0;
    let hi = jump.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (jump[mid].maxDocId < target) lo = mid + 1;
      else hi = mid;
    }
    if (lo === jump.length) {
      this.value = undefined;
      this.blockIndex = jump.length - 1;
      this.blockIds = [];
      this.posInBlock = 0;
      return;
    }
    if (lo > this.blockIndex) {
      this.loadBlock(lo); // skip blocks [blockIndex+1, lo) undecoded
    }
    while (this.value === undefined || this.value < target) {
      if (this.advance() === undefined) break;
    }
  }
}

export class SegmentReader {
  readonly stats: ReaderStats = { decodedBlocks: 0 };
  private readonly terms = new Map<string, TermMeta>();
  private readonly data: Buffer;

  private constructor(data: Buffer) {
    this.data = data;
    for (const meta of parseDictionary(data)) {
      this.terms.set(meta.term, meta);
    }
  }

  // Opens a segment only if the standalone validator accepts it.
  static open(path: string): SegmentReader {
    return SegmentReader.fromBuffer(readFileSync(path));
  }

  static fromBuffer(data: Buffer): SegmentReader {
    const result = validateSegment(data);
    if (!result.ok) throw new SegmentValidationError(result.errors);
    return new SegmentReader(data);
  }

  get termCount(): number {
    return this.terms.size;
  }

  has(term: string): boolean {
    return this.terms.has(term);
  }

  // Returns a cursor over the term's docIDs, or null for unknown terms.
  cursor(term: string): PostingCursor | null {
    const meta = this.terms.get(term);
    if (meta === undefined) return null;
    return new PostingCursor(this.data, meta, this.stats);
  }

  // All docIDs of a single term, ascending.
  query(term: string): number[] {
    const c = this.cursor(term);
    if (c === null) return [];
    const out: number[] = [];
    while (c.value !== undefined) {
      out.push(c.value);
      c.advance();
    }
    return out;
  }

  // AND intersection of multiple terms, ascending. Uses jump-table seeks
  // so non-matching stretches are skipped without decoding.
  andQuery(terms: string[]): number[] {
    if (terms.length === 0) return [];
    const cursors: PostingCursor[] = [];
    for (const t of terms) {
      const c = this.cursor(t);
      if (c === null) return [];
      cursors.push(c);
    }
    const out: number[] = [];
    let vals = cursors.map((c) => c.value);
    while (vals.every((v) => v !== undefined)) {
      const max = Math.max(...(vals as number[]));
      if (vals.every((v) => v === max)) {
        out.push(max);
        vals = cursors.map((c) => c.advance());
      } else {
        vals = vals.map((v, i) => {
          if (v === max) return v;
          cursors[i].seek(max);
          return cursors[i].value;
        });
      }
    }
    return out;
  }
}
