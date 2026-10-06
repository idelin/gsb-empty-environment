import type { PageRequest, PageResult } from "../src/searchController.ts";

export interface Item {
  id: string;
  label?: string;
}

interface Pending<T> {
  request: PageRequest;
  page: number;
  resolve: (result: PageResult<T>) => void;
  reject: (error: unknown) => void;
}

/**
 * A fetcher whose responses are resolved manually, in any order the test
 * chooses, so out-of-order arrival, retries and stale generations can be
 * exercised deterministically.
 */
export class ScriptedFetcher<T extends { id: string }> {
  readonly requests: Pending<T>[] = [];

  readonly fetch = (request: PageRequest, page: number): Promise<PageResult<T>> =>
    new Promise<PageResult<T>>((resolve, reject) => {
      this.requests.push({ request, page, resolve, reject });
    });

  /** Oldest unmatched in-flight request for a page, optionally per generation. */
  pending(page: number, generation?: number): Pending<T> {
    const hit = this.requests.find(
      (r) =>
        r.page === page &&
        (generation === undefined || r.request.generation === generation),
    );
    if (!hit) {
      throw new Error(`no pending request for page ${page}`);
    }
    return hit;
  }

  resolvePage(page: number, result: PageResult<T>, generation?: number): void {
    const hit = this.pending(page, generation);
    this.requests.splice(this.requests.indexOf(hit), 1);
    hit.resolve(result);
  }

  rejectPage(page: number, error: unknown, generation?: number): void {
    const hit = this.pending(page, generation);
    this.requests.splice(this.requests.indexOf(hit), 1);
    hit.reject(error);
  }
}

/** Flushes the microtask queue so controller .then handlers run. */
export async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

export function page<T>(items: T[], hasMore = true): PageResult<T> {
  return { items, hasMore };
}

export function item(id: string): Item {
  return { id };
}

export function ids(items: { id: string }[]): string[] {
  return items.map((it) => it.id);
}
