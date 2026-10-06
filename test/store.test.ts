import { describe, expect, test } from "bun:test";
import { SearchStore } from "../src/store";
import type { PageCursor } from "../src/store";

interface Item {
  id: string;
  label: string;
}

const item = (id: string): Item => ({ id, label: `item-${id}` });
const ids = (items: Item[]): string[] => items.map((entry) => entry.id);

function beginAndApply(
  store: SearchStore<Item>,
  page: number,
  items: Item[],
  generation = store.generation,
): boolean {
  const cursor: PageCursor = { generation, page };
  if (store.pageStatus(page) !== "loading") {
    const begun = store.beginPage(page);
    if (!begun) return false;
  }
  return store.applyPage(cursor, items);
}

describe("SearchStore generations", () => {
  test("setQuery bumps generation and clears pages", () => {
    const store = new SearchStore<Item>();
    const first = store.setQuery("alpha");
    beginAndApply(store, 0, [item("a")]);
    const second = store.setQuery("beta");
    expect(second).toBe(first + 1);
    expect(store.items).toEqual([]);
    expect(store.pageStatus(0)).toBeUndefined();
  });

  test("discards responses from stale generations", () => {
    const store = new SearchStore<Item>();
    const stale = store.setQuery("old");
    store.beginPage(0);
    store.setQuery("new");
    store.beginPage(0);
    const applied = store.applyPage(
      { generation: stale, page: 0 },
      [item("stale")],
    );
    expect(applied).toBe(false);
    expect(store.items).toEqual([]);
    expect(store.pageStatus(0)).toBe("loading");
  });

  test("discards failures from stale generations", () => {
    const store = new SearchStore<Item>();
    const stale = store.setQuery("old");
    store.beginPage(0);
    store.setQuery("new");
    expect(
      store.failPage({ generation: stale, page: 0 }, new Error("late")),
    ).toBe(false);
    expect(store.failedPages).toEqual([]);
  });
});

describe("SearchStore page assembly", () => {
  test("assembles out-of-order pages by page number", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    beginAndApply(store, 2, [item("e"), item("f")]);
    beginAndApply(store, 0, [item("a"), item("b")]);
    beginAndApply(store, 1, [item("c"), item("d")]);
    expect(ids(store.items)).toEqual(["a", "b", "c", "d", "e", "f"]);
  });

  test("exposes only the contiguous loaded prefix", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    beginAndApply(store, 0, [item("a")]);
    beginAndApply(store, 2, [item("c")]);
    expect(ids(store.items)).toEqual(["a"]);
  });

  test("dedupes by id keeping the first occurrence position", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    beginAndApply(store, 0, [item("a"), item("b"), item("a")]);
    beginAndApply(store, 1, [item("b"), item("c")]);
    expect(ids(store.items)).toEqual(["a", "b", "c"]);
  });
});

describe("SearchStore failure and retry", () => {
  test("failed page can be retried and then applied", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    const cursor = store.beginPage(0)!;
    expect(store.failPage(cursor, new Error("boom"))).toBe(true);
    expect(store.failedPages).toEqual([0]);

    const retry = store.retryPage(0)!;
    expect(retry.generation).toBe(store.generation);
    expect(store.applyPage(retry, [item("a")])).toBe(true);
    expect(ids(store.items)).toEqual(["a"]);
    expect(store.failedPages).toEqual([]);
  });

  test("retryPage rejects pages that are not failed", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    expect(store.retryPage(0)).toBeNull();
    store.beginPage(0);
    expect(store.retryPage(0)).toBeNull();
  });
});

describe("SearchStore completion", () => {
  test("empty page marks the end; complete only when no gaps", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    beginAndApply(store, 0, [item("a")]);
    expect(store.isComplete).toBe(false);
    beginAndApply(store, 1, []);
    expect(store.isComplete).toBe(true);
    expect(store.missingPages).toEqual([]);
  });

  test("never claims complete across a missing page", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    beginAndApply(store, 0, [item("a")]);
    beginAndApply(store, 2, []); // end known at page 2, page 1 absent
    expect(store.isComplete).toBe(false);
    expect(store.missingPages).toEqual([1]);
    beginAndApply(store, 1, [item("b")]);
    expect(store.isComplete).toBe(true);
    expect(ids(store.items)).toEqual(["a", "b"]);
  });

  test("never claims complete across a failed page until retried", () => {
    const store = new SearchStore<Item>();
    store.setQuery("q");
    beginAndApply(store, 0, [item("a")]);
    const cursor = store.beginPage(1)!;
    beginAndApply(store, 2, []);
    store.failPage(cursor, new Error("boom"));
    expect(store.isComplete).toBe(false);
    expect(store.missingPages).toEqual([1]);

    const retry = store.retryPage(1)!;
    store.applyPage(retry, [item("b")]);
    expect(store.isComplete).toBe(true);
    expect(ids(store.items)).toEqual(["a", "b"]);
  });

  test("short page relative to pageSize marks the end", () => {
    const store = new SearchStore<Item>({ pageSize: 2 });
    store.setQuery("q");
    beginAndApply(store, 0, [item("a"), item("b")]);
    expect(store.isComplete).toBe(false);
    beginAndApply(store, 1, [item("c")]);
    expect(store.isComplete).toBe(true);
  });
});
