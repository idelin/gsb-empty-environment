import { describe, expect, test } from "bun:test";
import { SearchCoordinator } from "../src/coordinator";
import type { PageRequest } from "../src/coordinator";

interface Item {
  id: string;
}

const item = (id: string): Item => ({ id });
const ids = (items: Item[]): string[] => items.map((entry) => entry.id);

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T[]) => void;
  reject: (error: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T[]) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T[]>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Fetcher whose responses are resolved manually, in any order the test picks. */
class ControlledFetcher {
  readonly requests: PageRequest[] = [];
  private pending = new Map<string, Deferred<Item[]>>();

  fetch = (request: PageRequest): Promise<Item[]> => {
    this.requests.push(request);
    const slot = deferred<Item[]>();
    this.pending.set(this.key(request.generation, request.page), slot);
    return slot.promise;
  };

  resolve(generation: number, page: number, items: Item[]): void {
    this.slot(generation, page).resolve(items);
  }

  reject(generation: number, page: number, error: unknown): void {
    this.slot(generation, page).reject(error);
  }

  private slot(generation: number, page: number): Deferred<Item[]> {
    const slot = this.pending.get(this.key(generation, page));
    if (!slot) {
      throw new Error(`no pending request for gen ${generation} page ${page}`);
    }
    return slot;
  }

  private key(generation: number, page: number): string {
    return `${generation}:${page}`;
  }
}

const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

describe("SearchCoordinator", () => {
  test("passes tokenized query to the fetcher", async () => {
    const fetcher = new ControlledFetcher();
    const coordinator = new SearchCoordinator<Item>(fetcher.fetch);
    coordinator.search("  Red  Apples, red ");
    expect(fetcher.requests).toHaveLength(1);
    expect(fetcher.requests[0]!.tokens).toEqual(["red", "apples"]);
    expect(fetcher.requests[0]!.page).toBe(0);
    fetcher.resolve(1, 0, []);
    await tick();
    expect(coordinator.store.isComplete).toBe(true);
  });

  test("assembles pages resolved out of order by page number", async () => {
    const fetcher = new ControlledFetcher();
    const coordinator = new SearchCoordinator<Item>(fetcher.fetch);
    const generation = coordinator.search("q");
    coordinator.loadNext();
    coordinator.loadNext();
    expect(fetcher.requests.map((request) => request.page)).toEqual([0, 1, 2]);

    fetcher.resolve(generation, 2, []);
    fetcher.resolve(generation, 1, [item("c"), item("d")]);
    fetcher.resolve(generation, 0, [item("a"), item("b")]);
    await tick();

    expect(ids(coordinator.store.items)).toEqual(["a", "b", "c", "d"]);
    expect(coordinator.store.isComplete).toBe(true);
  });

  test("drops responses from a superseded query", async () => {
    const fetcher = new ControlledFetcher();
    const coordinator = new SearchCoordinator<Item>(fetcher.fetch);
    const stale = coordinator.search("old");
    const current = coordinator.search("new");
    expect(current).toBe(stale + 1);

    fetcher.resolve(stale, 0, [item("stale")]);
    await tick();
    expect(coordinator.store.items).toEqual([]);

    fetcher.resolve(current, 0, [item("fresh")]);
    await tick();
    expect(ids(coordinator.store.items)).toEqual(["fresh"]);
  });

  test("retries a failed page without losing earlier pages", async () => {
    const fetcher = new ControlledFetcher();
    const coordinator = new SearchCoordinator<Item>(fetcher.fetch);
    const generation = coordinator.search("q");
    coordinator.loadNext();
    coordinator.loadNext();

    fetcher.resolve(generation, 0, [item("a")]);
    fetcher.reject(generation, 1, new Error("network"));
    fetcher.resolve(generation, 2, []);
    await tick();

    expect(coordinator.store.failedPages).toEqual([1]);
    expect(coordinator.store.isComplete).toBe(false);
    expect(ids(coordinator.store.items)).toEqual(["a"]);

    expect(coordinator.retryFailed()).toBe(1);
    fetcher.resolve(generation, 1, [item("b")]);
    await tick();

    expect(ids(coordinator.store.items)).toEqual(["a", "b"]);
    expect(coordinator.store.isComplete).toBe(true);
  });

  test("does not advance past a failed page", async () => {
    const fetcher = new ControlledFetcher();
    const coordinator = new SearchCoordinator<Item>(fetcher.fetch);
    const generation = coordinator.search("q");
    fetcher.reject(generation, 0, new Error("network"));
    await tick();

    expect(coordinator.loadNext()).toBe(false);
    expect(fetcher.requests).toHaveLength(1);

    coordinator.retryFailed();
    fetcher.resolve(generation, 0, []);
    await tick();
    expect(coordinator.store.isComplete).toBe(true);
  });

  test("empty last page completes the result set", async () => {
    const fetcher = new ControlledFetcher();
    const coordinator = new SearchCoordinator<Item>(fetcher.fetch);
    const generation = coordinator.search("q");
    fetcher.resolve(generation, 0, [item("a"), item("b")]);
    await tick();
    expect(coordinator.store.isComplete).toBe(false);

    coordinator.loadNext();
    fetcher.resolve(generation, 1, []);
    await tick();
    expect(coordinator.store.isComplete).toBe(true);
    expect(ids(coordinator.store.items)).toEqual(["a", "b"]);
  });
});
