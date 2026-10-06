import { tokenizeQuery } from "./tokenize.ts";

/** A single page of search results as returned by the backend. */
export interface PageResult<T> {
  items: T[];
  /** false marks the final page; an empty items list is also terminal. */
  hasMore: boolean;
}

/** What the controller passes to the fetcher for each page request. */
export interface PageRequest {
  query: string;
  tokens: string[];
  /** Monotonic id identifying which query generation this page belongs to. */
  generation: number;
}

export type FetchPage<T> = (
  request: PageRequest,
  page: number,
) => Promise<PageResult<T>>;

export type PageStatus = "loading" | "loaded" | "failed";

export interface SearchState<T> {
  /** 0 means no search has been issued yet. */
  generation: number;
  query: string;
  tokens: string[];
  /**
   * Items from the contiguous run of loaded pages starting at page 0,
   * deduplicated by id, first occurrence wins.
   */
  items: T[];
  /** Status per requested page, indexed by page number. */
  pageStatuses: PageStatus[];
  /** Errors per failed page, indexed by page number. */
  pageErrors: (unknown | undefined)[];
  failedPages: number[];
  /** True while any page of the current generation is in flight. */
  loading: boolean;
  /**
   * True only when the contiguous run from page 0 reaches a terminal page.
   * A missing or failed page in between always blocks completion.
   */
  complete: boolean;
}

type Listener<T> = (state: SearchState<T>) => void;

interface PageSlot<T> {
  status: PageStatus;
  result?: PageResult<T>;
  error?: unknown;
}

function isTerminal<T>(result: PageResult<T>): boolean {
  return !result.hasMore || result.items.length === 0;
}

/**
 * Coordinates paginated search over a changing query.
 *
 * Two independent coordinates are tracked:
 * - generation: bumped on every `search()`; responses from older
 *   generations are discarded on arrival.
 * - page: the cursor within one generation; pages may resolve in any
 *   order and are reassembled strictly by page number.
 */
export class SearchController<T extends { id: string }> {
  private readonly fetchPage: FetchPage<T>;
  private readonly listeners = new Set<Listener<T>>();

  private generation = 0;
  private query = "";
  private tokens: string[] = [];
  private pages = new Map<number, PageSlot<T>>();

  constructor(fetchPage: FetchPage<T>) {
    this.fetchPage = fetchPage;
  }

  subscribe(listener: Listener<T>): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Starts a new query generation and requests page 0. */
  search(query: string): void {
    this.generation += 1;
    this.query = query;
    this.tokens = tokenizeQuery(query);
    this.pages = new Map();
    this.requestPage(0);
  }

  /**
   * Requests the first page of the current generation never requested.
   * Failed pages are skipped and stay failed until `retryPage` is called.
   */
  loadNextPage(): void {
    if (this.generation === 0) {
      throw new Error("loadNextPage() before search()");
    }
    let page = 0;
    while (this.pages.has(page)) page += 1;
    this.requestPage(page);
  }

  /** Re-requests a failed page of the current generation. */
  retryPage(page: number): void {
    if (this.pages.get(page)?.status !== "failed") {
      throw new Error(`page ${page} is not in a failed state`);
    }
    this.requestPage(page);
  }

  getState(): SearchState<T> {
    const maxPage = this.pages.size === 0 ? -1 : Math.max(...this.pages.keys());
    const pageStatuses: PageStatus[] = [];
    const pageErrors: (unknown | undefined)[] = [];
    const failedPages: number[] = [];
    let loading = false;
    for (let page = 0; page <= maxPage; page += 1) {
      const slot = this.pages.get(page);
      if (slot) {
        pageStatuses[page] = slot.status;
        pageErrors[page] = slot.error;
        if (slot.status === "failed") failedPages.push(page);
        if (slot.status === "loading") loading = true;
      }
    }

    const items: T[] = [];
    const seen = new Set<string>();
    let complete = false;
    for (let page = 0; ; page += 1) {
      const slot = this.pages.get(page);
      if (!slot || slot.status !== "loaded" || !slot.result) break;
      for (const item of slot.result.items) {
        if (!seen.has(item.id)) {
          seen.add(item.id);
          items.push(item);
        }
      }
      if (isTerminal(slot.result)) {
        complete = true;
        break;
      }
    }

    return {
      generation: this.generation,
      query: this.query,
      tokens: this.tokens,
      items,
      pageStatuses,
      pageErrors,
      failedPages,
      loading,
      complete,
    };
  }

  private requestPage(page: number): void {
    const generation = this.generation;
    this.pages.set(page, { status: "loading" });
    this.emit();

    const request: PageRequest = {
      query: this.query,
      tokens: this.tokens,
      generation,
    };

    this.fetchPage(request, page).then(
      (result) => {
        if (generation !== this.generation) return; // stale generation: discard
        this.pages.set(page, { status: "loaded", result });
        this.emit();
      },
      (error: unknown) => {
        if (generation !== this.generation) return; // stale generation: discard
        this.pages.set(page, { status: "failed", error });
        this.emit();
      },
    );
  }

  private emit(): void {
    const state = this.getState();
    for (const listener of this.listeners) listener(state);
  }
}
