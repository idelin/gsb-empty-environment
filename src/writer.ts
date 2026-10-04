// Segment index writer. Builds the binary layout described in FORMAT.md
// from (term -> ascending docID list) input. Duplicate IDs are merged.

import { writeFileSync } from "node:fs";
import {
  BLOCK_HEADER_SIZE,
  BLOCK_MAGIC,
  DEFAULT_BLOCK_SIZE,
  DIGEST_SIZE,
  HEADER_SIZE,
  MAGIC,
  MAX_DOC_ID,
  MAX_TERM_BYTES,
  VERSION,
  compareTerms,
  crc32,
  encodeVarint,
  termHash,
} from "./format.ts";

export interface WriteOptions {
  // Max number of docIDs per posting block.
  blockSize?: number;
}

interface JumpEntry {
  offset: number; // relative to the term's postings span
  maxDocId: number;
  count: number;
}

export class SegmentWriter {
  private lists = new Map<string, number[]>();

  add(term: string, docId: number): void {
    if (!Number.isInteger(docId) || docId < 0 || docId > MAX_DOC_ID) {
      throw new RangeError(`docID out of range [0, ${MAX_DOC_ID}]: ${docId}`);
    }
    if (Buffer.byteLength(term, "utf8") > MAX_TERM_BYTES) {
      throw new RangeError(`term too long: ${JSON.stringify(term.slice(0, 32))}...`);
    }
    let list = this.lists.get(term);
    if (list === undefined) {
      list = [];
      this.lists.set(term, list);
    }
    list.push(docId);
  }

  addAll(term: string, docIds: Iterable<number>): void {
    for (const id of docIds) this.add(term, id);
  }

  // Serializes the segment. Deterministic: terms are sorted by UTF-8 bytes,
  // IDs are sorted ascending with duplicates merged.
  toBuffer(options: WriteOptions = {}): Buffer {
    const blockSize = options.blockSize ?? DEFAULT_BLOCK_SIZE;
    if (!Number.isInteger(blockSize) || blockSize < 1) {
      throw new RangeError(`invalid blockSize: ${blockSize}`);
    }

    const terms = [...this.lists.keys()].sort(compareTerms);
    const postingsParts: Buffer[] = [];
    let postingsLength = 0;
    const dictParts: Buffer[] = [];

    for (const term of terms) {
      const ids = [...new Set(this.lists.get(term)!)].sort((a, b) => a - b);
      const termOffset = postingsLength;
      const jump: JumpEntry[] = [];

      for (let start = 0; start < ids.length; start += blockSize) {
        const block = ids.slice(start, start + blockSize);
        const deltas: number[] = [];
        for (let i = 0; i < block.length; i++) {
          deltas.push(i === 0 ? block[0] : block[i] - block[i - 1]);
        }
        const payload = Buffer.concat(deltas.map(encodeVarint));
        const header = Buffer.alloc(BLOCK_HEADER_SIZE);
        header.writeUInt32LE(BLOCK_MAGIC, 0);
        header.writeUInt32LE(termHash(term), 4);
        header.writeUInt32LE(block[0], 8);
        header.writeUInt32LE(block[block.length - 1], 12);
        header.writeUInt32LE(block.length, 16);
        header.writeUInt32LE(payload.length, 20);
        jump.push({ offset: postingsLength - termOffset, maxDocId: block[block.length - 1], count: block.length });
        postingsParts.push(header, payload);
        postingsLength += header.length + payload.length;
      }

      const termBytes = Buffer.from(term, "utf8");
      const entry = Buffer.alloc(2 + termBytes.length + 16 + jump.length * 12);
      let p = 0;
      entry.writeUInt16LE(termBytes.length, p); p += 2;
      termBytes.copy(entry, p); p += termBytes.length;
      entry.writeUInt32LE(termOffset, p); p += 4;
      entry.writeUInt32LE(postingsLength - termOffset, p); p += 4;
      entry.writeUInt32LE(ids.length, p); p += 4;
      entry.writeUInt32LE(jump.length, p); p += 4;
      for (const j of jump) {
        entry.writeUInt32LE(j.offset, p); p += 4;
        entry.writeUInt32LE(j.maxDocId, p); p += 4;
        entry.writeUInt32LE(j.count, p); p += 4;
      }
      dictParts.push(entry);
    }

    const dict = Buffer.concat(dictParts);
    const header = Buffer.alloc(HEADER_SIZE);
    header.writeUInt32LE(MAGIC, 0);
    header.writeUInt32LE(VERSION, 4);
    header.writeUInt32LE(terms.length, 8);
    header.writeUInt32LE(HEADER_SIZE, 12);
    header.writeUInt32LE(dict.length, 16);
    header.writeUInt32LE(HEADER_SIZE + dict.length, 20);
    header.writeUInt32LE(postingsLength, 24);

    const body = Buffer.concat([header, dict, ...postingsParts]);
    const digest = Buffer.alloc(DIGEST_SIZE);
    digest.writeUInt32LE(crc32(body), 0);
    return Buffer.concat([body, digest]);
  }

  write(path: string, options: WriteOptions = {}): void {
    writeFileSync(path, this.toBuffer(options));
  }
}
