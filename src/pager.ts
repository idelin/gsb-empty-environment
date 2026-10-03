/** Fixed-size page manager. Pages live in memory but are addressed by page id,
 *  exactly like a disk-backed buffer pool would address them. */

export const PAGE_SIZE = 4096;
export const INVALID_PAGE_ID = 0xffffffff;

export const PageType = {
  Internal: 1,
  Leaf: 2,
  Overflow: 3,
} as const;

export type PageType = (typeof PageType)[keyof typeof PageType];

export class Pager {
  private pages: Buffer[] = [];

  allocate(): number {
    const pageId = this.pages.length;
    this.pages.push(Buffer.alloc(PAGE_SIZE));
    return pageId;
  }

  read(pageId: number): Buffer {
    const page = this.pages[pageId];
    if (!page) throw new RangeError(`invalid page id ${pageId}`);
    return page;
  }

  write(pageId: number, data: Buffer): void {
    if (data.length !== PAGE_SIZE) {
      throw new RangeError(`page write must be exactly ${PAGE_SIZE} bytes, got ${data.length}`);
    }
    if (!this.pages[pageId]) throw new RangeError(`invalid page id ${pageId}`);
    this.pages[pageId] = Buffer.from(data);
  }

  get pageCount(): number {
    return this.pages.length;
  }
}
