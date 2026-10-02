/**
 * Compact read-only inverted index segment.
 *
 * Segment layout:
 *   magic     4 bytes  "SIDX"
 *   version   1 byte
 *   docCount  u32
 *   dictOffset u32     absolute offset of the term dictionary
 *   postings region    concatenated posting lists (see postings.ts)
 *   dictionary         front-coded term dictionary (see dictionary.ts)
 *
 * The builder collects (term -> [(docId, tf)]) in memory, then serializes
 * once. The resulting buffer is immutable and read-only: SegmentReader only
 * interprets it, and queries never mutate state.
 */

import { ByteReader, ByteWriter } from "./bytes.ts";
import { buildDictionary, DictionaryReader, type DictEntry } from "./dictionary.ts";
import { encodePostings, PostingsIterator, type Posting } from "./postings.ts";

export const SEGMENT_MAGIC = 0x53494458; // "SIDX"
export const SEGMENT_VERSION = 1;
const HEADER_SIZE = 4 + 1 + 4 + 4;

export function tokenize(text: string): string[] {
  const matches = text.toLowerCase().match(/[a-z0-9]+/g);
  return matches ?? [];
}

export class SegmentBuilder {
  private docCount = 0;
  private readonly termPostings = new Map<string, Posting[]>();

  /** Adds a document; returns its assigned docId (dense, from 0). */
  addDocument(text: string): number {
    const docId = this.docCount++;
    const tfByTerm = new Map<string, number>();
    for (const token of tokenize(text)) {
      tfByTerm.set(token, (tfByTerm.get(token) ?? 0) + 1);
    }
    for (const [term, tf] of tfByTerm) {
      let list = this.termPostings.get(term);
      if (!list) {
        list = [];
        this.termPostings.set(term, list);
      }
      list.push({ docId, tf });
    }
    return docId;
  }

  get size(): number {
    return this.docCount;
  }

  get termCount(): number {
    return this.termPostings.size;
  }

  /** Serializes the segment into an immutable buffer. */
  build(): Uint8Array {
    const terms = [...this.termPostings.keys()].sort();

    // Postings region first: dictionary entries need absolute offsets.
    const postingsRegion = new ByteWriter();
    const dictEntries: DictEntry[] = [];
    for (const term of terms) {
      const list = this.termPostings.get(term)!;
      const encoded = encodePostings(list);
      dictEntries.push({
        term,
        postingsOffset: HEADER_SIZE + postingsRegion.length,
        postingsLength: encoded.length,
        docFreq: list.length,
      });
      postingsRegion.writeBytes(encoded);
    }

    const dictionary = buildDictionary(dictEntries);
    const dictOffset = HEADER_SIZE + postingsRegion.length;

    const out = new ByteWriter(dictOffset + dictionary.length);
    out.writeU32(SEGMENT_MAGIC);
    out.writeByte(SEGMENT_VERSION);
    out.writeU32(this.docCount);
    out.writeU32(dictOffset);
    out.writeBytes(postingsRegion.toUint8Array());
    out.writeBytes(dictionary);
    return out.toUint8Array();
  }
}

export class SegmentReader {
  readonly docCount: number;
  private readonly buf: Uint8Array;
  private readonly dictionary: DictionaryReader;

  constructor(buf: Uint8Array) {
    const reader = new ByteReader(buf);
    const magic = reader.readU32();
    if (magic !== SEGMENT_MAGIC) {
      throw new Error(`bad segment magic: 0x${magic.toString(16)}`);
    }
    const version = reader.readByte();
    if (version !== SEGMENT_VERSION) {
      throw new Error(`unsupported segment version: ${version}`);
    }
    this.docCount = reader.readU32();
    const dictOffset = reader.readU32();
    this.buf = buf;
    this.dictionary = new DictionaryReader(buf, dictOffset);
  }

  get termCount(): number {
    return this.dictionary.termCount;
  }

  /** Raw dictionary lookup; null when the term is absent. */
  lookup(term: string): DictEntry | null {
    return this.dictionary.lookup(term);
  }

  /** Read-only iterator over a term's posting list; null when absent. */
  postings(term: string): PostingsIterator | null {
    const entry = this.dictionary.lookup(term);
    if (!entry) return null;
    return new PostingsIterator(this.buf, entry.postingsOffset);
  }
}
