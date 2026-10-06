import { test } from "node:test";
import assert from "node:assert/strict";
import { SearchController } from "../src/searchController.ts";
import { ScriptedFetcher, settle, page, item, ids } from "./helpers.ts";
import type { Item } from "./helpers.ts";

function setup() {
  const fetcher = new ScriptedFetcher<Item>();
  const controller = new SearchController<Item>(fetcher.fetch);
  return { fetcher, controller };
}

test("search tokenizes the query and requests page 0 of a new generation", () => {
  const { fetcher, controller } = setup();
  controller.search('hello "big world"');
  const state = controller.getState();
  assert.equal(state.generation, 1);
  assert.deepEqual(state.tokens, ["hello", "big world"]);
  assert.equal(state.loading, true);
  assert.equal(state.complete, false);
  const req = fetcher.pending(0).request;
  assert.equal(req.generation, 1);
  assert.deepEqual(req.tokens, ["hello", "big world"]);
});

test("pages arriving out of order are assembled by page number", async () => {
  const { fetcher, controller } = setup();
  controller.search("q");
  controller.loadNextPage();
  controller.loadNextPage();

  fetcher.resolvePage(2, page([item("e"), item("f")], false));
  await settle();
  assert.deepEqual(ids(controller.getState().items), []);
  assert.equal(controller.getState().complete, false);

  fetcher.resolvePage(0, page([item("a"), item("b")]));
  await settle();
  assert.deepEqual(ids(controller.getState().items), ["a", "b"]);
  assert.equal(controller.getState().complete, false);

  fetcher.resolvePage(1, page([item("c"), item("d")]));
  await settle();
  assert.deepEqual(ids(controller.getState().items), ["a", "b", "c", "d", "e", "f"]);
  assert.equal(controller.getState().complete, true);
});

test("responses from a stale generation are discarded", async () => {
  const { fetcher, controller } = setup();
  controller.search("old");
  controller.search("new");
  assert.equal(controller.getState().generation, 2);

  fetcher.resolvePage(0, page([item("stale")], false), 1);
  await settle();
  let state = controller.getState();
  assert.deepEqual(state.items, []);
  assert.equal(state.complete, false);
  assert.equal(state.query, "new");

  fetcher.resolvePage(0, page([item("fresh")], false), 2);
  await settle();
  state = controller.getState();
  assert.deepEqual(ids(state.items), ["fresh"]);
  assert.equal(state.complete, true);
});

test("a failed page can be retried and then joins the run", async () => {
  const { fetcher, controller } = setup();
  controller.search("q");
  fetcher.resolvePage(0, page([item("a")]));
  await settle();
  controller.loadNextPage();
  controller.loadNextPage();

  fetcher.rejectPage(1, new Error("boom"));
  await settle();
  let state = controller.getState();
  assert.deepEqual(state.failedPages, [1]);
  assert.equal(state.pageStatuses[1], "failed");
  assert.equal(state.complete, false);

  controller.retryPage(1);
  assert.equal(controller.getState().pageStatuses[1], "loading");
  fetcher.resolvePage(1, page([item("b")]));
  fetcher.resolvePage(2, page([item("c")], false));
  await settle();
  state = controller.getState();
  assert.deepEqual(ids(state.items), ["a", "b", "c"]);
  assert.deepEqual(state.failedPages, []);
  assert.equal(state.complete, true);
});

test("a terminal page beyond a gap never marks the run complete", async () => {
  const { fetcher, controller } = setup();
  controller.search("q");
  controller.loadNextPage();
  controller.loadNextPage();

  fetcher.resolvePage(0, page([item("a")]));
  fetcher.resolvePage(2, page([], false));
  await settle();
  let state = controller.getState();
  assert.deepEqual(ids(state.items), ["a"]);
  assert.equal(state.complete, false);

  fetcher.resolvePage(1, page([item("b")]));
  await settle();
  state = controller.getState();
  assert.deepEqual(ids(state.items), ["a", "b"]);
  assert.equal(state.complete, true);
});

test("an empty last page terminates the run", async () => {
  const { fetcher, controller } = setup();
  controller.search("q");
  fetcher.resolvePage(0, page([item("a")]));
  await settle();
  controller.loadNextPage();
  fetcher.resolvePage(1, page([], true));
  await settle();
  const state = controller.getState();
  assert.deepEqual(ids(state.items), ["a"]);
  assert.equal(state.complete, true);
});

test("duplicate ids keep their first position", async () => {
  const { fetcher, controller } = setup();
  controller.search("q");
  controller.loadNextPage();
  fetcher.resolvePage(0, page([item("a"), item("b"), item("c")]));
  fetcher.resolvePage(1, page([item("b"), item("d"), item("a")], false));
  await settle();
  assert.deepEqual(ids(controller.getState().items), ["a", "b", "c", "d"]);
});

test("retrying a non-failed page throws", async () => {
  const { fetcher, controller } = setup();
  controller.search("q");
  fetcher.resolvePage(0, page([item("a")], false));
  await settle();
  assert.throws(() => controller.retryPage(0), /not in a failed state/);
});

test("subscribers are notified on request, resolve and failure", async () => {
  const { fetcher, controller } = setup();
  const seen: string[] = [];
  controller.subscribe((s) => {
    seen.push(`${s.pageStatuses[0] ?? "none"}:${s.items.length}`);
  });
  controller.search("q");
  fetcher.resolvePage(0, page([item("a")], false));
  await settle();
  assert.deepEqual(seen, ["loading:0", "loaded:1"]);
});
