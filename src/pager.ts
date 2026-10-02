export const PAGE_SIZE = 4096;

/**
 * In-memory page manager. Pages are fixed-size (PAGE_SIZE) buffers addressed
 * by a 32-bit page id. Page ids start at 1 so that 0 can be used as the
 * null/end-of-chain sentinel everywhere. Freed pages go onto a free list
 * and are reused.
 */
export class Pager {
  private pages: Map<number, Buffer> = new Map();
  private freeList: number[] = [];
  private nextId: number = 1;

  allocate(): number {
    const id = this.freeList.length > 0 ? this.freeList.pop() as number : this.nextId++;
    this.pages.set(id, Buffer.alloc(PAGE_SIZE));
    return id;
  }

  read(id: number): Buffer {
    const page = this.pages.get(id);
    if (!page) throw new Error(`pager: page ${id} does not exist`);
    return page;
  }

  write(id: number, data: Buffer): void {
    if (data.length !== PAGE_SIZE) {
      throw new Error(`pager: page write must be exactly ${PAGE_SIZE} bytes, got ${data.length}`);
    }
    if (!this.pages.has(id)) throw new Error(`pager: page ${id} does not exist`);
    this.pages.set(id, data);
  }

  free(id: number): void {
    if (!this.pages.delete(id)) throw new Error(`pager: page ${id} does not exist`);
    this.freeList.push(id);
  }

  exists(id: number): boolean {
    return this.pages.has(id);
  }

  /** Number of live (allocated, not freed) pages. */
  get pageCount(): number {
    return this.pages.size;
  }
}
