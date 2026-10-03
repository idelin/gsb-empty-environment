import { INVALID_PAGE_ID, PAGE_SIZE, PageType, Pager } from './pager.ts';

/**
 * Overflow page chain linker.
 *
 * Overflow page layout (PAGE_SIZE bytes):
 *   [0]      u8    page type = PageType.Overflow
 *   [1..4]   u32le next overflow page id (INVALID_PAGE_ID terminates the chain)
 *   [5..8]   u32le chunk length in bytes
 *   [9..]    chunk payload (up to OVERFLOW_PAYLOAD_CAPACITY bytes)
 */
export const OVERFLOW_HEADER_SIZE = 9;
export const OVERFLOW_PAYLOAD_CAPACITY = PAGE_SIZE - OVERFLOW_HEADER_SIZE;

export interface OverflowWriteResult {
  firstPageId: number;
  pageIds: number[];
}

export class OverflowChain {
  private readonly pager: Pager;

  constructor(pager: Pager) {
    this.pager = pager;
  }

  /** Append `data` to a freshly allocated chain of overflow pages. */
  write(data: Buffer): OverflowWriteResult {
    if (data.length === 0) return { firstPageId: INVALID_PAGE_ID, pageIds: [] };
    const pageIds: number[] = [];
    let offset = 0;
    while (offset < data.length) {
      const pageId = this.pager.allocate();
      const chunk = data.subarray(offset, offset + OVERFLOW_PAYLOAD_CAPACITY);
      const page = Buffer.alloc(PAGE_SIZE);
      page.writeUInt8(PageType.Overflow, 0);
      page.writeUInt32LE(INVALID_PAGE_ID, 1);
      page.writeUInt32LE(chunk.length, 5);
      chunk.copy(page, OVERFLOW_HEADER_SIZE);
      if (pageIds.length > 0) {
        const prev = Buffer.from(this.pager.read(pageIds[pageIds.length - 1]));
        prev.writeUInt32LE(pageId, 1);
        this.pager.write(pageIds[pageIds.length - 1], prev);
      }
      this.pager.write(pageId, page);
      pageIds.push(pageId);
      offset += chunk.length;
    }
    return { firstPageId: pageIds[0], pageIds };
  }

  /** Walk the chain from `firstPageId` and concatenate all chunks. */
  read(firstPageId: number, expectedLength?: number): Buffer {
    const chunks: Buffer[] = [];
    let pageId = firstPageId;
    while (pageId !== INVALID_PAGE_ID) {
      const page = this.pager.read(pageId);
      if (page.readUInt8(0) !== PageType.Overflow) {
        throw new Error(`page ${pageId} is not an overflow page`);
      }
      const chunkLength = page.readUInt32LE(5);
      chunks.push(Buffer.from(page.subarray(OVERFLOW_HEADER_SIZE, OVERFLOW_HEADER_SIZE + chunkLength)));
      pageId = page.readUInt32LE(1);
    }
    const data = Buffer.concat(chunks);
    if (expectedLength !== undefined && data.length !== expectedLength) {
      throw new Error(`overflow chain length mismatch: expected ${expectedLength}, got ${data.length}`);
    }
    return data;
  }

  /** Collect the page ids of a chain, in link order. */
  pageIdsOf(firstPageId: number): number[] {
    const ids: number[] = [];
    let pageId = firstPageId;
    while (pageId !== INVALID_PAGE_ID) {
      ids.push(pageId);
      pageId = this.pager.read(pageId).readUInt32LE(1);
    }
    return ids;
  }
}
