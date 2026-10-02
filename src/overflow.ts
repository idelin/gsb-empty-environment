import { Pager, PAGE_SIZE } from './pager.ts';

export const OVERFLOW_PAGE_TAG = 0x4f; // 'O'
/** tag(1) + nextPageId(4) + chunkLen(4) */
export const OVERFLOW_HEADER_SIZE = 9;
export const OVERFLOW_CHUNK_CAPACITY = PAGE_SIZE - OVERFLOW_HEADER_SIZE;

/**
 * Chained overflow pages. Payload that does not fit inline in a B+ tree node
 * is chopped into PAGE_SIZE chunks; each overflow page stores one chunk plus
 * a pointer to the next page id (0 terminates the chain).
 *
 * Page layout:
 *   byte  0      : tag (0x4F)
 *   bytes 1..4   : next overflow page id (UInt32LE, 0 = end of chain)
 *   bytes 5..8   : chunk length (UInt32LE)
 *   bytes 9..    : chunk data
 */
export class OverflowChain {
  private pager: Pager;

  constructor(pager: Pager) {
    this.pager = pager;
  }

  /** Writes `data` across a fresh chain of overflow pages. Returns the head page id. */
  write(data: Buffer): number {
    if (data.length === 0) throw new Error('overflow: empty payload must stay inline');
    let offset = 0;
    let firstId = 0;
    let prevId = -1;
    while (offset < data.length) {
      const id = this.pager.allocate();
      const end = Math.min(offset + OVERFLOW_CHUNK_CAPACITY, data.length);
      const chunk = data.subarray(offset, end);
      const page = Buffer.alloc(PAGE_SIZE);
      page.writeUInt8(OVERFLOW_PAGE_TAG, 0);
      page.writeUInt32LE(0, 1);
      page.writeUInt32LE(chunk.length, 5);
      chunk.copy(page, OVERFLOW_HEADER_SIZE);
      this.pager.write(id, page);
      if (prevId !== -1) {
        this.pager.read(prevId).writeUInt32LE(id, 1);
      } else {
        firstId = id;
      }
      prevId = id;
      offset = end;
    }
    return firstId;
  }

  /** Returns the ordered list of page ids forming the chain. */
  chainIds(firstId: number): number[] {
    const ids: number[] = [];
    let id = firstId;
    while (id !== 0) {
      const page = this.pager.read(id);
      if (page.readUInt8(0) !== OVERFLOW_PAGE_TAG) {
        throw new Error(`overflow: page ${id} is not an overflow page`);
      }
      ids.push(id);
      id = page.readUInt32LE(1);
    }
    return ids;
  }

  /** Walks the chain and reassembles the original payload. */
  read(firstId: number, totalLen?: number): Buffer {
    const parts: Buffer[] = [];
    let id = firstId;
    while (id !== 0) {
      const page = this.pager.read(id);
      if (page.readUInt8(0) !== OVERFLOW_PAGE_TAG) {
        throw new Error(`overflow: page ${id} is not an overflow page`);
      }
      const len = page.readUInt32LE(5);
      parts.push(page.subarray(OVERFLOW_HEADER_SIZE, OVERFLOW_HEADER_SIZE + len));
      id = page.readUInt32LE(1);
    }
    const data = Buffer.concat(parts);
    return totalLen === undefined ? data : data.subarray(0, totalLen);
  }

  /** Releases every page of the chain back to the pager free list. */
  free(firstId: number): void {
    for (const id of this.chainIds(firstId)) this.pager.free(id);
  }
}
