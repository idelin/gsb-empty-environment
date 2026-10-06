import { SearchStore } from "./store";
import type { PageCursor, SearchStoreOptions } from "./store";

export interface PageRequest {
  readonly query: string;
  readonly tokens: readonly string[];
  readonly page: number;
  readonly generation: number;
}

export type PageFetcher<T> = (request: PageRequest) => Promise<T[]>;

/**
 * Drives a SearchStore with an async page fetcher. Every in-flight request
 * carries a (generation, page) cursor, so responses from superseded
 * queries are discarded by the store no matter when they resolve.
 */
export class SearchCoordinator<T extends { id: string }> {
  readonly store: SearchStore<T>;
  private readonly fetcher: PageFetcher<T>;

  constructor(fetcher: PageFetcher<T>, options: SearchStoreOptions = {}) {
    this.store = new SearchStore<T>(options);
    this.fetcher = fetcher;
  }

  /** Starts a new query generation and requests its first page. */
  search(raw: string): number {
    const generation = this.store.setQuery(raw);
    this.request(0);
    return generation;
  }

  /** Requests the next page. Returns false when paging cannot advance. */
  loadNext(): boolean {
    const next = this.store.nextPageToLoad();
    if (next === null) return false;
    return this.request(next);
  }

  /** Re-requests every failed page of the current generation. */
  retryFailed(): number {
    const failed = this.store.failedPages;
    for (const page of failed) {
      const cursor = this.store.retryPage(page);
      if (cursor) this.load(cursor);
    }
    return failed.length;
  }

  private request(page: number): boolean {
    const cursor = this.store.beginPage(page);
    if (!cursor) return false;
    this.load(cursor);
    return true;
  }

  private load(cursor: PageCursor): void {
    const request: PageRequest = {
      query: this.store.query,
      tokens: this.store.tokens,
      page: cursor.page,
      generation: cursor.generation,
    };
    this.fetcher(request).then(
      (items) => {
        this.store.applyPage(cursor, items);
      },
      (error) => {
        this.store.failPage(cursor, error);
      },
    );
  }
}
