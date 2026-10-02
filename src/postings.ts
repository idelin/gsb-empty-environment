/**
 * Posting list encoding with delta-compressed doc IDs and skip pointers.
 *
 * Layout of one posting list:
 *   docFreq      varint   number of postings
 *   skipInterval varint   distance (in postings) between skip entries
 *   skipCount    varint
 *   skip table   skipCount * { docId varint (absolute), dataOffset u32 }
 *                entry i describes the posting at index (i+1)*skipInterval;
 *                dataOffset is relative to the start of the data region and
 *                points at that posting's encoded bytes.
 *   dataLength   u32      byte length of the data region
 *   data         per posting: docDelta varint, tf varint
 *
 * A skip entry stores the absolute doc ID of its boundary posting, so after
 * jumping we can resume delta decoding from a known base without reading the
 * skipped postings.
 */

import { ByteReader, ByteWriter } from "./bytes.ts";

export interface Posting {
  docId: number;
  tf: number;
}

export const DEFAULT_SKIP_INTERVAL = 16;

export function skipIntervalFor(docFreq: number): number {
  return Math.max(4, Math.floor(Math.sqrt(docFreq)));
}

/** Postings must be sorted by ascending docId. */
export function encodePostings(
  postings: Posting[],
  skipInterval = skipIntervalFor(postings.length),
): Uint8Array {
  const docFreq = postings.length;
  const data = new ByteWriter(docFreq * 2);
  const skips: { docId: number; dataOffset: number }[] = [];

  let lastDocId = 0;
  for (let i = 0; i < docFreq; i++) {
    if (i > 0 && i % skipInterval === 0) {
      skips.push({ docId: postings[i].docId, dataOffset: data.length });
    }
    data.writeVarint(postings[i].docId - lastDocId);
    data.writeVarint(postings[i].tf);
    lastDocId = postings[i].docId;
  }

  const out = new ByteWriter();
  out.writeVarint(docFreq);
  out.writeVarint(skipInterval);
  out.writeVarint(skips.length);
  for (const skip of skips) {
    out.writeVarint(skip.docId);
    out.writeU32(skip.dataOffset);
  }
  const dataBytes = data.toUint8Array();
  out.writeU32(dataBytes.length);
  out.writeBytes(dataBytes);
  return out.toUint8Array();
}

export class PostingsIterator {
  readonly docFreq: number;
  /** Number of postings decoded so far (work metric for benchmarks/tests). */
  decodedCount = 0;
  /** Number of skip-pointer jumps performed. */
  skipJumps = 0;

  private readonly skipDocIds: number[] = [];
  private readonly skipOffsets: number[] = [];
  private readonly skipInterval: number;
  private readonly reader: ByteReader;
  private readonly dataStart: number;

  private index = -1; // index of current posting, -1 = before first
  private lastDocId = 0; // delta base for the next decode
  private currentDocId = -1;
  private currentTf = 0;
  private exhausted = false;

  constructor(buf: Uint8Array, offset: number) {
    const reader = new ByteReader(buf, offset);
    this.docFreq = reader.readVarint();
    this.skipInterval = reader.readVarint();
    const skipCount = reader.readVarint();
    for (let i = 0; i < skipCount; i++) {
      this.skipDocIds.push(reader.readVarint());
      this.skipOffsets.push(reader.readU32());
    }
    reader.readU32(); // dataLength, region end is implied by docFreq
    this.dataStart = reader.offset;
    this.reader = reader;
    this.next();
  }

  done(): boolean {
    return this.exhausted;
  }

  docId(): number {
    return this.currentDocId;
  }

  tf(): number {
    return this.currentTf;
  }

  next(): void {
    if (this.exhausted) return;
    if (this.index + 1 >= this.docFreq) {
      this.exhausted = true;
      return;
    }
    const delta = this.reader.readVarint();
    const tf = this.reader.readVarint();
    this.lastDocId += delta;
    this.currentDocId = this.lastDocId;
    this.currentTf = tf;
    this.index++;
    this.decodedCount++;
  }

  /**
   * Move to the first posting with docId >= target, consulting the skip
   * table to leap over regions that cannot contain the target.
   */
  advance(target: number): void {
    if (this.exhausted || this.currentDocId >= target) return;

    // Last skip entry whose docId <= target and that lies ahead of us.
    let lo = 0;
    let hi = this.skipDocIds.length - 1;
    let pick = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >>> 1;
      if (this.skipDocIds[mid] <= target) {
        pick = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    if (pick >= 0) {
      const boundaryIndex = (pick + 1) * this.skipInterval;
      if (boundaryIndex > this.index) {
        // Reposition directly at the boundary posting: we know its absolute
        // docId from the skip entry, so the stored delta can be discarded.
        this.reader.offset = this.dataStart + this.skipOffsets[pick];
        this.reader.readVarint(); // delta, base unknown after a jump
        this.currentTf = this.reader.readVarint();
        this.currentDocId = this.skipDocIds[pick];
        this.lastDocId = this.currentDocId;
        this.index = boundaryIndex;
        this.decodedCount++;
        this.skipJumps++;
      }
    }

    while (!this.exhausted && this.currentDocId < target) {
      this.next();
    }
  }
}
