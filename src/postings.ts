/**
 * Compressed posting lists with skip pointers.
 *
 * A posting list stores (docId, termFreq) pairs. DocIDs are delta-encoded
 * as varints; frequencies are plain varints. Every SKIP_INTERVAL postings a
 * skip entry is recorded:
 *
 *   skip[i] = { baseDocId: docId of posting (i+1)*K - 1,
 *               offset:    byte offset of posting (i+1)*K in the data area }
 *
 * Jumping to skip entry i lands the iterator *on* posting (i+1)*K - 1 with
 * its absolute docID known, so delta decoding can resume from the recorded
 * offset without any absolute-docID side channel.
 *
 * Layout of one encoded list:
 *   [skipCount varint]
 *   [skip entries: (baseDocId varint, offset varint) * skipCount]
 *   [posting data: (docDelta varint, freq varint) * docCount]
 */
import { ByteReader, ByteWriter } from './bytes.ts';

export const SKIP_INTERVAL = 16;

export const NO_MORE_DOCS = Number.MAX_SAFE_INTEGER;

export interface Posting {
  docId: number;
  freq: number;
}

/** Encodes postings (must be sorted by ascending docId) into the binary list format. */
export function encodePostings(postings: Posting[]): Uint8Array {
  const data = new ByteWriter();
  const skipDocIds: number[] = [];
  const skipOffsets: number[] = [];

  let previousDocId = 0;
  for (let i = 0; i < postings.length; i++) {
    if (i > 0 && i % SKIP_INTERVAL === 0) {
      skipDocIds.push(previousDocId);
      skipOffsets.push(data.length);
    }
    data.writeVarint(postings[i].docId - previousDocId);
    data.writeVarint(postings[i].freq);
    previousDocId = postings[i].docId;
  }

  const out = new ByteWriter();
  out.writeVarint(skipDocIds.length);
  for (let i = 0; i < skipDocIds.length; i++) {
    out.writeVarint(skipDocIds[i]);
    out.writeVarint(skipOffsets[i]);
  }
  out.writeBytes(data.toUint8Array());
  return out.toUint8Array();
}

/** Counters proving that skip pointers avoid decoding useless postings. */
export interface IteratorStats {
  /** Postings actually varint-decoded while iterating. */
  decoded: number;
  /** Times a skip pointer repositioned the read cursor. */
  skipsUsed: number;
}

/**
 * Forward-only iterator over one encoded posting list.
 * `advance()` uses the skip table to leap over runs of postings whose
 * docIDs are all below the target, decoding at most SKIP_INTERVAL - 1
 * postings afterwards.
 */
export class PostingIterator {
  readonly stats: IteratorStats = { decoded: 0, skipsUsed: 0 };

  private readonly reader: ByteReader;
  private readonly skipDocIds: number[] = [];
  private readonly skipOffsets: number[] = [];
  private readonly dataStart: number;
  readonly docCount: number;

  private index = -1;
  private currentDoc = 0;
  private currentFreq = 0;

  constructor(data: Uint8Array, offset: number, docCount: number) {
    this.docCount = docCount;
    const reader = new ByteReader(data, offset);
    const skipCount = reader.readVarint();
    for (let i = 0; i < skipCount; i++) {
      this.skipDocIds.push(reader.readVarint());
      this.skipOffsets.push(reader.readVarint());
    }
    this.dataStart = reader.offset;
    this.reader = reader;
  }

  /** Current docID. Valid only after a successful next()/advance(). */
  doc(): number {
    return this.currentDoc;
  }

  /** Term frequency of the current posting. */
  freq(): number {
    return this.currentFreq;
  }

  /** Moves to the next posting; returns its docID or NO_MORE_DOCS. */
  next(): number {
    if (this.currentDoc === NO_MORE_DOCS) return NO_MORE_DOCS;
    if (this.index + 1 >= this.docCount) return this.exhaust();
    this.decodeNext();
    return this.currentDoc;
  }

  /**
   * Moves to the first posting with docID >= target, using skip pointers.
   * Returns that docID or NO_MORE_DOCS.
   */
  advance(target: number): number {
    if (this.currentDoc === NO_MORE_DOCS) return NO_MORE_DOCS;
    if (this.index >= 0 && this.currentDoc >= target) return this.currentDoc;

    // Rightmost skip entry whose base docID is still below the target.
    let lo = 0;
    let hi = this.skipDocIds.length - 1;
    let best = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >>> 1;
      if (this.skipDocIds[mid] < target) {
        best = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    // Only jump forward, never backwards.
    if (best >= 0 && this.skipDocIds[best] > this.currentDoc) {
      this.currentDoc = this.skipDocIds[best];
      this.index = (best + 1) * SKIP_INTERVAL - 1;
      this.reader.offset = this.dataStart + this.skipOffsets[best];
      this.stats.skipsUsed++;
    }

    while (this.currentDoc < target) {
      if (this.index + 1 >= this.docCount) return this.exhaust();
      this.decodeNext();
    }
    return this.currentDoc;
  }

  private decodeNext(): void {
    const delta = this.reader.readVarint();
    this.currentFreq = this.reader.readVarint();
    this.currentDoc += delta;
    this.index++;
    this.stats.decoded++;
  }

  private exhaust(): number {
    this.index = this.docCount;
    this.currentDoc = NO_MORE_DOCS;
    this.currentFreq = 0;
    return NO_MORE_DOCS;
  }
}
