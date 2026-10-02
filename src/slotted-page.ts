/**
 * Slotted-Page binary slot encoder for B+ tree nodes.
 *
 * Page layout (PAGE_SIZE = 4096 bytes, little-endian):
 *
 *   +------------------------------------------------------------------+
 *   | Header (16B) | Slot Directory ---> |  free space  | <--- Records |
 *   +------------------------------------------------------------------+
 *   0              16                   freeStart      freeEnd      4096
 *
 * Header:
 *   [0]  pageType  u8    (1 = leaf, 2 = internal)
 *   [1]  flags     u8    (reserved)
 *   [2]  slotCount u16
 *   [4]  freeStart u16   end of the slot directory (grows toward middle)
 *   [6]  freeEnd   u16   start of the record area  (grows toward middle)
 *   [8]  aux       u32   leaf: next-leaf page id / internal: leftmost child
 *   [12] reserved  u32
 *
 * Slot entry (4 bytes, sorted by key ascending):
 *   [0] recordOffset u16   offset of the record (= start of key bytes)
 *   [2] keyLength    u16
 *
 * Record (variable length, stored from the page end backward):
 *   leaf:     [key bytes][RID: pageId u32, slotId u16]   (value = 6 bytes)
 *   internal: [key bytes][childPageId u32]               (value = 4 bytes)
 *
 * Keys are compared as unsigned byte strings (memcmp order), so the slot
 * directory supports in-page binary search with O(log K) comparisons.
 */

export const PAGE_SIZE = 4096;
export const HEADER_SIZE = 16;
export const SLOT_SIZE = 4;

const OFF_PAGE_TYPE = 0;
const OFF_SLOT_COUNT = 2;
const OFF_FREE_START = 4;
const OFF_FREE_END = 6;
const OFF_AUX = 8;

export const PageType = { Leaf: 1, Internal: 2 } as const;
export type PageType = (typeof PageType)[keyof typeof PageType];

export class PageFullError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PageFullError";
  }
}

export class DuplicateKeyError extends Error {
  constructor(key: string) {
    super(`duplicate key: ${JSON.stringify(key)}`);
    this.name = "DuplicateKeyError";
  }
}

/** Record identifier: physical address of a tuple in the heap file. */
export interface Rid {
  pageId: number;
  slotId: number;
}

const RID_SIZE = 6; // u32 pageId + u16 slotId
const CHILD_ID_SIZE = 4; // u32 child page id

function toKeyBuffer(key: string | Buffer): Buffer {
  return typeof key === "string" ? Buffer.from(key, "utf8") : key;
}

export abstract class SlottedPage {
  /** Raw 4KB page image. */
  readonly buf: Buffer;
  /** Key comparisons performed by the most recent binary search. */
  lastProbeCount = 0;

  protected constructor(pageType: PageType, buf?: Buffer) {
    if (buf !== undefined) {
      if (buf.length !== PAGE_SIZE) {
        throw new RangeError(`page buffer must be ${PAGE_SIZE} bytes, got ${buf.length}`);
      }
      if (buf.readUInt8(OFF_PAGE_TYPE) !== pageType) {
        throw new Error(`page type mismatch: expected ${pageType}, got ${buf.readUInt8(OFF_PAGE_TYPE)}`);
      }
      this.buf = buf;
    } else {
      this.buf = Buffer.alloc(PAGE_SIZE);
      this.buf.writeUInt8(pageType, OFF_PAGE_TYPE);
      this.buf.writeUInt16LE(0, OFF_SLOT_COUNT);
      this.buf.writeUInt16LE(HEADER_SIZE, OFF_FREE_START);
      this.buf.writeUInt16LE(PAGE_SIZE, OFF_FREE_END);
      this.buf.writeUInt32LE(0, OFF_AUX);
    }
  }

  get pageType(): PageType {
    return this.buf.readUInt8(OFF_PAGE_TYPE) as PageType;
  }

  get slotCount(): number {
    return this.buf.readUInt16LE(OFF_SLOT_COUNT);
  }

  /** End of the slot directory; grows toward the middle of the page. */
  get freeStart(): number {
    return this.buf.readUInt16LE(OFF_FREE_START);
  }

  /** Start of the record area; grows toward the middle of the page. */
  get freeEnd(): number {
    return this.buf.readUInt16LE(OFF_FREE_END);
  }

  get freeSpace(): number {
    return this.freeEnd - this.freeStart;
  }

  protected get aux(): number {
    return this.buf.readUInt32LE(OFF_AUX);
  }

  protected set aux(value: number) {
    this.buf.writeUInt32LE(value, OFF_AUX);
  }

  /** Byte offset of slot directory entry `i` within the page. */
  slotDirOffset(i: number): number {
    return HEADER_SIZE + i * SLOT_SIZE;
  }

  /** Byte offset of the record referenced by slot `i`. */
  recordOffset(i: number): number {
    return this.buf.readUInt16LE(this.slotDirOffset(i));
  }

  protected keyLength(i: number): number {
    return this.buf.readUInt16LE(this.slotDirOffset(i) + 2);
  }

  /** Key bytes of slot `i` (zero-copy view into the page). */
  keyAt(i: number): Buffer {
    const off = this.recordOffset(i);
    return this.buf.subarray(off, off + this.keyLength(i));
  }

  protected compareKeyAt(i: number, key: Buffer): number {
    this.lastProbeCount++;
    return this.keyAt(i).compare(key);
  }

  /**
   * Binary search: index of the first slot whose key is >= `key`
   * (== slotCount when every key is smaller). O(log K) comparisons.
   */
  lowerBound(key: string | Buffer): number {
    const target = toKeyBuffer(key);
    this.lastProbeCount = 0;
    let lo = 0;
    let hi = this.slotCount;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.compareKeyAt(mid, target) < 0) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    return lo;
  }

  /**
   * Write a record at the top of the free space and insert a slot for it
   * at sorted position `pos`. Returns the record's offset in the page.
   */
  protected insertRecord(pos: number, key: Buffer, valueLen: number): number {
    const recordLen = key.length + valueLen;
    if (this.freeSpace < SLOT_SIZE + recordLen) {
      throw new PageFullError(
        `page full: need ${SLOT_SIZE + recordLen} bytes, only ${this.freeSpace} free`,
      );
    }
    const count = this.slotCount;
    // Shift slot entries [pos, count) one slot up (directory grows toward middle).
    const dirFrom = this.slotDirOffset(pos);
    this.buf.copyWithin(dirFrom + SLOT_SIZE, dirFrom, dirFrom + (count - pos) * SLOT_SIZE);
    // Write key bytes at the end of the page (records grow toward middle).
    const recordOff = this.freeEnd - recordLen;
    key.copy(this.buf, recordOff);
    // Write the slot entry.
    this.buf.writeUInt16LE(recordOff, dirFrom);
    this.buf.writeUInt16LE(key.length, dirFrom + 2);
    // Update the header.
    this.buf.writeUInt16LE(count + 1, OFF_SLOT_COUNT);
    this.buf.writeUInt16LE(this.freeStart + SLOT_SIZE, OFF_FREE_START);
    this.buf.writeUInt16LE(recordOff, OFF_FREE_END);
    return recordOff;
  }

  /** Remove slot `pos`; reclaims contiguous dead space at the record boundary. */
  protected removeSlot(pos: number): void {
    const count = this.slotCount;
    const dirFrom = this.slotDirOffset(pos);
    this.buf.copyWithin(dirFrom, dirFrom + SLOT_SIZE, dirFrom + (count - pos) * SLOT_SIZE);
    this.buf.writeUInt16LE(count - 1, OFF_SLOT_COUNT);
    this.buf.writeUInt16LE(this.freeStart - SLOT_SIZE, OFF_FREE_START);
    let minOff = PAGE_SIZE;
    for (let i = 0; i < count - 1; i++) {
      const off = this.recordOffset(i);
      if (off < minOff) minOff = off;
    }
    this.buf.writeUInt16LE(count - 1 === 0 ? PAGE_SIZE : minOff, OFF_FREE_END);
  }
}

/** B+ tree leaf node: sorted variable-length keys mapped to RIDs. */
export class LeafPage extends SlottedPage {
  constructor(buf?: Buffer) {
    super(PageType.Leaf, buf);
  }

  /** Page id of the right sibling (leaf-level linked list). */
  get nextLeaf(): number {
    return this.aux;
  }

  setNextLeaf(pageId: number): void {
    this.aux = pageId;
  }

  /**
   * Insert (key, rid) keeping the slot directory sorted.
   * Returns the record offset. Throws DuplicateKeyError / PageFullError.
   */
  insert(key: string | Buffer, rid: Rid): number {
    const k = toKeyBuffer(key);
    if (k.length === 0) throw new RangeError("empty key");
    const pos = this.lowerBound(k);
    if (pos < this.slotCount && this.keyAt(pos).equals(k)) {
      throw new DuplicateKeyError(k.toString("utf8"));
    }
    const recordOff = this.insertRecord(pos, k, RID_SIZE);
    this.buf.writeUInt32LE(rid.pageId, recordOff + k.length);
    this.buf.writeUInt16LE(rid.slotId, recordOff + k.length + 4);
    return recordOff;
  }

  /**
   * Point lookup via in-page binary search. O(log K) key comparisons.
   * Returns the RID, the slot index, and the record's byte offset in the page.
   */
  lookup(key: string | Buffer): { rid: Rid; slot: number; offset: number } | null {
    const k = toKeyBuffer(key);
    const pos = this.lowerBound(k);
    if (pos >= this.slotCount || !this.keyAt(pos).equals(k)) return null;
    const offset = this.recordOffset(pos);
    const valueOff = offset + k.length;
    return {
      rid: {
        pageId: this.buf.readUInt32LE(valueOff),
        slotId: this.buf.readUInt16LE(valueOff + 4),
      },
      slot: pos,
      offset,
    };
  }

  /** Remove `key`; returns false when the key is not present. */
  delete(key: string | Buffer): boolean {
    const k = toKeyBuffer(key);
    const pos = this.lowerBound(k);
    if (pos >= this.slotCount || !this.keyAt(pos).equals(k)) return false;
    this.removeSlot(pos);
    return true;
  }

  /** Iterate entries in ascending key order. */
  *entries(): Generator<{ key: Buffer; rid: Rid; offset: number }> {
    for (let i = 0; i < this.slotCount; i++) {
      const key = this.keyAt(i);
      const offset = this.recordOffset(i);
      const valueOff = offset + key.length;
      yield {
        key,
        rid: {
          pageId: this.buf.readUInt32LE(valueOff),
          slotId: this.buf.readUInt16LE(valueOff + 4),
        },
        offset,
      };
    }
  }
}

/**
 * B+ tree internal node: separator keys mapped to child page ids.
 *
 * Invariant: child of slot `i` covers keys in [key_i, key_{i+1});
 * `leftmostChild` covers keys < key_0.
 */
export class InternalPage extends SlottedPage {
  constructor(buf?: Buffer) {
    super(PageType.Internal, buf);
  }

  /** Child subtree holding all keys smaller than the first separator. */
  get leftmostChild(): number {
    return this.aux;
  }

  setLeftmostChild(pageId: number): void {
    this.aux = pageId;
  }

  /**
   * Insert a separator (key, childPageId) keeping the directory sorted.
   * Returns the record offset. Throws DuplicateKeyError / PageFullError.
   */
  insert(key: string | Buffer, childPageId: number): number {
    const k = toKeyBuffer(key);
    if (k.length === 0) throw new RangeError("empty key");
    const pos = this.lowerBound(k);
    if (pos < this.slotCount && this.keyAt(pos).equals(k)) {
      throw new DuplicateKeyError(k.toString("utf8"));
    }
    const recordOff = this.insertRecord(pos, k, CHILD_ID_SIZE);
    this.buf.writeUInt32LE(childPageId, recordOff + k.length);
    return recordOff;
  }

  /**
   * Route `key` to the page id of the child subtree that may contain it.
   * O(log K) key comparisons.
   */
  findChild(key: string | Buffer): number {
    const k = toKeyBuffer(key);
    const pos = this.lowerBound(k);
    if (pos < this.slotCount && this.keyAt(pos).equals(k)) {
      return this.childAt(pos);
    }
    if (pos === 0) return this.leftmostChild;
    return this.childAt(pos - 1);
  }

  /** Child page id stored in slot `i`. */
  childAt(i: number): number {
    const off = this.recordOffset(i) + this.keyLength(i);
    return this.buf.readUInt32LE(off);
  }

  /** Iterate (separator key, child page id) pairs in ascending key order. */
  *entries(): Generator<{ key: Buffer; childPageId: number; offset: number }> {
    for (let i = 0; i < this.slotCount; i++) {
      yield { key: this.keyAt(i), childPageId: this.childAt(i), offset: this.recordOffset(i) };
    }
  }
}
