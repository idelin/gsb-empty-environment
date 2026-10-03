/**
 * PageManager simulates a paged storage engine.
 * Every B+ tree node occupies exactly one page identified by a page id.
 * Freed pages are pushed onto a Free Page List and reused by later allocations.
 */
export class PageManager<T> {
  private pages: (T | null)[] = [];
  private freeList: number[] = [];
  private allocated = 0;

  /** Allocate a page, reusing a slot from the free list when available. */
  allocate(page: T): number {
    this.allocated++;
    const reused = this.freeList.pop();
    if (reused !== undefined) {
      this.pages[reused] = page;
      return reused;
    }
    this.pages.push(page);
    return this.pages.length - 1;
  }

  /** Return a page to the free list. */
  free(id: number): void {
    if (this.pages[id] === null || this.pages[id] === undefined) {
      throw new Error(`double free or invalid page id: ${id}`);
    }
    this.pages[id] = null;
    this.freeList.push(id);
    this.allocated--;
  }

  get(id: number): T {
    const page = this.pages[id];
    if (page === null || page === undefined) {
      throw new Error(`page ${id} is not allocated`);
    }
    return page;
  }

  isFree(id: number): boolean {
    return this.pages[id] === null;
  }

  /** Pages currently in use. */
  get allocatedCount(): number {
    return this.allocated;
  }

  /** Pages sitting in the free page list. */
  get freeCount(): number {
    return this.freeList.length;
  }

  /** Total page slots ever created (allocated + free). */
  get capacity(): number {
    return this.pages.length;
  }
}
