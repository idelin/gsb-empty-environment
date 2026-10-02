/**
 * Slotted-Page binary encoder for B+ tree nodes.
 *
 * Physical layout of a 4 KiB page:
 *
 *   0                   HEADER_SIZE                 freeStart        freeEnd        PAGE_SIZE
 *   +--------------------+---------------------------+---------------+--------------+
 *   | page header        | slot directory (grows ->) |  free space   | cells (<- grow) |
 *   +--------------------+---------------------------+---------------+--------------+
 *
 * Header (12 bytes):
 *   u8  pageType   (1 = leaf, 2 = internal)
 *   u8  flags      (reserved)
 *   u16 slotCount
 *   u16 freeStart  (end of the slot directory)
 *   u16 freeEnd    (start of the cell area, grows backwards from PAGE_SIZE)
 *   u32 reserved
 *
 * Slot entry (4 bytes), sorted by key ascending:
 *   u16 cellOffset, u16 cellLength
 *
 * Cell formats (compact, no padding):
 *   leaf:     u16 keyLen | key bytes | u32 ridPageId | u32 ridSlotId
 *   internal: u16 keyLen | key bytes | u32 childPageId
 */

export const PAGE_SIZE = 4096;
export const HEADER_SIZE = 12;
export const SLOT_SIZE = 4;

export const PageType = { Leaf: 1, Internal: 2 } as const;
export type PageType = (typeof PageType)[keyof typeof PageType];

export interface Rid {
  pageId: number;
  slotId: number;
}

export interface SlotLocation {
  /** Index inside the slot directory (0-based, key-sorted order). */
  slotIndex: number;
  /** Absolute byte offset of the cell inside the page. */
  offset: number;
  /** Cell length in bytes. */
  length: number;
}

/** Optional probe counter used to verify O(log K) search behaviour. */
export interface Probe {
  comparisons: number;
}

function compareBytes(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

export class PageFullError extends Error {
  constructor(needed: number, available: number) {
    super(`page full: need ${needed} bytes, only ${available} available`);
    this.name = "PageFullError";
  }
}

export class DuplicateKeyError extends Error {
  constructor(key: string) {
    super(`duplicate key: ${JSON.stringify(key)}`);
    this.name = "DuplicateKeyError";
  }
}

export class SlottedPage {
  readonly buffer: Buffer;
  readonly pageType: PageType;

  private constructor(buffer: Buffer, pageType: PageType) {
    this.buffer = buffer;
    this.pageType = pageType;
  }

  static create(pageType: PageType, pageSize: number = PAGE_SIZE): SlottedPage {
    const buffer = Buffer.alloc(pageSize);
    buffer.writeUInt8(pageType, 0);
    buffer.writeUInt8(0, 1);
    buffer.writeUInt16LE(0, 2); // slotCount
    buffer.writeUInt16LE(HEADER_SIZE, 4); // freeStart
    buffer.writeUInt16LE(pageSize, 6); // freeEnd
    buffer.writeUInt32LE(0, 8);
    return new SlottedPage(buffer, pageType);
  }

  static from(buffer: Buffer): SlottedPage {
    const pageType = buffer.readUInt8(0) as PageType;
    if (pageType !== PageType.Leaf && pageType !== PageType.Internal) {
      throw new Error(`invalid page type: ${pageType}`);
    }
    return new SlottedPage(buffer, pageType);
  }

  get slotCount(): number {
    return this.buffer.readUInt16LE(2);
  }

  get freeSpace(): number {
    return this.buffer.readUInt16LE(6) - this.buffer.readUInt16LE(4);
  }

  private set slotCount(value: number) {
    this.buffer.writeUInt16LE(value, 2);
  }

  private slotOffset(slotIndex: number): number {
    return HEADER_SIZE + slotIndex * SLOT_SIZE;
  }

  private readSlot(slotIndex: number): { offset: number; length: number } {
    const at = this.slotOffset(slotIndex);
    return {
      offset: this.buffer.readUInt16LE(at),
      length: this.buffer.readUInt16LE(at + 2),
    };
  }

  private writeSlot(slotIndex: number, offset: number, length: number): void {
    const at = this.slotOffset(slotIndex);
    this.buffer.writeUInt16LE(offset, at);
    this.buffer.writeUInt16LE(length, at + 2);
  }

  private readCellKey(offset: number): Buffer {
    const keyLen = this.buffer.readUInt16LE(offset);
    return this.buffer.subarray(offset + 2, offset + 2 + keyLen);
  }

  keyAt(slotIndex: number): string {
    const { offset } = this.readSlot(slotIndex);
    return this.readCellKey(offset).toString("utf8");
  }

  /**
   * Binary search over the slot directory.
   * Returns the slot index of `key`, or -(insertionPoint + 1) when absent.
   */
  private binarySearch(key: Buffer, probe?: Probe): number {
    let lo = 0;
    let hi = this.slotCount - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >>> 1;
      const { offset } = this.readSlot(mid);
      if (probe) probe.comparisons++;
      const cmp = compareBytes(this.readCellKey(offset), key);
      if (cmp === 0) return mid;
      if (cmp < 0) lo = mid + 1;
      else hi = mid - 1;
    }
    return -(lo + 1);
  }

  private cellLengthFor(key: Buffer): number {
    return 2 + key.length + (this.pageType === PageType.Leaf ? 8 : 4);
  }

  private ensureCapacity(key: Buffer): number {
    const cellLength = this.cellLengthFor(key);
    const needed = cellLength + SLOT_SIZE;
    if (this.freeSpace < needed) {
      throw new PageFullError(needed, this.freeSpace);
    }
    return cellLength;
  }

  private insertCell(key: Buffer, valueWriter: (at: number) => void): SlotLocation {
    const cellLength = this.ensureCapacity(key);
    const probe: Probe = { comparisons: 0 };
    const found = this.binarySearch(key, probe);
    if (found >= 0) throw new DuplicateKeyError(key.toString("utf8"));
    const insertionPoint = -(found + 1);

    // Grow the cell area downwards from the end of the page.
    const freeEnd = this.buffer.readUInt16LE(6) - cellLength;
    this.buffer.writeUInt16LE(freeEnd, 6);

    // Write the cell: u16 keyLen | key | value
    this.buffer.writeUInt16LE(key.length, freeEnd);
    key.copy(this.buffer, freeEnd + 2);
    valueWriter(freeEnd + 2 + key.length);

    // Grow the slot directory forwards: shift slots right of insertionPoint.
    const count = this.slotCount;
    const dirEnd = this.slotOffset(count);
    if (insertionPoint < count) {
      this.buffer.copyWithin(
        this.slotOffset(insertionPoint + 1),
        this.slotOffset(insertionPoint),
        dirEnd,
      );
    }
    this.writeSlot(insertionPoint, freeEnd, cellLength);
    this.slotCount = count + 1;
    this.buffer.writeUInt16LE(dirEnd + SLOT_SIZE, 4); // freeStart

    return { slotIndex: insertionPoint, offset: freeEnd, length: cellLength };
  }

  /** Leaf-only: insert key -> RID, keeping slots sorted ascending. */
  insertLeaf(key: string, rid: Rid): SlotLocation {
    if (this.pageType !== PageType.Leaf) throw new Error("not a leaf page");
    const keyBytes = Buffer.from(key, "utf8");
    return this.insertCell(keyBytes, (at) => {
      this.buffer.writeUInt32LE(rid.pageId, at);
      this.buffer.writeUInt32LE(rid.slotId, at + 4);
    });
  }

  /** Internal-only: insert separator key -> child page id. */
  insertInternal(key: string, childPageId: number): SlotLocation {
    if (this.pageType !== PageType.Internal) throw new Error("not an internal page");
    const keyBytes = Buffer.from(key, "utf8");
    return this.insertCell(keyBytes, (at) => {
      this.buffer.writeUInt32LE(childPageId, at);
    });
  }

  /** Locate the slot for `key` in O(log K). Returns null when absent. */
  find(key: string, probe?: Probe): SlotLocation | null {
    const keyBytes = Buffer.from(key, "utf8");
    const idx = this.binarySearch(keyBytes, probe);
    if (idx < 0) return null;
    const { offset, length } = this.readSlot(idx);
    return { slotIndex: idx, offset, length };
  }

  /** Leaf-only: read the RID stored at a located slot. */
  readRid(location: SlotLocation): Rid {
    const keyLen = this.buffer.readUInt16LE(location.offset);
    const at = location.offset + 2 + keyLen;
    return {
      pageId: this.buffer.readUInt32LE(at),
      slotId: this.buffer.readUInt32LE(at + 4),
    };
  }

  /** Internal-only: read the child page id stored at a located slot. */
  readChildPageId(location: SlotLocation): number {
    const keyLen = this.buffer.readUInt16LE(location.offset);
    return this.buffer.readUInt32LE(location.offset + 2 + keyLen);
  }

  /** Iterate keys in ascending slot order (for debugging / demos). */
  *keys(): Generator<string> {
    for (let i = 0; i < this.slotCount; i++) yield this.keyAt(i);
  }
}
