/**
 * Compact read-only inverted-index segment.
 *
 * Binary layout:
 *   [magic "SIS1" (4 bytes)]
 *   [dictionaryLength varint]
 *   [dictionary block]   — prefix-compressed term dictionary
 *   [postings region]    — concatenated compressed posting lists
 *
 * Term pointers stored in the dictionary are relative to the start of the
 * postings region, keeping every varint small.
 */
import { ByteReader, ByteWriter, compareBytes } from './bytes.ts';
import {
  DictionaryReader,
  encodeDictionary,
  type TermEntry,
  type TermPointer,
} from './dictionary.ts';
import {
  NO_MORE_DOCS,
  PostingIterator,
  encodePostings,
  type IteratorStats,
  type Posting,
} from './postings.ts';

const MAGIC = Uint8Array.from([0x53, 0x49, 0x53, 0x31]); // "SIS1"

/** Collects documents and serializes them into an immutable segment buffer. */
export class SegmentBuilder {
  private readonly termPostings = new Map<string, Map<number, number>>();
  private documentCount = 0;

  /** Adds one document; repeated tokens increase the term frequency. */
  addDocument(docId: number, tokens: Iterable<string>): void {
    if (!Number.isSafeInteger(docId) || docId < 0) {
      throw new RangeError(`docId must be a non-negative safe integer, got ${docId}`);
    }
    const frequencies = new Map<string, number>();
    for (const token of tokens) {
      const term = token.toLowerCase();
      frequencies.set(term, (frequencies.get(term) ?? 0) + 1);
    }
    for (const [term, freq] of frequencies) {
      let postings = this.termPostings.get(term);
      if (postings === undefined) {
        postings = new Map();
        this.termPostings.set(term, postings);
      }
      postings.set(docId, freq);
    }
    this.documentCount++;
  }

  get docCount(): number {
    return this.documentCount;
  }

  get termCount(): number {
    return this.termPostings.size;
  }

  /** Serializes the accumulated index into the compact segment format. */
  build(): Uint8Array {
    const encoder = new TextEncoder();
    const terms = [...this.termPostings.keys()].sort((a, b) =>
      compareBytes(encoder.encode(a), encoder.encode(b)),
    );

    const postingsRegion = new ByteWriter();
    const entries: TermEntry[] = [];
    for (const term of terms) {
      const postings: Posting[] = [...this.termPostings.get(term)!.entries()]
        .sort((a, b) => a[0] - b[0])
        .map(([docId, freq]) => ({ docId, freq }));
      const encoded = encodePostings(postings);
      entries.push({
        term,
        pointer: {
          offset: postingsRegion.length,
          length: encoded.length,
          docCount: postings.length,
        },
      });
      postingsRegion.writeBytes(encoded);
    }

    const dictionary = encodeDictionary(entries);

    const segment = new ByteWriter();
    segment.writeBytes(MAGIC);
    segment.writeVarint(dictionary.length);
    segment.writeBytes(dictionary);
    segment.writeBytes(postingsRegion.toUint8Array());
    return segment.toUint8Array();
  }
}

export interface TermStats extends IteratorStats {
  term: string;
  docCount: number;
}

export interface IntersectionResult {
  docIds: number[];
  /** Per-term decode/skip counters, ordered shortest posting list first. */
  stats: TermStats[];
}

/** Read-only query side of a segment buffer. */
export class Segment {
  private readonly buffer: Uint8Array;
  private readonly dictionary: DictionaryReader;
  private readonly postingsStart: number;

  constructor(buffer: Uint8Array) {
    this.buffer = buffer;
    if (
      buffer.length < MAGIC.length ||
      !MAGIC.every((byte, i) => buffer[i] === byte)
    ) {
      throw new Error('not a SIS1 segment buffer');
    }
    const reader = new ByteReader(buffer, MAGIC.length);
    const dictionaryLength = reader.readVarint();
    this.dictionary = new DictionaryReader(buffer, reader.offset);
    this.postingsStart = reader.offset + dictionaryLength;
  }

  get termCount(): number {
    return this.dictionary.termCount;
  }

  /** All terms in byte order (mainly for tooling/tests). */
  terms(): string[] {
    return [...this.dictionary.terms()];
  }

  /** Posting-list pointer for a term, or null when the term is unknown. */
  termInfo(term: string): TermPointer | null {
    return this.dictionary.lookup(term.toLowerCase());
  }

  /** Opens a forward-only iterator over the term's posting list. */
  lookup(term: string): PostingIterator | null {
    const pointer = this.dictionary.lookup(term.toLowerCase());
    if (pointer === null) return null;
    return new PostingIterator(
      this.buffer,
      this.postingsStart + pointer.offset,
      pointer.docCount,
    );
  }

  /**
   * Boolean AND over multiple terms ("database AND storage").
   *
   * Iterators are sorted by posting-list length so the shortest list drives
   * the merge; every other list is positioned with skip-pointer `advance()`,
   * leaping over runs of docIDs that cannot appear in the intersection.
   */
  intersect(terms: string[]): IntersectionResult {
    const named: { term: string; iterator: PostingIterator }[] = [];
    for (const term of terms) {
      const iterator = this.lookup(term);
      if (iterator === null) return { docIds: [], stats: [] };
      named.push({ term: term.toLowerCase(), iterator });
    }
    if (named.length === 0) return { docIds: [], stats: [] };
    named.sort((a, b) => a.iterator.docCount - b.iterator.docCount);

    const driver = named[0].iterator;
    const docIds: number[] = [];

    let candidate = driver.next();
    outer: while (candidate !== NO_MORE_DOCS) {
      for (let i = 1; i < named.length; i++) {
        const doc = named[i].iterator.advance(candidate);
        if (doc === NO_MORE_DOCS) break outer;
        if (doc !== candidate) {
          candidate = driver.advance(doc);
          continue outer;
        }
      }
      docIds.push(candidate);
      candidate = driver.next();
    }

    return {
      docIds,
      stats: named.map(({ term, iterator }) => ({
        term,
        docCount: iterator.docCount,
        decoded: iterator.stats.decoded,
        skipsUsed: iterator.stats.skipsUsed,
      })),
    };
  }
}
