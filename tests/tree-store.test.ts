import { test } from "node:test";
import assert from "node:assert/strict";
import { ROOT_ID, TreeStore } from "../src/tree-store.ts";
import { createControllableLoader } from "./helpers.ts";

function ids(nodes: { id: string }[]): string[] {
  return nodes.map((n) => n.id);
}

test("expand loads children asynchronously and reports load count", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);

  const pending = store.expand(ROOT_ID);
  assert.equal(store.get(ROOT_ID)!.status, "loading");
  assert.equal(backend.loadCount(), 1);
  assert.equal(backend.requests[0].parentId, null);

  backend.requests[0].deferred.resolve([
    { id: "a", hasChildren: true },
    { id: "b" },
  ]);
  await pending;

  assert.equal(store.get(ROOT_ID)!.status, "loaded");
  assert.deepEqual(store.rootIds(), ["a", "b"]);
  assert.deepEqual(ids(store.childrenOf(ROOT_ID)), ["a", "b"]);
});

test("concurrent expands of the same generation share a single request", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);

  const first = store.expand(ROOT_ID);
  const second = store.expand(ROOT_ID);
  const third = store.expand(ROOT_ID);
  assert.equal(backend.loadCount(), 1);

  backend.requests[0].deferred.resolve([{ id: "a" }]);
  await Promise.all([first, second, third]);
  assert.deepEqual(store.rootIds(), ["a"]);

  await store.expand(ROOT_ID);
  assert.equal(backend.loadCount(), 1);
});

async function expandRootWith(backend: ReturnType<typeof createControllableLoader>, store: TreeStore) {
  const pending = store.expand(ROOT_ID);
  backend.requestsFor(null)[0].deferred.resolve([{ id: "a", hasChildren: true }]);
  await pending;
}

test("collapse before the response discards it via generation bump", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const pending = store.expand("a");
  assert.equal(backend.requestsFor("a").length, 1);
  const generationBefore = store.get("a")!.generation;

  store.collapse("a");
  assert.ok(store.get("a")!.generation > generationBefore);
  assert.equal(store.get("a")!.expanded, false);

  backend.requestsFor("a")[0].deferred.resolve([{ id: "ghost" }]);
  await pending;

  assert.deepEqual(ids(store.childrenOf("a")), []);
  assert.equal(store.get("ghost"), undefined);
  assert.equal(store.get("a")!.status, "idle");
});

test("collapse then re-expand: stale response discarded when old resolves first", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const firstLoad = store.expand("a");
  store.collapse("a");
  const secondLoad = store.expand("a");
  assert.equal(backend.requestsFor("a").length, 2);

  backend.requestsFor("a")[0].deferred.resolve([{ id: "stale-child" }]);
  await firstLoad;
  assert.deepEqual(ids(store.childrenOf("a")), []);
  assert.equal(store.get("a")!.status, "loading");

  backend.requestsFor("a")[1].deferred.resolve([{ id: "fresh-child" }]);
  await secondLoad;
  assert.deepEqual(ids(store.childrenOf("a")), ["fresh-child"]);
  assert.equal(store.get("stale-child"), undefined);
  assert.equal(store.get("a")!.status, "loaded");
});

test("collapse then re-expand: stale response discarded when old resolves last", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const firstLoad = store.expand("a");
  store.collapse("a");
  const secondLoad = store.expand("a");
  assert.equal(backend.requestsFor("a").length, 2);

  backend.requestsFor("a")[1].deferred.resolve([{ id: "fresh-child" }]);
  await secondLoad;
  assert.deepEqual(ids(store.childrenOf("a")), ["fresh-child"]);

  backend.requestsFor("a")[0].deferred.resolve([{ id: "stale-child" }]);
  await firstLoad;
  assert.deepEqual(ids(store.childrenOf("a")), ["fresh-child"]);
  assert.equal(store.get("stale-child"), undefined);
  assert.equal(store.get("a")!.status, "loaded");
});

test("collapse then re-expand: stale rejection does not clobber the new state", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const firstLoad = store.expand("a");
  store.collapse("a");
  const secondLoad = store.expand("a");

  backend.requestsFor("a")[1].deferred.resolve([{ id: "fresh-child" }]);
  await secondLoad;

  backend.requestsFor("a")[0].deferred.reject(new Error("stale failure"));
  await firstLoad;
  assert.equal(store.get("a")!.status, "loaded");
  assert.equal(store.get("a")!.error, null);
  assert.deepEqual(ids(store.childrenOf("a")), ["fresh-child"]);
});

test("remove before the response leaves no ghost nodes", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const pending = store.expand("a");
  assert.equal(backend.requestsFor("a").length, 1);

  store.remove("a");
  assert.equal(store.get("a"), undefined);
  assert.deepEqual(store.rootIds(), []);

  backend.requestsFor("a")[0].deferred.resolve([{ id: "ghost-1" }, { id: "ghost-2" }]);
  await pending;

  assert.equal(store.get("a"), undefined);
  assert.equal(store.get("ghost-1"), undefined);
  assert.equal(store.get("ghost-2"), undefined);
  assert.deepEqual(store.rootIds(), []);
});

test("remove deletes the whole subtree and detaches from the parent", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const expandA = store.expand("a");
  backend.requestsFor("a")[0].deferred.resolve([{ id: "b", hasChildren: true }]);
  await expandA;

  const expandB = store.expand("b");
  backend.requestsFor("b")[0].deferred.resolve([{ id: "c" }]);
  await expandB;

  store.remove("a");

  assert.equal(store.get("a"), undefined);
  assert.equal(store.get("b"), undefined);
  assert.equal(store.get("c"), undefined);
  assert.deepEqual(store.rootIds(), []);
});

test("selection path tracks the selected node from the root", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const expandA = store.expand("a");
  backend.requestsFor("a")[0].deferred.resolve([{ id: "b", hasChildren: true }]);
  await expandA;

  const expandB = store.expand("b");
  backend.requestsFor("b")[0].deferred.resolve([{ id: "c" }]);
  await expandB;

  assert.deepEqual(store.selectionPath(), []);
  store.select("c");
  assert.equal(store.selectedId(), "c");
  assert.deepEqual(store.selectionPath(), ["a", "b", "c"]);
});

test("removing the selected subtree falls back to the nearest surviving ancestor", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const expandA = store.expand("a");
  backend.requestsFor("a")[0].deferred.resolve([{ id: "b", hasChildren: true }]);
  await expandA;

  const expandB = store.expand("b");
  backend.requestsFor("b")[0].deferred.resolve([{ id: "c" }]);
  await expandB;

  store.select("c");
  store.remove("b");

  assert.equal(store.selectedId(), "a");
  assert.deepEqual(store.selectionPath(), ["a"]);

  store.remove("a");
  assert.equal(store.selectedId(), null);
  assert.deepEqual(store.selectionPath(), []);
});

test("failed load keeps the displayed tree and can be retried", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const expandA = store.expand("a");
  backend.requestsFor("a")[0].deferred.resolve([{ id: "b" }, { id: "c" }]);
  await expandA;
  assert.deepEqual(ids(store.childrenOf("a")), ["b", "c"]);

  const failed = store.refresh("a");
  const boom = new Error("boom");
  backend.requestsFor("a")[1].deferred.reject(boom);
  await failed;

  assert.equal(store.get("a")!.status, "error");
  assert.equal(store.get("a")!.error, boom);
  assert.deepEqual(ids(store.childrenOf("a")), ["b", "c"]);
  assert.notEqual(store.get("b"), undefined);
  assert.notEqual(store.get("c"), undefined);

  const retried = store.expand("a");
  assert.equal(backend.requestsFor("a").length, 3);
  backend.requestsFor("a")[2].deferred.resolve([{ id: "b" }, { id: "d" }]);
  await retried;

  assert.equal(store.get("a")!.status, "loaded");
  assert.equal(store.get("a")!.error, null);
  assert.deepEqual(ids(store.childrenOf("a")), ["b", "d"]);
  assert.notEqual(store.get("b"), undefined);
  assert.equal(store.get("c"), undefined);
  assert.notEqual(store.get("d"), undefined);
});

test("failed first load is retryable and reports the error", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const failed = store.expand("a");
  backend.requestsFor("a")[0].deferred.reject(new Error("offline"));
  await failed;
  assert.equal(store.get("a")!.status, "error");
  assert.deepEqual(ids(store.childrenOf("a")), []);

  const retried = store.expand("a");
  assert.equal(backend.requestsFor("a").length, 2);
  backend.requestsFor("a")[1].deferred.resolve([{ id: "b" }]);
  await retried;
  assert.equal(store.get("a")!.status, "loaded");
  assert.deepEqual(ids(store.childrenOf("a")), ["b"]);
});

test("refresh while a load is in flight invalidates the older request", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const firstLoad = store.expand("a");
  const secondLoad = store.refresh("a");
  assert.equal(backend.requestsFor("a").length, 2);

  backend.requestsFor("a")[1].deferred.resolve([{ id: "fresh" }]);
  await secondLoad;
  backend.requestsFor("a")[0].deferred.resolve([{ id: "stale" }]);
  await firstLoad;

  assert.deepEqual(ids(store.childrenOf("a")), ["fresh"]);
  assert.equal(store.get("stale"), undefined);
});

test("collapse after a successful load keeps the cache and avoids a reload", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const expandA = store.expand("a");
  backend.requestsFor("a")[0].deferred.resolve([{ id: "b" }]);
  await expandA;

  store.collapse("a");
  assert.equal(store.get("a")!.expanded, false);

  await store.expand("a");
  assert.equal(backend.requestsFor("a").length, 1);
  assert.equal(store.get("a")!.expanded, true);
  assert.deepEqual(ids(store.childrenOf("a")), ["b"]);
});

test("reconciled refresh that drops the selected node falls back to its parent", async () => {
  const backend = createControllableLoader();
  const store = new TreeStore(backend.loader);
  await expandRootWith(backend, store);

  const expandA = store.expand("a");
  backend.requestsFor("a")[0].deferred.resolve([{ id: "b" }]);
  await expandA;

  store.select("b");
  assert.deepEqual(store.selectionPath(), ["a", "b"]);

  const refreshed = store.refresh("a");
  backend.requestsFor("a")[1].deferred.resolve([{ id: "c" }]);
  await refreshed;

  assert.equal(store.get("b"), undefined);
  assert.equal(store.selectedId(), "a");
  assert.deepEqual(store.selectionPath(), ["a"]);
});
