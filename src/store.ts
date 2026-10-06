import { tokenize } from "./tokenize";

export type PageStatus = "loading" | "loaded" | "failed";

/** Identifies one page request within one query generation. */
export interface PageCursor {
  readonly generation: number;
  readonly page: number;
}

export interface PageRecord<T> {
  readonly page: number;
  status: PageStatus;
  items: T[];
  error?: unknown;
}

export interface SearchStoreOptions {
  /**
   * Expected items per full page. A loaded page with fewer items marks the
   * end of the result set. Defaults to Infinity, so only an empty page
   * marks the end.
   */
  pageSize?: number;
}

/**
 * Holds paginated search results for one query at a time.
 *
 * - Every `setQuery` bumps the generation; responses tagged with an older
 *   generation are discarded.
 * - Pages are keyed by page number, so out-of-order responses within one
 *   generation are assembled in page order.
 * - Failed pages can be retried; completion is only reported once every
 *   page up to the known last page is loaded (no gaps).
 */
export class SearchStore<T extends { id: string }> {
  private readonly pageSize: number;
  private currentGeneration = 0;
  private rawQuery = "";
  private queryTokens: string[] = [];
  private pages = new Map<number, PageRecord<T>>();
  private endPage: number | null = null;

  constructor(options: SearchStoreOptions = {}) {
    this.pageSize = options.pageSize ?? Number.POSITIVE_INFINITY;
  }

  get generation(): number {
    return this.currentGeneration;
  }

  get query(): string {
    return this.rawQuery;
  }

  get tokens(): readonly string[] {
    return this.queryTokens;
  }

  /** Starts a new query generation and drops all prior page state. */
  setQuery(raw: string): number {
    this.currentGeneration += 1;
    this.rawQuery = raw;
    this.queryTokens = tokenize(raw);
    this.pages.clear();
    this.endPage = null;
    return this.currentGeneration;
  }

  /**
   * Marks a page as in-flight and returns its cursor, or null when the
   * page is already loading/loaded or the number is invalid.
   */
  beginPage(page: number): PageCursor | null {
    if (!Number.isInteger(page) || page < 0) return null;
    const record = this.pages.get(page);
    if (record && record.status !== "failed") return null;
    this.pages.set(page, { page, status: "loading", items: [] });
    return { generation: this.currentGeneration, page };
  }

  /**
   * Applies a successful response. Returns false (discarding the payload)
   * when the cursor belongs to a stale generation or an unknown page.
   */
  applyPage(cursor: PageCursor, items: T[]): boolean {
    if (cursor.generation !== this.currentGeneration) return false;
    const record = this.pages.get(cursor.page);
    if (!record || record.status !== "loading") return false;
    record.status = "loaded";
    record.items = items;
    record.error = undefined;
    const isShortPage =
      Number.isFinite(this.pageSize) && items.length < this.pageSize;
    if (items.length === 0 || isShortPage) {
      this.endPage =
        this.endPage === null ? cursor.page : Math.min(this.endPage, cursor.page);
    }
    return true;
  }

  /** Marks an in-flight page as failed. Stale generations are discarded. */
  failPage(cursor: PageCursor, error: unknown): boolean {
    if (cursor.generation !== this.currentGeneration) return false;
    const record = this.pages.get(cursor.page);
    if (!record || record.status !== "loading") return false;
    record.status = "failed";
    record.error = error;
    return true;
  }

  /** Re-arms a failed page for another attempt. */
  retryPage(page: number): PageCursor | null {
    const record = this.pages.get(page);
    if (!record || record.status !== "failed") return null;
    record.status = "loading";
    record.error = undefined;
    return { generation: this.currentGeneration, page };
  }

  pageStatus(page: number): PageStatus | undefined {
    return this.pages.get(page)?.status;
  }

  get failedPages(): number[] {
    return this.sortedPages((record) => record.status === "failed");
  }

  /**
   * Pages that still block completion: every page from 0 to the known last
   * page that is not loaded. Empty when the end is not known yet.
   */
  get missingPages(): number[] {
    if (this.endPage === null) return [];
    const missing: number[] = [];
    for (let page = 0; page <= this.endPage; page += 1) {
      if (this.pages.get(page)?.status !== "loaded") missing.push(page);
    }
    return missing;
  }

  /**
   * True only when the last page is known (a short/empty page was loaded)
   * and no page up to it is missing or failed.
   */
  get isComplete(): boolean {
    return this.endPage !== null && this.missingPages.length === 0;
  }

  /**
   * Next page number to request, or null when paging cannot advance:
   * the end is known, or an earlier page failed (failed pages must be
   * retried before moving forward). In-flight pages do not block, so
   * several pages may be requested concurrently.
   */
  nextPageToLoad(): number | null {
    if (this.endPage !== null) return null;
    for (let page = 0; ; page += 1) {
      const record = this.pages.get(page);
      if (!record) return page;
      if (record.status === "failed") return null;
    }
  }

  /**
   * Contiguous loaded prefix starting at page 0, assembled in page order
   * regardless of arrival order, deduped by item id keeping the first
   * occurrence position.
   */
  get items(): T[] {
    const seen = new Set<string>();
    const assembled: T[] = [];
    for (let page = 0; ; page += 1) {
      const record = this.pages.get(page);
      if (!record || record.status !== "loaded") break;
      for (const item of record.items) {
        if (!seen.has(item.id)) {
          seen.add(item.id);
          assembled.push(item);
        }
      }
      if (this.endPage !== null && page >= this.endPage) break;
    }
    return assembled;
  }

  private sortedPages(predicate: (record: PageRecord<T>) => boolean): number[] {
    const pages: number[] = [];
    for (const record of this.pages.values()) {
      if (predicate(record)) pages.push(record.page);
    }
    return pages.sort((a, b) => a - b);
  }
}
